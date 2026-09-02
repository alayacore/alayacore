package providers_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// An OpenAI assistant turn has one shape — reasoning, content, tool calls — and
// this provider closes its blocks in it. The closures are what place blocks in
// the persisted record (llm.Agent's assembler lays out by close order), so when
// the tail emitted the boundary events text-then-reasoning the record and the
// display inverted against the shape the protocol defines.
//
// Pin closure order to the protocol's assistant-turn shape.
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

// A step that also calls a tool must close the tool *last*, because tools sit
// after reasoning and text in an assistant turn. Before the tool loop moved below
// this pair, a --no-delta run closed the tool first: the record followed that
// order while the window for TOOL CALL was created above the REASONING that
// produced it.
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
