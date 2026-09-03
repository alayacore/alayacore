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

// One step's record used to be assembled twice by code that never called each
// other: the provider's accumulators (getContents, delivered inside
// StepCompleteEvent) on the path that finished, and llm.Agent's own copy
// (stepTextBlocks, salvageExecutedTools) on the path that was cut. That design is
// gone — llm.Agent's assembler now serves every path — but this test stays,
// because it asserts the property the refactor exists to buy rather than its
// mechanism: the same stream, run finished and run cut, must land the same
// history — same parts, same order, same IDs — and must run the tool both times.
//
// The tool half is not decoration. It is where the old design measurably failed:
// when the missing-terminator rule first landed as an early return, a call whose
// arguments had fully streamed was neither executed nor recorded (tool ran=0,
// history empty) while the adapter had already drawn it from its deltas — the same
// shape as the ordering bug that opened this whole line of work, reached from the
// other side. Records matching is the contract; the error is what makes the
// difference visible.
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

// OpenAI's delta schema has no per-field terminator, so a cut stream is the
// normal way to reach the record without closures — and a server is free to put
// `content` before `reasoning_content` in its chunks. The record must still come
// out in the shape an assistant turn has in this protocol (reasoning, content,
// tool calls), because that array is what gets replayed and what a saved session
// re-lays on reopen.
func TestOpenAICutStepRecordsAssistantTurnShape(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		// content streamed first, reasoning second; no finish_reason, no [DONE]
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ANSWER\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"THINK\"}}]}\n\n")
	})
	provider, err := providers.NewOpenAI(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, ReasoningField: "reasoning_content"})
	if err != nil {
		t.Fatal(err)
	}

	record, err := recordOfStep(t, provider)
	if err == nil {
		t.Fatal("a body with no terminating signal must report an error")
	}
	var kinds []string
	for _, p := range record {
		switch p.(type) {
		case *llm.ReasoningPart:
			kinds = append(kinds, "reasoning")
		case *llm.TextPart:
			kinds = append(kinds, "text")
		}
	}
	if got := strings.Join(kinds, ","); got != "reasoning,text" {
		t.Errorf("record = [%s], want reasoning,text: the provider declares this turn's shape "+
			"with the content, so a cut stream cannot scramble the file", got)
	}
}

// The same rule for OpenAI, whose protocol gives it nothing to forward: the
// positions are constants derived from the assistant-turn shape, so a new event
// site (a fourth block kind, a new delta field) is the realistic way this breaks.
func TestOpenAIDeclaresPositionOnEveryContentEvent(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"THINK\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ANSWER\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"noop\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	})
	provider, err := providers.NewOpenAI(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, ReasoningField: "reasoning_content"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.StreamMessages(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	positions := map[string]int{}
	checked := 0
	for event := range events {
		var pos int
		var key string
		switch e := event.(type) {
		case llm.ReasoningDeltaEvent:
			pos, key = e.Position, e.Key
		case llm.TextDeltaEvent:
			pos, key = e.Position, e.Key
		case llm.ToolInputDeltaEvent:
			pos, key = e.Position, e.Key
		case llm.ToolInputStartEvent:
			pos, key = e.Position, e.Key
		case llm.ReasoningCompleteEvent:
			pos, key = e.Position, e.Key
		case llm.TextCompleteEvent:
			pos, key = e.Position, e.Key
		case llm.ToolInputCompleteEvent:
			pos, key = e.Position, e.Key
		default:
			continue
		}
		checked++
		if pos == 0 {
			t.Errorf("%T for %q carries no record position", event, key)
		}
		if seen, dup := positions[key]; dup && seen != pos {
			t.Errorf("block %q declared two positions: %d and %d", key, seen, pos)
		}
		positions[key] = pos
	}
	if checked < 7 {
		t.Fatalf("only %d content events seen; the body no longer exercises this path", checked)
	}
	if positions["reasoning"] >= positions["text"] || positions["text"] >= positions["tool:0"] {
		t.Errorf("declared layout = %v, want reasoning < text < tool", positions)
	}
}
