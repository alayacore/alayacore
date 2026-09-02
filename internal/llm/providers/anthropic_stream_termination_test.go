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

// anthropicEvents builds an SSE body for one assistant message: a thinking
// block and a text block, both properly closed. withStop adds the terminal
// message_delta/message_stop pair.
func anthropicEvents(withStop bool) func(io.Writer) {
	return func(w io.Writer) {
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")

		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"a long reasoning\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")

		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Answer.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")

		if withStop {
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n")
			fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\",\"usage\":{\"input_tokens\":10,\"output_tokens\":15}}\n\n")
		}
	}
}

func streamAnthropic(t *testing.T, body func(io.Writer)) ([]llm.ContentPart, error) {
	t.Helper()
	server := newMockSSEServer(t, body)
	provider, err := providers.NewAnthropic(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, Model: "claude-test"})
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

// mustStreamAnthropic streams a complete message and returns its record, failing
// the test if the step did not run clean.
func mustStreamAnthropic(t *testing.T, withStop bool) []llm.ContentPart {
	t.Helper()
	parts, err := streamAnthropic(t, anthropicEvents(withStop))
	if err != nil {
		t.Fatalf("terminated stream failed: %v", err)
	}
	return parts
}

func describe(parts []llm.ContentPart) []string {
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case *llm.ReasoningPart:
			out = append(out, "reasoning:"+v.Text)
		case *llm.TextPart:
			if p.GetRole() == llm.RoleAssistant {
				out = append(out, "text:"+v.Text)
			}
		}
	}
	return out
}

// message_stop is the only event that ends an Anthropic message and the only
// place StepCompleteEvent is emitted. Without it the old code returned no
// events and no error: the turn was reported successful while the reasoning and
// text streamed to the display never reached history, so they vanished on
// save-and-reopen. The premature end is now an error, and llm.Agent's
// failed-step path still contributes the streamed blocks.
func TestAnthropicStreamWithoutMessageStopIsAnError(t *testing.T) {
	_, err := streamAnthropic(t, anthropicEvents(false))
	if err == nil {
		t.Fatal("a stream that never sent message_stop must not report success")
	}
	if !strings.Contains(err.Error(), "message_stop") {
		t.Errorf("error should name the missing terminal event, got: %v", err)
	}
}

func TestAnthropicStreamWithoutMessageStopKeepsStreamedContent(t *testing.T) {
	published, err := streamAnthropic(t, anthropicEvents(false))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	got := strings.Join(describe(published), ",")
	want := "reasoning:a long reasoning,text:Answer."
	if got != want {
		t.Errorf("history = [%s], want [%s] — content streamed to the display must survive the error", got, want)
	}
}

// The normal path must be untouched: message_stop present, no error, contents
// from the authoritative step.
func TestAnthropicStreamWithMessageStopUnaffected(t *testing.T) {
	published, err := streamAnthropic(t, anthropicEvents(true))
	if err != nil {
		t.Fatalf("complete stream must not error, got: %v", err)
	}
	got := strings.Join(describe(published), ",")
	want := "reasoning:a long reasoning,text:Answer."
	if got != want {
		t.Errorf("history = [%s], want [%s]", got, want)
	}
}

// The point of one assembler is that a step cannot be described two ways. Run
// the same message once whole and once cut off before message_stop, and require
// the histories to match part for part, ID for ID: the cut turn keeps its content
// and is simply known, by the error, to be unfinished.
//
// This is the property the previous design hoped for and could not enforce, since
// each path assembled the record with its own code.
func TestAnthropicCutStepMatchesFinishedStep(t *testing.T) {
	summarize := func(parts []llm.ContentPart) []string {
		var out []string
		for _, p := range parts {
			if p.GetRole() == llm.RoleUser {
				continue // the echoed prompt, identical on both paths
			}
			switch v := p.(type) {
			case *llm.ReasoningPart:
				out = append(out, fmt.Sprintf("reasoning(%q,id=%d)", v.Text, p.GetHistoryID()))
			case *llm.TextPart:
				out = append(out, fmt.Sprintf("text(%q,id=%d)", v.Text, p.GetHistoryID()))
			}
		}
		return out
	}

	whole := summarize(mustStreamAnthropic(t, true))
	cut, err := streamAnthropic(t, anthropicEvents(false))
	if err == nil {
		t.Fatal("a stream without message_stop must report an error")
	}
	cutSum := summarize(cut)

	if strings.Join(cutSum, " ") != strings.Join(whole, " ") {
		t.Errorf("cut step recorded [%s], finished step recorded [%s]",
			strings.Join(cutSum, " "), strings.Join(whole, " "))
	}
	if len(whole) != 2 {
		t.Fatalf("expected 2 parts in the finished record, got %d: %v", len(whole), whole)
	}
}
