package llm

import (
	"context"
	"encoding/json"
	"strings"
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

// A model can name a tool that does not exist — a typo, a tool from another
// model's config, an MCP server that has not finished initializing. The call has
// to come back with an error the model can read, and it must be treated as an
// answered call: the model asked, alayacore replies "no such tool". Returning it
// as unanswered would drop the pair, and a step that recorded a tool_use with no
// tool_result is the conversation the next request cannot build (gotcha 3).
func TestExecuteToolUnknownToolAnswersTheCall(t *testing.T) {
	a := NewAgent(AgentConfig{
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "known_tool"},
			Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
				return []ContentPart{&TextPart{Text: "ok"}}, nil
			},
		}},
	})

	var sawError bool
	callbacks := StreamCallbacks{
		OnToolOutput: func(_ string, _ []ContentPart, err error, _ uint64) error {
			sawError = err != nil
			return nil
		},
	}
	call := &ToolInputPart{ID: "c1", Name: "no_such_tool"}

	part := a.executeTool(context.Background(), call, callbacks, 7)
	top, ok := part.(*ToolOutputPart)
	if !ok {
		t.Fatalf("got %T, want *ToolOutputPart", part)
	}
	if !top.IsError {
		t.Error("an unknown tool produced a success result")
	}
	if !sawError {
		t.Error("OnToolOutput was not told about the failure")
	}
	text := ""
	if len(top.Output) > 0 {
		if tp, isText := top.Output[0].(*TextPart); isText {
			text = tp.Text
		}
	}
	if !strings.Contains(text, "no_such_tool") {
		t.Errorf("the result does not name the missing tool, so the model cannot correct it: %q", text)
	}

	// And through the shared lifecycle, which is what decides history.
	result, ran := a.runToolCall(context.Background(), call, 7, StreamCallbacks{})
	if !ran {
		t.Error("runToolCall reported the call as never-executed; its result would be dropped from history")
	}
	if out, isResult := result.(*ToolOutputPart); !isResult || !out.IsError {
		t.Errorf("runToolCall returned %v, want an error result", result)
	}
}
