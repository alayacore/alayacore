package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Why llm.Agent keeps a second copy of the step's text at all (stepTextBlocks):
// the provider's own accumulator is unreachable once the step ends early.
// streamEvents stops pulling when the stream errors, so a provider written the
// way both of them are — checking yield's result and returning — never gets to
// the tail that would hand its buffer over.
func TestProviderCannotHandBackItsBufferAfterAbandonment(t *testing.T) {
	reachedTail := false
	sawComplete := ""
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		if !yield(ReasoningDeltaEvent{Delta: "thinking", Key: "reasoning"}, nil) {
			return
		}
		if !yield(TextDeltaEvent{Delta: "Answer", Key: "text"}, nil) {
			return
		}
		if !yield(nil, errors.New("boom")) {
			return // every real provider stops here
		}
		// parseStream's tail: emit the authoritative completes, then the step.
		reachedTail = true
		yield(ReasoningCompleteEvent{Key: "reasoning"}, nil)
	}}

	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 2})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:               func() uint64 { return 1 },
		OnReasoningDelta:    func(string, uint64) error { return nil },
		OnTextDelta:         func(string, uint64) error { return nil },
		OnReasoningComplete: func(text string, _ uint64) error { sawComplete = text; return nil },
	}); err == nil {
		t.Fatal("want the stream error to reach the caller")
	}

	if reachedTail {
		t.Error("provider ran its post-stream tail after the consumer abandoned it")
	}
	if sawComplete != "" {
		t.Errorf("provider delivered authoritative text after abandonment: %q", sawComplete)
	}
}

// The other half of the same fact: a provider cannot deliver even if it tries.
// iter.Seq2 gives a producer no channel to hand back data after the consumer
// stops pulling, so "let the provider salvage itself" is not an option that
// exists. Pinning the runtime's rule here, because the alternative — reading the
// spec and guessing — is how the duplicate buffer gets deleted someday.
func TestYieldAfterAbandonmentPanics(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ReasoningDeltaEvent{Delta: "thinking", Key: "reasoning"}, nil) // true: still pulling
		yield(nil, errors.New("boom"))                                       // false: caller returns
		// Ignoring that is legal Go and a runtime error at once.
		_ = yield(TextCompleteEvent{Key: "text"}, nil)
	}}

	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 2})
	panicked := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = "runtime error"
			}
		}()
		_, _ = agent.Stream(context.Background(), nil, StreamCallbacks{
			IDGen:            func() uint64 { return 1 },
			OnReasoningDelta: func(string, uint64) error { return nil },
		})
	}()

	if !strings.Contains(panicked, "runtime error") {
		t.Fatal("yield after abandonment returned normally; a provider could still deliver content")
	}
}
