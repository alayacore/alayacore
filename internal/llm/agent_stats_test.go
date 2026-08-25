package llm

import (
	"context"
	"encoding/json"
	"iter"
	"testing"
	"time"
)

// timedProvider simulates a provider stream with realistic timing:
// 50ms of request/network latency before the first delta (TTFT), then
// 250ms of generation, then the step completes with authoritative usage.
type timedProvider struct{}

func (m *timedProvider) StreamMessages(
	_ context.Context,
	_ []ContentPart,
	_ []ToolDefinition,
	_, _ string,
) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		time.Sleep(50 * time.Millisecond)
		if !yield(TextDeltaEvent{Delta: "Hello ", Index: 0}, nil) {
			return
		}
		time.Sleep(250 * time.Millisecond)
		if !yield(TextDeltaEvent{Delta: "world", Index: 0}, nil) {
			return
		}
		yield(StepCompleteEvent{
			Contents: []ContentPart{
				&TextPart{Text: "Hello world", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
			},
			Usage: Usage{
				InputTokens:  10,
				OutputTokens: 100,
			},
			StopReason: "end_turn",
		}, nil)
	}, nil
}

func (m *timedProvider) SetReasoningLevel(_ int)                       {}
func (m *timedProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *timedProvider) SetVideoConfig(_ int, _ int)                   {}

// TestAgentStepStats verifies that Agent.Stream computes per-step speed
// metrics from the provider's authoritative usage and fires OnStepStats
// exactly once, before OnStepFinish.
//
// TokensPerSec is the END-TO-END throughput: 100 tokens over the whole
// round trip (Duration ≈ 300ms) → ≈ 333 tok/s. No reliability gates.
func TestAgentStepStats(t *testing.T) {
	a := NewAgent(AgentConfig{Provider: &timedProvider{}, MaxSteps: 5})

	var gotStats []StepStats
	finishOrder := 0
	statsOrder := 0
	callbacks := StreamCallbacks{
		OnStepStats: func(stats StepStats) error {
			gotStats = append(gotStats, stats)
			statsOrder = finishOrder // record ordering: stats fired before finish
			return nil
		},
		OnStepFinish: func(_ []ContentPart, _ Usage) error {
			finishOrder++
			return nil
		},
	}

	if _, err := a.Stream(context.Background(), nil, callbacks); err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	if len(gotStats) != 1 {
		t.Fatalf("OnStepStats called %d times, want 1", len(gotStats))
	}
	st := gotStats[0]

	if st.Step != 1 {
		t.Errorf("Step = %d, want 1", st.Step)
	}
	if st.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want 100", st.OutputTokens)
	}

	// TTFT: ~50ms request latency before the first delta. Tolerate
	// scheduling noise on both sides.
	if st.TimeToFirstToken < 40*time.Millisecond {
		t.Errorf("TimeToFirstToken = %v, want >= 40ms", st.TimeToFirstToken)
	}
	if st.TimeToFirstToken > 5*time.Second {
		t.Errorf("TimeToFirstToken = %v, want <= 5s", st.TimeToFirstToken)
	}

	// Duration: ~300ms (50ms latency + 250ms generation), NOT including
	// any post-stream work.
	if st.Duration < 290*time.Millisecond {
		t.Errorf("Duration = %v, want >= 290ms", st.Duration)
	}
	if st.Duration > 5*time.Second {
		t.Errorf("Duration = %v, want <= 5s", st.Duration)
	}

	// TPS = OutputTokens / Duration (end-to-end, TTFT included).
	wantMin := 100 / st.Duration.Seconds() * 0.8
	wantMax := 100 / st.Duration.Seconds() * 1.2
	if st.TokensPerSec < wantMin || st.TokensPerSec > wantMax {
		t.Errorf("TokensPerSec = %.1f, want within [%.1f, %.1f] (100 tokens / end-to-end Duration)",
			st.TokensPerSec, wantMin, wantMax)
	}

	// OnStepStats must fire before OnStepFinish (so consumers can publish
	// stats ahead of the finish broadcast).
	if statsOrder != 0 || finishOrder != 1 {
		t.Errorf("ordering: stats fired at %d, finish at %d; want stats before finish", statsOrder, finishOrder)
	}
}

// TestAgentStepStatsToolOnlyStep verifies that a tool-call-only step still
// records TTFT (from the tool input delta) and a Duration, while TPS is 0
// when the provider reports no output tokens.
func TestAgentStepStatsToolOnlyStep(t *testing.T) {
	a := NewAgent(AgentConfig{
		Provider: &toolOnlyTimedProvider{},
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 5,
	})

	var got []StepStats
	callbacks := StreamCallbacks{
		OnStepStats: func(stats StepStats) error {
			got = append(got, stats)
			return nil
		},
	}
	if _, err := a.Stream(context.Background(), nil, callbacks); err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("OnStepStats called %d times, want 2 (tool step + text step)", len(got))
	}
	st := got[0] // step 1: tool-only
	if st.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 for tool-only step", st.OutputTokens)
	}
	if st.TokensPerSec != 0 {
		t.Errorf("TokensPerSec = %.1f, want 0 (no output tokens)", st.TokensPerSec)
	}
	if st.TimeToFirstToken == 0 {
		t.Error("TimeToFirstToken = 0, want tool input delta counted as first token")
	}
	if st.Duration == 0 {
		t.Error("Duration = 0, want stream duration recorded")
	}

	// Step 2: 5 output tokens — the simple end-to-end formula still
	// reports a speed (no gates, display never blanks).
	if st2 := got[1]; st2.OutputTokens != 5 || st2.TokensPerSec <= 0 {
		t.Errorf("step 2: OutputTokens=%d TokensPerSec=%.1f, want 5/>0 (no gates)",
			st2.OutputTokens, st2.TokensPerSec)
	}
}

// TestAgentStepStatsAlwaysShows verifies the "simple and stable" contract:
// every completed step with output tokens reports a TokensPerSec — short
// outputs, burst deliveries, and normal streaming alike. No reliability
// gates, no blank display.
func TestAgentStepStatsAlwaysShows(t *testing.T) {
	a := NewAgent(AgentConfig{
		Provider: &variedProvider{},
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 5,
	})

	var got []StepStats
	callbacks := StreamCallbacks{
		OnStepStats: func(stats StepStats) error {
			got = append(got, stats)
			return nil
		},
	}
	if _, err := a.Stream(context.Background(), nil, callbacks); err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("OnStepStats called %d times, want 3", len(got))
	}
	for i, st := range got {
		if st.OutputTokens == 0 {
			continue // no tokens → nothing to measure
		}
		if st.TokensPerSec <= 0 {
			t.Errorf("step %d: OutputTokens=%d TokensPerSec=%.1f, want >0 (always shows)",
				i+1, st.OutputTokens, st.TokensPerSec)
		}
	}
}

// variedProvider emits, per call:
//
//  1. step 1 — tool call, 20 output tokens, long window (short output);
//  2. step 2 — tool call, 100 output tokens, BURST (all deltas arrive
//     immediately — the previously-inflated case);
//  3. step 3 — text, 100 output tokens, normal streaming.
//
// All three must report TokensPerSec under the simple end-to-end formula.
type variedProvider struct {
	calls int
}

func (m *variedProvider) StreamMessages(
	_ context.Context,
	_ []ContentPart,
	_ []ToolDefinition,
	_, _ string,
) (iter.Seq2[StreamEvent, error], error) {
	m.calls++
	return func(yield func(StreamEvent, error) bool) {
		switch m.calls {
		case 1:
			time.Sleep(100 * time.Millisecond)
			if !yield(ToolInputStartEvent{ID: "c1", Name: "t", Index: 0}, nil) {
				return
			}
			if !yield(ToolInputDeltaEvent{ID: "c1", Delta: `{}`, Index: 0}, nil) {
				return
			}
			if !yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 0}, nil) {
				return
			}
			time.Sleep(200 * time.Millisecond)
			yield(StepCompleteEvent{
				Contents: []ContentPart{
					&ToolInputPart{ID: "c1", Name: "t", Input: json.RawMessage(`{}`)},
				},
				Usage:      Usage{OutputTokens: 20},
				StopReason: "tool_use",
			}, nil)
		case 2:
			if !yield(ToolInputStartEvent{ID: "c2", Name: "t", Index: 0}, nil) {
				return
			}
			if !yield(ToolInputDeltaEvent{ID: "c2", Delta: `{}`, Index: 0}, nil) {
				return
			}
			if !yield(ToolInputCompleteEvent{ID: "c2", Input: json.RawMessage(`{}`), Index: 0}, nil) {
				return
			}
			yield(StepCompleteEvent{
				Contents: []ContentPart{
					&ToolInputPart{ID: "c2", Name: "t", Input: json.RawMessage(`{}`)},
				},
				Usage:      Usage{OutputTokens: 100},
				StopReason: "tool_use",
			}, nil)
		default:
			time.Sleep(50 * time.Millisecond)
			if !yield(TextDeltaEvent{Delta: "x", Index: 0}, nil) {
				return
			}
			time.Sleep(250 * time.Millisecond)
			if !yield(TextDeltaEvent{Delta: "y", Index: 0}, nil) {
				return
			}
			yield(StepCompleteEvent{
				Contents: []ContentPart{
					&TextPart{Text: "xy", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
				},
				Usage:      Usage{OutputTokens: 100},
				StopReason: "end_turn",
			}, nil)
		}
	}, nil
}

func (m *variedProvider) SetReasoningLevel(_ int)                       {}
func (m *variedProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *variedProvider) SetVideoConfig(_ int, _ int)                   {}

// toolOnlyTimedProvider emits a tool call on its first call (step 1, zero
// output tokens) and a text-only response on the second (step 2, 5 tokens).
type toolOnlyTimedProvider struct {
	calls int
}

func (m *toolOnlyTimedProvider) StreamMessages(
	_ context.Context,
	_ []ContentPart,
	_ []ToolDefinition,
	_, _ string,
) (iter.Seq2[StreamEvent, error], error) {
	m.calls++
	return func(yield func(StreamEvent, error) bool) {
		if m.calls == 1 {
			// Step 1: tool call with zero output tokens.
			if !yield(ToolInputStartEvent{ID: "c1", Name: "t", Index: 0}, nil) {
				return
			}
			if !yield(ToolInputDeltaEvent{ID: "c1", Delta: `{"path":"/tmp"`, Index: 0}, nil) {
				return
			}
			if !yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{"path":"/tmp"}`), Index: 0}, nil) {
				return
			}
			yield(StepCompleteEvent{
				Contents: []ContentPart{
					&ToolInputPart{ID: "c1", Name: "t", Input: json.RawMessage(`{"path":"/tmp"}`)},
				},
				Usage:      Usage{},
				StopReason: "tool_use",
			}, nil)
			return
		}
		// Step 2: text-only response ends the task.
		yield(StepCompleteEvent{
			Contents: []ContentPart{
				&TextPart{Text: "done", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
			},
			Usage:      Usage{OutputTokens: 5},
			StopReason: "end_turn",
		}, nil)
	}, nil
}

func (m *toolOnlyTimedProvider) SetReasoningLevel(_ int)                       {}
func (m *toolOnlyTimedProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *toolOnlyTimedProvider) SetVideoConfig(_ int, _ int)                   {}
