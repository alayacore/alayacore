package llm

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

// A provider (or a proxy) that repeats a completion boundary for one call must
// not cause the tool to run twice. beginToolCall reports the repeat via its
// second result, and streamEvents starts the call only for the first boundary;
// the guard lives in streamEvents, so it covers both drivers.
//
// The bug this pins: beginToolCall returns the *existing* part on a repeat
// (nil would break the record), so a caller that only checks for nil — which is
// exactly what the code did — ran the tool a second time, with real side
// effects.
func TestDuplicateToolBoundaryRunsToolOnce(t *testing.T) {
	for _, serial := range []bool{false, true} {
		name := "parallel"
		if serial {
			name = "serial"
		}
		t.Run(name, func(t *testing.T) {
			var runs int32
			provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
				yield(ToolInputStartEvent{ID: "c1", Name: "t", Key: "block:0"}, nil)
				yield(ToolInputDeltaEvent{ID: "c1", Delta: "{}", Key: "block:0"}, nil)
				yield(ToolInputCompleteEvent{ID: "c1", Key: "block:0"}, nil)
				// The same call's boundary, repeated.
				yield(ToolInputCompleteEvent{ID: "c1", Key: "block:0"}, nil)
				yield(StepCompleteEvent{Usage: Usage{OutputTokens: 1}, StopReason: "tool_use"}, nil)
			}}

			agent := NewAgent(AgentConfig{
				Provider: provider,
				Tools: []Tool{{
					Definition: ToolDefinition{Name: "t", Schema: json.RawMessage(`{"type":"object"}`)},
					Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
						atomic.AddInt32(&runs, 1)
						return []ContentPart{&TextPart{Text: "ok"}}, nil
					},
				}},
				MaxSteps:        1,
				SerialToolCalls: serial,
			})

			_, _ = agent.Stream(context.Background(), nil, StreamCallbacks{})
			if got := atomic.LoadInt32(&runs); got != 1 {
				t.Errorf("tool executed %d times, want 1: a repeated boundary must not run it again", got)
			}
		})
	}
}
