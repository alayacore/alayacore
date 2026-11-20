package llm

// What a panic inside a tool means.
//
// A tool call is the one part of a step that runs on a goroutine of its own (in
// the default mode), so the recover in streamEvents — which exists precisely so a
// bug cannot take the content the user watched — cannot see it. These tests pin
// the answer: the step ends and reports it, the process and the conversation
// survive, and the history keeps what the step had already recorded.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// panickingProvider asks for one call of "boom" after saying something, so the
// step has both text to salvage and a call that never gets a result.
func panickingProvider() *mockProviderWithTextAndTools {
	return &mockProviderWithTextAndTools{responses: []mockResponse{
		{text: "thinking out loud", toolCalls: []ToolInputPart{
			{ID: "call_1", Name: "boom", Input: []byte("{}")},
		}},
		{text: "never reached"},
	}}
}

func boomTool() []Tool {
	return []Tool{{
		Definition: ToolDefinition{Name: "boom", Schema: json.RawMessage(`{"type":"object","properties":{}}`)},
		Execute: func(context.Context, json.RawMessage) ([]ContentPart, error) {
			panic("kaboom")
		},
	}}
}

// The bug this covers: the panic used to reach the runtime from a tool
// goroutine, and the runtime answers that by ending the process — taking the
// conversation with it, which is the one thing the agent's own recover refuses to
// allow.
func TestToolPanicEndsTheStepNotTheProcess(t *testing.T) {
	for _, mode := range []struct {
		name   string
		serial bool
	}{
		{"parallel (default)", false},
		{"serial", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			var finished []ContentPart
			agent := NewAgent(AgentConfig{
				Provider:        panickingProvider(),
				Tools:           boomTool(),
				SerialToolCalls: mode.serial,
				MaxSteps:        3,
			})

			_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
				OnStepFinish: func(contents []ContentPart, _ Usage) error {
					finished = contents
					return nil
				},
			})

			if err == nil {
				t.Fatal("Stream error = nil, want the panic reported as the step's failure")
			}
			// The name and the value are what a reader needs to find the bug; the
			// stack is there because recovering is what hides it.
			for _, want := range []string{`panic in tool "boom"`, "kaboom", "goroutine"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}

			// The step's other content is not the panic's to take: the text that
			// streamed before the call is in the history the step finished on.
			if countText(finished, "thinking out loud") != 1 {
				t.Errorf("finished history = %v, want the streamed text kept", finished)
			}
			// The call it answered never produced a result, so it must not be
			// replayed as an assistant tool_use without one.
			for _, p := range finished {
				if tip, ok := p.(*ToolInputPart); ok {
					t.Errorf("finished history holds tool call %q with no result", tip.ID)
				}
			}
		})
	}
}

// The panic text is the report, not a tool result: a result would be re-sent to
// the provider on the next request, carrying a stack into the conversation.
func TestToolPanicNeverBecomesAToolResult(t *testing.T) {
	agent := NewAgent(AgentConfig{
		Provider: panickingProvider(),
		Tools:    boomTool(),
		MaxSteps: 3,
	})

	var (
		outputs int
		race    string
	)
	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnToolOutput: func(_ string, _ []ContentPart, err error, _ uint64) error {
			outputs++
			if err != nil {
				race = err.Error()
			}
			return nil
		},
	})
	if err == nil {
		t.Fatal("Stream error = nil, want the panic reported")
	}
	if outputs != 0 {
		t.Errorf("OnToolOutput ran %d times, want 0: a panic produces no result", outputs)
	}
	if race != "" {
		t.Errorf("a tool result carried the panic: %q", race)
	}
}
