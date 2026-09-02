package llm

import (
	"context"
	"strings"
	"testing"
)

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
