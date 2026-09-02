package providers_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// The OpenAI provider builds the persisted content array as
// [reasoning, text, tools] unconditionally (openai.go getContents), but it
// used to emit the *complete* events as text-then-reasoning. Both orders feed
// the same step, and the mismatch is what the --no-delta run renders: with no
// deltas pending, the authoritative AR/AT frames create the TUI windows in
// arrival order, so emitting text first put ASSISTANT above the REASONING the
// session file lists first.
//
// Pin complete-event order to content-array order.
func TestOpenAICompleteEventsFollowContentPositions(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking hard\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:         "test",
		BaseURL:        server.URL,
		ReasoningField: "reasoning",
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for event := range events {
		switch event.(type) {
		case llm.ReasoningCompleteEvent:
			order = append(order, "reasoning-complete")
		case llm.TextCompleteEvent:
			order = append(order, "text-complete")
		}
	}
	// The record the boundaries describe, assembled by llm.Agent.
	contents := stepRecord(t, provider)

	want := []string{"reasoning-complete", "text-complete"}
	if len(order) != len(want) {
		t.Fatalf("complete events = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("complete event %d = %q, want %q — event order must match content-array order",
				i, order[i], want[i])
		}
	}

	// The array the step is persisted from: reasoning at position 0.
	if len(contents) < 2 {
		t.Fatalf("contents = %d parts, want at least 2: %#v", len(contents), contents)
	}
	if _, ok := contents[0].(*llm.ReasoningPart); !ok {
		t.Errorf("contents[0] = %T, want *llm.ReasoningPart", contents[0])
	}
	if _, ok := contents[1].(*llm.TextPart); !ok {
		t.Errorf("contents[1] = %T, want *llm.TextPart", contents[1])
	}
}

// A step that also calls a tool must emit tool-complete *last*, because
// getContents() persists tools after reasoning and text. Before the tool loop
// was moved below this pair, a --no-delta run emitted AF first: the tool block
// took the step's lowest historyID while sitting last in the record, and the
// terminal rendered TOOL CALL above the REASONING that produced it.
func TestOpenAICompleteEventsIncludeToolsLast(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"think\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey: "test", BaseURL: server.URL, ReasoningField: "reasoning",
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.StreamMessages(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for event := range events {
		switch event.(type) {
		case llm.ReasoningCompleteEvent:
			order = append(order, "reasoning")
		case llm.TextCompleteEvent:
			order = append(order, "text")
		case llm.ToolInputCompleteEvent:
			order = append(order, "tool")
		}
	}

	want := []string{"reasoning", "text", "tool"}
	if len(order) != len(want) {
		t.Fatalf("complete events = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("complete event %d = %q, want %q — must follow the persisted array order",
				i, order[i], want[i])
		}
	}
}
