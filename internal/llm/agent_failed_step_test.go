package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A canceled or failed step must still contribute what it produced. The
// reasoning is the expensive, often longest part of a turn, and it was already
// shown on screen; dropping it leaves history holding a bare tool_use for a
// turn in which the model plainly thought first.
func TestFailedStepKeepsReasoningAndText(t *testing.T) {
	noop := Tool{
		Definition: ToolDefinition{Name: "noop"},
		Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
			return []ContentPart{&TextPart{Text: "done"}}, nil
		},
	}
	streamErr := errors.New("canceled")

	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ReasoningDeltaEvent{Delta: "thin", Key: "b0"}, nil)
		yield(ReasoningDeltaEvent{Delta: "king", Key: "b0"}, nil)
		yield(ReasoningCompleteEvent{Text: "thinking", Key: "b0"}, nil)
		yield(TextDeltaEvent{Delta: "Answer", Key: "b1"}, nil)
		yield(TextCompleteEvent{Text: "Answer.", Key: "b1"}, nil)
		yield(ToolInputStartEvent{ID: "c1", Name: "noop", Key: "b2"}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Key: "b2"}, nil)
		yield(nil, streamErr)
	}}

	// IDs as a session's counter would hand them out: one per block, in
	// arrival order, never reused.
	next := uint64(1)
	var published []ContentPart
	agent := NewAgent(AgentConfig{Provider: provider, Tools: []Tool{noop}, MaxSteps: 3})
	_, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:               func() uint64 { n := next; next++; return n },
		OnReasoningDelta:    func(string, uint64) error { return nil },
		OnReasoningComplete: func(string, uint64) error { return nil },
		OnTextDelta:         func(string, uint64) error { return nil },
		OnTextComplete:      func(string, uint64) error { return nil },
		OnToolInputStart:    func(_, _ string, _ uint64) error { return nil },
		OnToolInputComplete: func(string, json.RawMessage, uint64) error { return nil },
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			published = append([]ContentPart{}, contents...)
			return nil
		},
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("Stream err = %v, want %v", err, streamErr)
	}

	var kinds []string
	byKind := map[string]string{}
	idSeen := map[uint64]bool{}
	for _, p := range published {
		switch v := p.(type) {
		case *ReasoningPart:
			kinds = append(kinds, "reasoning")
			byKind["reasoning"] = v.Text
		case *TextPart:
			kinds = append(kinds, "text")
			byKind["text"] = v.Text
		case *ToolInputPart:
			kinds = append(kinds, "call")
		case *ToolOutputPart:
			kinds = append(kinds, "result")
		}
		id := p.GetHistoryID()
		if id == 0 {
			t.Errorf("%T has no history ID", p)
		}
		if idSeen[id] {
			t.Errorf("duplicate history ID %d on %T", id, p)
		}
		idSeen[id] = true
	}

	want := []string{"reasoning", "text", "call", "result"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("history = %v, want %v", kinds, want)
	}
	if byKind["reasoning"] != "thinking" {
		t.Errorf("reasoning = %q, want %q (complete text, not fragments)", byKind["reasoning"], "thinking")
	}
	if byKind["text"] != "Answer." {
		t.Errorf("text = %q, want %q", byKind["text"], "Answer.")
	}
	// The IDs must be the ones issued while streaming: the display windows are
	// keyed by them, so different numbers here would split screen from history.
	for _, id := range []uint64{1, 2, 3} {
		if !idSeen[id] {
			t.Errorf("history ID %d not reused — content saved under numbers the display never saw", id)
		}
	}
}

// Nothing completed and no tool ran: the partial blocks the user watched
// streaming still belong to history, the same way a max_tokens-truncated answer
// does today.
func TestCanceledStepKeepsPartialBlocks(t *testing.T) {
	streamErr := errors.New("canceled")
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ReasoningDeltaEvent{Delta: "a long thinki", Key: "b0"}, nil)
		yield(TextDeltaEvent{Delta: "a half writ", Key: "b1"}, nil)
		yield(nil, streamErr) // no complete events at all
	}}

	next := uint64(1)
	var published []ContentPart
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 2})
	_, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:            func() uint64 { n := next; next++; return n },
		OnReasoningDelta: func(string, uint64) error { return nil },
		OnTextDelta:      func(string, uint64) error { return nil },
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			published = append([]ContentPart{}, contents...)
			return nil
		},
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("Stream err = %v, want %v", err, streamErr)
	}
	if len(published) != 2 {
		t.Fatalf("published %d parts, want 2 (partial reasoning + partial answer): %#v", len(published), published)
	}
	r, ok := published[0].(*ReasoningPart)
	if !ok || r.Text != "a long thinki" {
		t.Errorf("part 0 = %#v, want ReasoningPart with the streamed fragment", published[0])
	}
	tx, ok := published[1].(*TextPart)
	if !ok || tx.Text != "a half writ" {
		t.Errorf("part 1 = %#v, want TextPart with the streamed fragment", published[1])
	}
}

// A provider error before anything streams must not invent empty parts.
func TestFailedStepWithNoContentAddsNothing(t *testing.T) {
	streamErr := errors.New("died early")
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(nil, streamErr)
	}}
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 2})
	next := uint64(1)
	published := -1
	_, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:        func() uint64 { n := next; next++; return n },
		OnStepFinish: func(contents []ContentPart, _ Usage) error { published = len(contents); return nil },
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("Stream err = %v, want %v", err, streamErr)
	}
	if published != -1 {
		t.Errorf("OnStepFinish fired with %d parts for a step that produced nothing", published)
	}
}
