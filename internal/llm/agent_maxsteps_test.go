package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"math"
	"testing"
)

// mockProviderAlwaysToolCalls always returns tool calls, never a final text response.
// This simulates an agent that never converges to a final answer.
type mockProviderAlwaysToolCalls struct {
	callCount int
}

func (m *mockProviderAlwaysToolCalls) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	m.callCount++
	return func(yield func(StreamEvent, error) bool) {
		// Always emit a tool call, never a text-only response. The record is
		// assembled by llm.Agent from these events, so a stub provider states
		// only what the wire carried: identity, arguments, where they ended.
		yield(ToolInputStartEvent{ID: "call_1", Name: "repeat", Key: "block:0"}, nil)
		yield(ToolInputDeltaEvent{ID: "call_1", Delta: "{}", Key: "block:0"}, nil)
		yield(ToolInputCompleteEvent{ID: "call_1", Key: "block:0"}, nil)
		yield(StepCompleteEvent{Usage: Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}, nil
}

func (m *mockProviderAlwaysToolCalls) SetReasoningLevel(_ int)                       {}
func (m *mockProviderAlwaysToolCalls) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *mockProviderAlwaysToolCalls) SetVideoConfig(_ int, _ int)                   {}

func TestAgentMaxStepsExceeded(t *testing.T) {
	provider := &mockProviderAlwaysToolCalls{}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "repeat", Description: "Repeat", Schema: []byte(`{"type":"object"}`)},
				Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
					return []ContentPart{&TextPart{Text: "repeated"}}, nil
				},
			},
		},
		MaxSteps: 3,
	})

	result, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult even when max steps exceeded")
	}

	if provider.callCount != 3 {
		t.Fatalf("expected 3 provider calls (max steps), got %d", provider.callCount)
	}

	t.Logf("PASS: Agent correctly returns ErrMaxStepsExceeded after %d steps", provider.callCount)
}

func TestAgentCompletesWithinMaxSteps(t *testing.T) {
	// Provider returns tool call on first call, then text-only on second call
	provider := &mockProviderWithTextAndTools{
		responses: []mockResponse{
			{
				toolCalls: []ToolInputPart{{ID: "call_1", Name: "ping", Input: []byte(`{}`)}},
			},
			{
				text: "Done!",
			},
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "ping", Description: "Ping", Schema: []byte(`{"type":"object"}`)},
				Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
					return []ContentPart{&TextPart{Text: "pong"}}, nil
				},
			},
		},
		MaxSteps: 3,
	})

	result, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if err != nil {
		t.Fatalf("expected no error when agent completes within max steps, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	t.Log("PASS: Agent completes normally within max steps without error")
}

// mockProviderTruncated returns a text response with a truncation stop reason.
type mockProviderTruncated struct {
	stopReason string
}

func (m *mockProviderTruncated) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "Partial response...", Key: "block:0"}, nil)
		yield(TextCompleteEvent{Key: "block:0"}, nil)
		yield(StepCompleteEvent{
			Usage:      Usage{InputTokens: 10, OutputTokens: 5},
			StopReason: m.stopReason,
		}, nil)
	}, nil
}

func (m *mockProviderTruncated) SetReasoningLevel(_ int)                       {}
func (m *mockProviderTruncated) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *mockProviderTruncated) SetVideoConfig(_ int, _ int)                   {}

func TestAgentTruncatedMaxTokens(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "max_tokens"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "Write a novel", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("expected ErrResponseTruncated, got: %v", err)
	}

	if result == nil || len(result.Contents) == 0 {
		t.Fatalf("expected non-nil StreamResult with partial content, got: %+v", result)
	}

	t.Log("PASS: Agent returns ErrResponseTruncated for max_tokens with partial messages")
}

func TestAgentTruncatedLength(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "length"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "Write a novel", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("expected ErrResponseTruncated, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult even when truncated")
	}

	t.Log("PASS: Agent returns ErrResponseTruncated for length with partial messages")
}

func TestAgentNoTruncationOnEndTurn(t *testing.T) {
	provider := &mockProviderTruncated{stopReason: "end_turn"}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		MaxSteps: 5,
	})

	result, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "Hello", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if err != nil {
		t.Fatalf("expected no error for end_turn, got: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	t.Log("PASS: Agent does not return ErrResponseTruncated for end_turn")
}

// TestAgentNonPositiveMaxStepsMeansNoLimit covers the gap between the CLI
// (which now rejects a negative --max-steps at startup) and the agent itself:
// NewAgent normalized only 0 to "unlimited", so a negative bound — reachable
// through a hand-edited session file's max_steps — left the loop body
// unreachable. The agent then did no work at all and still returned
// ErrMaxStepsExceeded, reporting a bad setting as a runaway model.
func TestAgentNonPositiveMaxStepsMeansNoLimit(t *testing.T) {
	for _, maxSteps := range []int{0, -1, -100, math.MinInt} {
		agent := NewAgent(AgentConfig{
			Provider: &mockProviderAlwaysToolCalls{},
			MaxSteps: maxSteps,
		})

		if agent.config.MaxSteps != math.MaxInt {
			t.Errorf("MaxSteps=%d: effective limit = %d, want math.MaxInt (no limit)",
				maxSteps, agent.config.MaxSteps)
		}
	}
}

// A positive bound is still honored exactly — the guard above must not turn
// every invalid value into "unlimited" in a way that breaks the real feature.
func TestAgentPositiveMaxStepsIsHonoured(t *testing.T) {
	provider := &mockProviderAlwaysToolCalls{}
	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "repeat", Schema: json.RawMessage(`{"type":"object"}`)},
			Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
				return []ContentPart{&TextPart{Text: "repeated"}}, nil
			},
		}},
		MaxSteps: 2,
	})

	_, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "Hello", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})

	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("Stream() error = %v, want ErrMaxStepsExceeded", err)
	}
	if provider.callCount != 2 {
		t.Errorf("provider called %d times, want exactly the 2-step limit", provider.callCount)
	}
}
