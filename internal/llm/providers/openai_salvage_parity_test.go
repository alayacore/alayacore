package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// openaiStepBody streams one assistant turn — reasoning, text, and a complete
// tool call — and either terminates it or stops dead after the last delta.
func openaiStepBody(terminate, withTool bool) func(io.Writer) {
	return func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"a long reasoning\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		if withTool {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"noop\",\"arguments\":\"{}\"}}]}}]}\n\n")
		}
		if terminate {
			reason := "stop"
			if withTool {
				reason = "tool_calls"
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", reason)
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
}

// runOpenAIStep streams a body through a real agent step and reports what
// reached history, formatted so that a difference between two runs is legible
// straight out of the failure message.
func runOpenAIStep(t *testing.T, body func(io.Writer)) (record []string, ranTool int32, err error) {
	t.Helper()
	server := newMockSSEServer(t, body)
	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:         "k",
		BaseURL:        server.URL,
		ReasoningField: "reasoning_content",
	})
	if err != nil {
		t.Fatal(err)
	}

	noop := llm.Tool{
		Definition: llm.ToolDefinition{Name: "noop"},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			atomic.AddInt32(&ranTool, 1)
			return []llm.ContentPart{&llm.TextPart{Text: "did it", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}}}, nil
		},
	}

	next := uint64(1)
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, Tools: []llm.Tool{noop}, MaxSteps: 1})
	_, streamErr := agent.Stream(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}),
		llm.StreamCallbacks{
			IDGen:               func() uint64 { n := next; next++; return n },
			OnReasoningDelta:    func(string, uint64) error { return nil },
			OnReasoningComplete: func(string, uint64) error { return nil },
			OnTextDelta:         func(string, uint64) error { return nil },
			OnTextComplete:      func(string, uint64) error { return nil },
			OnToolInputStart:    func(_, _ string, _ uint64) error { return nil },
			OnToolInputDelta:    func(_, _ string, _ uint64) error { return nil },
			OnToolInputComplete: func(string, json.RawMessage, uint64) error { return nil },
			OnStepFinish: func(contents []llm.ContentPart, _ llm.Usage) error {
				for _, p := range contents {
					if p.GetRole() == llm.RoleUser {
						continue // the echoed prompt, identical on both paths
					}
					switch v := p.(type) {
					case *llm.ReasoningPart:
						record = append(record, fmt.Sprintf("reasoning(%q,id=%d)", v.Text, p.GetHistoryID()))
					case *llm.TextPart:
						record = append(record, fmt.Sprintf("text(%q,id=%d)", v.Text, p.GetHistoryID()))
					case *llm.ToolInputPart:
						record = append(record, fmt.Sprintf("call(%s,id=%d)", v.ID, p.GetHistoryID()))
					case *llm.ToolOutputPart:
						record = append(record, fmt.Sprintf("result(%s,id=%d)", v.ID, p.GetHistoryID()))
					}
				}
				return nil
			},
		})
	return record, atomic.LoadInt32(&ranTool), streamErr
}

// One step's record is assembled twice by two pieces of code that never call
// each other: the provider's accumulators (getContents, delivered inside
// StepCompleteEvent) on the path that finishes, and llm.Agent's own copy
// (stepTextBlocks, salvageExecutedTools) on the path that is cut. Neither can
// guarantee on its own that the two agree, so the agreement is pinned here
// rather than left as a convention: the same stream, run twice, must land the
// same history — same parts, same order, same IDs — and must run the tool in
// both cases.
//
// The tool half is not decoration. When the missing-terminator rule first landed
// it was implemented as an early return, which silently meant that a call whose
// arguments had fully streamed was never executed and never recorded, while the
// adapter had already drawn it from its deltas — the same shape as the ordering
// bug that opened this whole line of work. Records matching is the fix's real
// contract; the error is what makes the difference visible.
func TestCutStepRecordsWhatAFinishedStepWouldHave(t *testing.T) {
	tests := []struct {
		name     string
		withTool bool
	}{
		{name: "reasoning and text"},
		{name: "with a tool call", withTool: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			whole, wholeRan, err := runOpenAIStep(t, openaiStepBody(true, tc.withTool))
			if err != nil && !strings.Contains(err.Error(), "maximum steps") {
				t.Fatalf("terminated stream failed: %v", err)
			}
			cut, cutRan, err := runOpenAIStep(t, openaiStepBody(false, tc.withTool))
			if err == nil {
				t.Fatal("a body with no terminating signal must not report success")
			}

			got, want := strings.Join(cut, " "), strings.Join(whole, " ")
			if got != want {
				t.Errorf("cut step recorded\n  [%s]\nbut the same stream ending cleanly recorded\n  [%s]", got, want)
			}
			if cutRan != wholeRan {
				t.Errorf("tool ran %d times on the cut step, %d on the terminated one", cutRan, wholeRan)
			}
			if tc.withTool && cutRan != 1 {
				t.Error("the tool call never executed on the cut path")
			}
		})
	}
}
