package llm

import (
	"context"
	"strings"
	"testing"
)

// A session running --no-delta registers no delta callbacks (session_task.go
// only wires them when NoDelta is false), but the provider still streams deltas:
// --no-delta chooses which frames to put on the wire to the adapter, not whether
// the response streams. Numbering must therefore still be settled while the
// response arrives, so turning display deltas off must not move a single ID.
//
// This pins it from the other side: the stream below delivers the text block
// first and the reasoning block second, while the complete events come in the
// opposite order. If the blocks were numbered at their first *callback-visible*
// event, reasoning — whose complete handler is the only one registered — would
// take the lower ID. It must not.
func TestStreamNumbersBlocksAtDeltasEvenWithNoDeltaCallbacks(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "Answer", Key: "text"}, nil)        // arrives first
		yield(ReasoningDeltaEvent{Delta: "thi", Key: "reasoning"}, nil) // arrives second
		yield(ReasoningCompleteEvent{Text: "thinking", Key: "reasoning"}, nil)
		yield(TextCompleteEvent{Text: "Answer.", Key: "text"}, nil)
		yield(StepCompleteEvent{Contents: []ContentPart{
			&ReasoningPart{Text: "thinking", ContentPartMeta: ContentPartMeta{BlockKey: "reasoning"}},
			&TextPart{Text: "Answer.", ContentPartMeta: ContentPartMeta{BlockKey: "text"}},
		}}, nil)
	}}

	next := uint64(100)
	ids := map[string]uint64{}
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 2})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen: func() uint64 { n := next; next++; return n },
		// Complete handlers only: exactly what --no-delta wires up.
		OnReasoningComplete: func(_ string, id uint64) error { ids["reasoning"] = id; return nil },
		OnTextComplete:      func(_ string, id uint64) error { ids["text"] = id; return nil },
	}); err != nil {
		t.Fatal(err)
	}

	if ids["text"] >= ids["reasoning"] {
		t.Errorf("text id=%d reasoning id=%d: numbering followed the complete events, not arrival; "+
			"--no-delta would number blocks differently from delta mode", ids["text"], ids["reasoning"])
	}
}

// A provider that persists a part naming a block it never streamed used to be
// invisible: the positional lookup found no ID, the part kept the zero value,
// and the conversation was saved with a content entry that no adapter frame
// could address (and that :fork could never resolve, since 0 is the "no ID"
// sentinel). It is now rejected where the mistake happens.
func TestStreamRejectsPartWithUnstreamedBlockKey(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		// Streams a text block...
		yield(TextDeltaEvent{Delta: "hello", Key: "block:0"}, nil)
		yield(TextCompleteEvent{Text: "hello", Key: "block:0"}, nil)
		// ...then persists a second part claiming a block that never appeared.
		yield(StepCompleteEvent{
			Contents: []ContentPart{
				&TextPart{Text: "hello", ContentPartMeta: ContentPartMeta{BlockKey: "block:0"}},
				&TextPart{Text: "ghost", ContentPartMeta: ContentPartMeta{BlockKey: "block:7"}},
			},
		}, nil)
	}}

	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	next := uint64(1)
	_, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:          func() uint64 { n := next; next++; return n },
		OnTextDelta:    func(string, uint64) error { return nil },
		OnTextComplete: func(string, uint64) error { return nil },
	})

	if err == nil || !strings.Contains(err.Error(), "block:7") {
		t.Fatalf("want an error naming the unstreamed block, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no history ID") {
		t.Fatalf("want the error to explain the missing ID, got: %v", err)
	}
}

// Same contract, other direction: a part that carries no key at all is a
// provider that assembled content without ever identifying its block.
func TestStreamRejectsPartWithoutBlockKey(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "hello", Key: "block:0"}, nil)
		yield(StepCompleteEvent{
			Contents: []ContentPart{&TextPart{Text: "hello"}}, // no BlockKey
		}, nil)
	}}

	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	next := uint64(1)
	_, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:       func() uint64 { n := next; next++; return n },
		OnTextDelta: func(string, uint64) error { return nil },
	})

	if err == nil || !strings.Contains(err.Error(), "no block key") {
		t.Fatalf("want an error about the missing block key, got: %v", err)
	}
}

// And the other way round: with no IDGen there is nothing to bind, so a keyless
// part must NOT be an error — that caller never asked for history IDs.
func TestStreamWithoutIDGenToleratesKeylessParts(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "hello", Key: "block:0"}, nil)
		yield(StepCompleteEvent{
			Contents: []ContentPart{&TextPart{Text: "hello"}},
		}, nil)
	}}

	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{}); err != nil {
		t.Fatalf("no IDGen means no numbering; want no error, got: %v", err)
	}
}
