package llm

import (
	"context"
	"encoding/json"
	"testing"
)

// TestExecuteToolStreamingVariant verifies that a tool with an
// ExecuteStreaming implementation is dispatched to it, that onDelta
// preview snapshots are forwarded to OnToolOutputDelta, and that the
// authoritative result comes from the returned []ContentPart.
func TestExecuteToolStreamingVariant(t *testing.T) {
	var gotDeltas []string
	a := NewAgent(AgentConfig{
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "stream_tool"},
			ExecuteStreaming: func(_ context.Context, _ json.RawMessage, onDelta func(string)) ([]ContentPart, error) {
				onDelta("preview1")
				onDelta("preview2")
				return []ContentPart{&TextPart{Text: "final"}}, nil
			},
		}},
	})

	callbacks := StreamCallbacks{
		OnToolOutputDelta: func(toolCallID, text string, historyID uint64) error {
			if toolCallID != "c1" {
				t.Errorf("toolCallID = %q, want %q", toolCallID, "c1")
			}
			if historyID != 7 {
				t.Errorf("historyID = %d, want 7", historyID)
			}
			gotDeltas = append(gotDeltas, text)
			return nil
		},
		OnToolOutput: func(toolCallID string, contents []ContentPart, err error, historyID uint64) error {
			return nil
		},
	}

	part := a.executeTool(context.Background(), &ToolInputPart{ID: "c1", Name: "stream_tool"}, callbacks, 7)

	top, ok := part.(*ToolOutputPart)
	if !ok {
		t.Fatalf("expected *ToolOutputPart, got %T", part)
	}
	if top.IsError {
		t.Error("expected success")
	}
	tp, ok := top.Output[0].(*TextPart)
	if !ok || tp.Text != "final" {
		t.Errorf("authoritative output = %v, want TextPart(\"final\")", top.Output)
	}

	if len(gotDeltas) != 2 || gotDeltas[0] != "preview1" || gotDeltas[1] != "preview2" {
		t.Errorf("deltas = %v, want [preview1 preview2]", gotDeltas)
	}
}

// TestExecuteToolFallsBackToExecute verifies that tools without
// ExecuteStreaming keep using the plain Execute path and never fire
// OnToolOutputDelta.
func TestExecuteToolFallsBackToExecute(t *testing.T) {
	deltaCalls := 0
	a := NewAgent(AgentConfig{
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "plain_tool"},
			Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
				return []ContentPart{&TextPart{Text: "ok"}}, nil
			},
		}},
	})

	callbacks := StreamCallbacks{
		OnToolOutputDelta: func(_, _ string, _ uint64) error {
			deltaCalls++
			return nil
		},
		OnToolOutput: func(toolCallID string, contents []ContentPart, err error, historyID uint64) error {
			return nil
		},
	}

	part := a.executeTool(context.Background(), &ToolInputPart{ID: "c1", Name: "plain_tool"}, callbacks, 1)
	if top, ok := part.(*ToolOutputPart); !ok || top.IsError {
		t.Fatalf("expected successful ToolOutputPart, got %v", part)
	}
	if deltaCalls != 0 {
		t.Errorf("OnToolOutputDelta called %d times, want 0", deltaCalls)
	}
}
