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

// anthropicOrderedBody streams thinking in block 0 and text in block 1, closing
// them in the order asked for. stopFirst=false delivers content_block_stop(1)
// before content_block_stop(0) — legal for a server to emit, since the index is
// declared up front and the stops merely carry it.
func anthropicOrderedBody(inOrder bool, closeZero bool) func(io.Writer) {
	return func(w io.Writer) {
		ev := func(f string, args ...any) { fmt.Fprintf(w, f, args...) }
		ev("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		ev("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		ev("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"THINK\"}}\n\n")
		ev("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		ev("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"ANSWER\"}}\n\n")
		stop := func(i int) {
			ev("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i)
		}
		if inOrder {
			if closeZero {
				stop(0)
			}
			stop(1)
		} else {
			stop(1)
			if closeZero {
				stop(0)
			}
		}
		ev("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
		ev("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
}

func newAnthropicOrdered(t *testing.T, body func(io.Writer)) *providers.AnthropicProvider {
	t.Helper()
	server := newMockSSEServer(t, body)
	provider, err := providers.NewAnthropic(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// boundaryOrder lists the block boundaries in the order they reach llm.Agent.
func boundaryOrder(t *testing.T, provider *providers.AnthropicProvider) []string {
	t.Helper()
	events, err := provider.StreamMessages(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for event := range events {
		switch e := event.(type) {
		case llm.ReasoningCompleteEvent:
			out = append(out, "reasoning:"+e.Key)
		case llm.TextCompleteEvent:
			out = append(out, "text:"+e.Key)
		case llm.ToolInputCompleteEvent:
			out = append(out, "call:"+e.Key)
		}
	}
	return out
}

// A boundary event is two things at once: it finalizes a display window and it
// fixes where that block lands in the persisted record. Both must follow the
// index the server declared, not the order its stop events happened to arrive
// in, so a server closing block 1 first cannot put the answer above the thinking
// that produced it — in the session file, in the live TUI, or under --plainio.
func TestAnthropicDeliversBoundariesInDeclaredIndexOrder(t *testing.T) {
	provider := newAnthropicOrdered(t, anthropicOrderedBody(false, true))

	got := strings.Join(boundaryOrder(t, provider), " ")
	want := "reasoning:block:0 text:block:1"
	if got != want {
		t.Errorf("boundaries arrived as [%s], want [%s] — the record and the display are laid out in this order", got, want)
	}

	var kinds []string
	for _, p := range stepRecord(t, provider) {
		switch p.(type) {
		case *llm.ReasoningPart:
			kinds = append(kinds, "reasoning")
		case *llm.TextPart:
			kinds = append(kinds, "text")
		}
	}
	if strings.Join(kinds, ",") != "reasoning,text" {
		t.Errorf("record = [%s], want reasoning before text", strings.Join(kinds, ","))
	}
}

// The ordinary case must be untouched: blocks closing as they are written are
// delivered as they close, with nothing held back to the end of the message.
func TestAnthropicInOrderStopsDeliveredImmediately(t *testing.T) {
	provider := newAnthropicOrdered(t, anthropicOrderedBody(true, true))

	got := strings.Join(boundaryOrder(t, provider), " ")
	if got != "reasoning:block:0 text:block:1" {
		t.Errorf("boundaries = [%s]", got)
	}
	if len(stepRecord(t, provider)) != 2 {
		t.Errorf("record lost a block")
	}
}

// A block that never closes must not strand the blocks behind it. The closure of
// block 1 waits briefly on block 0, and message_stop releases it: holding the
// order is a delivery rule, not a bet that every server finishes what it started.
func TestAnthropicStrandedBoundaryReleasedAtMessageStop(t *testing.T) {
	provider := newAnthropicOrdered(t, anthropicOrderedBody(true, false))

	got := strings.Join(boundaryOrder(t, provider), " ")
	if got != "text:block:1" {
		t.Errorf("boundaries = [%s], want block 1 delivered despite block 0 never closing", got)
	}
	var kinds []string
	for _, p := range stepRecord(t, provider) {
		switch p.(type) {
		case *llm.ReasoningPart:
			kinds = append(kinds, "reasoning")
		case *llm.TextPart:
			kinds = append(kinds, "text")
		}
	}
	// Block 0 streamed content but never closed. It is still recorded — what the
	// user watched arriving is what the step produced — and it keeps its DECLARED
	// slot rather than being shoved to the tail: a position the server announced at
	// content_block_start is a claim about layout whether or not the closure ever
	// came, and the record is what gets replayed. Order is therefore
	// [reasoning, text] even though only block 1 finished.
	if strings.Join(kinds, ",") != "reasoning,text" {
		t.Errorf("record = [%s], want reasoning,text per the declared indexes", strings.Join(kinds, ","))
	}
}

// A stream cut before any block closed still lands in the server's declared
// index order. Layout is declared with the content (TextDeltaEvent.Position, from
// the index the server announced at content_block_start) precisely so that the
// closure is not the only place the record's order comes from: an earlier design
// ordered blocks that never closed by arrival, and a server that opened thinking=0
// then text=1 but streamed text's delta first recorded [text, reasoning] —
// inverted against the shape Anthropic defines for an assistant turn, and replayed
// to the model that way.
//
// What the closure still decides is whether a block is *finished*; it no longer
// has to decide where things go.
func TestAnthropicCutStepStillOrdersByDeclaredIndex(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"ANSWER\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"THINK\"}}\n\n")
		// no content_block_stop, no message_stop: the body just ends
	})
	provider, err := providers.NewAnthropic(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}

	record, err := recordOfStep(t, provider)
	if err == nil {
		t.Fatal("a body with no message_stop must report an error")
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
		t.Errorf("cut record = [%s], want [reasoning,text]: the server declared thinking=0 before text=1, "+
			"and the session file is what gets replayed", got)
	}
}
