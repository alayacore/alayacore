package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// A server may stream tool_call chunks out of index order — index 1 before
// index 0. The protocol's index is the model's declared order for parallel
// calls, and it is what the request must be rebuilt in (openaiConvertToolInputs
// emits tool_calls in array order, and openaiConvertToolOutputs emits one
// role:"tool" message per input in array order), so the array keeps index order
// regardless of arrival: toolIndices() sorts, and reorderToolResults()
// sequences results by input order.
//
// Block keys are "tool:<index>", i.e. taken from the same protocol value, so
// identity and order agree by construction. Do not switch either to
// first-appearance order: that would number and lay out calls by streaming
// luck instead of by what the model asked for.
func TestOpenAIKeepsToolOrderFromProtocolIndexNotArrival(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"working\"}}]}\n\n")
		// index 1 is streamed first on purpose.
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_second\",\"function\":{\"name\":\"noop\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_first\",\"function\":{\"name\":\"noop\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	provider, err := providers.NewOpenAI(providers.BaseConfig{APIKey: "test", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	noop := llm.Tool{
		Definition: llm.ToolDefinition{Name: "noop"},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			return []llm.ContentPart{&llm.TextPart{Text: "ok"}}, nil
		},
	}

	next := uint64(100)
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, Tools: []llm.Tool{noop}, MaxSteps: 1})
	_, err = agent.Stream(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}),
		llm.StreamCallbacks{
			IDGen:               func() uint64 { n := next; next++; return n },
			OnTextComplete:      func(string, uint64) error { return nil },
			OnToolInputStart:    func(_, _ string, _ uint64) error { return nil },
			OnToolInputComplete: func(string, json.RawMessage, uint64) error { return nil },
			OnToolOutput:        func(string, []llm.ContentPart, error, uint64) error { return nil },
			OnStepFinish: func(contents []llm.ContentPart, _ llm.Usage) error {
				var inputs, outputs []string
				seenID := map[uint64]bool{}
				checkID := func(p llm.ContentPart, label string) {
					t.Helper()
					id := p.GetHistoryID()
					if id == 0 {
						t.Errorf("%s persisted with no history ID", label)
					}
					if seenID[id] {
						t.Errorf("duplicate history ID %d on %s", id, label)
					}
					seenID[id] = true
				}
				for _, p := range contents {
					switch v := p.(type) {
					case *llm.ToolInputPart:
						inputs = append(inputs, v.ID)
						checkID(p, "tool call "+v.ID)
					case *llm.ToolOutputPart:
						outputs = append(outputs, v.ID)
						checkID(p, "tool result "+v.ID)
					}
				}
				want := []string{"call_first", "call_second"} // protocol index order
				if len(inputs) != 2 || inputs[0] != want[0] || inputs[1] != want[1] {
					t.Errorf("tool call order = %v, want %v (protocol index, not arrival)", inputs, want)
				}
				if len(outputs) != 2 || outputs[0] != want[0] || outputs[1] != want[1] {
					t.Errorf("tool result order = %v, want %v (results follow call order)", outputs, want)
				}
				return fmt.Errorf("stop")
			},
		})
	if err == nil {
		t.Fatal("probe expected the loop to stop via OnStepFinish")
	}
}
