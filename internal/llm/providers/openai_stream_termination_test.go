package providers_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// openaiChunks builds an SSE body for one assistant turn: a reasoning delta and
// a content delta. finishReason adds the last chunk carrying it; done adds the
// [DONE] sentinel. Neither argument set leaves a body that simply stops.
func openaiChunks(finishReason bool, done bool) func(io.Writer) {
	return func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"a long reasoning\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		if finishReason {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		}
		if done {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
}

// streamOpenAI runs one agent step against a body and reports what reached
// history alongside the error the caller saw.
func streamOpenAI(t *testing.T, body func(io.Writer)) ([]llm.ContentPart, error) {
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
	next := uint64(1)
	var published []llm.ContentPart
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 1})
	_, streamErr := agent.Stream(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}),
		llm.StreamCallbacks{
			IDGen:               func() uint64 { n := next; next++; return n },
			OnReasoningDelta:    func(string, uint64) error { return nil },
			OnReasoningComplete: func(string, uint64) error { return nil },
			OnTextDelta:         func(string, uint64) error { return nil },
			OnTextComplete:      func(string, uint64) error { return nil },
			OnStepFinish: func(c []llm.ContentPart, _ llm.Usage) error {
				published = append([]llm.ContentPart{}, c...)
				return nil
			},
		})
	return published, streamErr
}

const wantOpenAIHistory = "reasoning:a long reasoning,text:Answer."

// The three ways a body can end. Only the third is not a turn the server
// finished: with no `finish_reason` and no `[DONE]`, nothing ever said the
// response was over, and rebuilding the step from the accumulators anyway
// presented a cut-off sentence as a concluded answer.
//
// Both tolerances are deliberate and were measured against this repo's own
// suite: demanding `finish_reason` specifically fails 12 tests whose endpoints
// close the stream with `[DONE]` instead, and demanding `[DONE]` on top of a
// `finish_reason` fails endpoints that end the body after the final chunk. Each
// of those is a server that *did* terminate, just by the other signal.
func TestOpenAIStreamTerminationSignals(t *testing.T) {
	tests := []struct {
		name         string
		finishReason bool
		done         bool
		wantErr      bool
	}{
		{name: "both signals", finishReason: true, done: true},
		{name: "finish_reason closes the body", finishReason: true, done: false},
		{name: "DONE without a reason", finishReason: false, done: true},
		{name: "no terminal signal at all", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			published, err := streamOpenAI(t, openaiChunks(tc.finishReason, tc.done))

			if tc.wantErr {
				if err == nil {
					t.Fatal("a body that sent neither finish_reason nor [DONE] must not report success")
				}
				if !strings.Contains(err.Error(), "terminating signal") {
					t.Errorf("error should name the missing terminal signal, got: %v", err)
				}
				// The error must cost nothing: what streamed is still history,
				// marked by the absence of a terminator rather than erased.
				if got := strings.Join(describe(published), ","); got != wantOpenAIHistory {
					t.Errorf("history = [%s], want [%s]", got, wantOpenAIHistory)
				}
				return
			}

			if err != nil {
				t.Fatalf("terminated stream must not error, got: %v", err)
			}
			if got := strings.Join(describe(published), ","); got != wantOpenAIHistory {
				t.Errorf("history = [%s], want [%s]", got, wantOpenAIHistory)
			}
		})
	}
}
