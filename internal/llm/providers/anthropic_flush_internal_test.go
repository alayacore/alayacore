package providers

// A consumer that stops has stopped.
//
// message_stop flushes the closures that an earlier, never-closed block was
// holding back, and a consumer can stop in the middle of that flush — the flush
// is a yield loop like any other. The handler used to answer by reporting "keep
// iterating" and returning, which left parseStream reading a stream nobody wanted
// any more, and took the step's completion out of the sequence by accident rather
// than by rule. This pins the rule at the one place that decides it.

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestAnthropicMessageStopReportsAStoppedConsumer(t *testing.T) {
	p := &AnthropicProvider{}
	state := &anthropicStreamState{}

	// Block 0 is opened and never closed, so block 1's closure cannot be
	// delivered as it happens: it waits for message_stop to flush what is left.
	state.createBlock(0, anthropicBlockTypeText, "", "")
	state.createBlock(1, anthropicBlockTypeToolCall, "t1", "read_file")
	state.closeBlock(1)
	state.queueCloseReturn(1, pendingClose{blockType: anthropicBlockTypeToolCall, id: "t1"},
		func(llm.StreamEvent, error) bool { return true })

	consumerStopped := false
	stepCompleted := false
	yield := func(ev llm.StreamEvent, _ error) bool {
		switch ev.(type) {
		case llm.ToolInputCompleteEvent:
			consumerStopped = true
			return false // the consumer stops here, mid-flush
		case llm.StepCompleteEvent:
			stepCompleted = true
			return true
		}
		return true
	}

	keepIterating := p.handleEvent("message_stop", `{"usage":{"output_tokens":2}}`, yield, state)

	if !consumerStopped {
		t.Fatal("the flush delivered nothing for the consumer to stop on")
	}
	if keepIterating {
		t.Error("handleEvent reported 'keep iterating' after the consumer stopped")
	}
	if stepCompleted {
		t.Error("the step was completed for a consumer that had already stopped")
	}
}
