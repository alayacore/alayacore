package llm

import (
	"context"
	"encoding/json"
	"testing"
)

// History IDs are minted when a block first appears in the stream, and the
// assembler holds the block, so nothing about the caller's display wiring can
// decide whether a block has an ID — or, now, whether it has content at all.
//
// --no-delta is the case that used to tempt the opposite answer: it registers no
// delta callbacks (session_task.go), which once meant the block's first
// callback-visible event was its complete event. It still means that, but it no
// longer matters, because the provider streams regardless and the assembler sees
// the deltas either way. Below, the text block arrives first and the reasoning
// block second, while the boundaries come in the opposite order: if the
// boundaries numbered anything, reasoning would take the lower ID.
func TestStreamNumbersBlocksAtDeltasEvenWithNoDeltaCallbacks(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "Answer", Key: "text"}, nil)
		yield(ReasoningDeltaEvent{Delta: "thi", Key: "reasoning"}, nil)
		yield(ReasoningCompleteEvent{Key: "reasoning"}, nil)
		yield(TextCompleteEvent{Key: "text"}, nil)
		yield(StepCompleteEvent{StopReason: "stop"}, nil)
	}}

	ids := map[string]uint64{}
	next := uint64(100)
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen: func() uint64 { n := next; next++; return n },
		// Complete handlers only: exactly what --no-delta wires up.
		OnReasoningComplete: func(_ string, id uint64) error { ids["reasoning"] = id; return nil },
		OnTextComplete:      func(_ string, id uint64) error { ids["text"] = id; return nil },
	}); err != nil {
		t.Fatal(err)
	}

	if ids["text"] >= ids["reasoning"] {
		t.Errorf("text id=%d reasoning id=%d: numbering followed the boundaries, not arrival; "+
			"--no-delta would number blocks differently from delta mode", ids["text"], ids["reasoning"])
	}
}

// A boundary for a block the stream never opened describes nothing: no content,
// no history ID, and nothing an adapter should draw. This is where the old code
// needed an explicit rejection — a provider could persist a part claiming a block
// it had never streamed, and the record kept it with a zero ID that no frame
// could address and that :fork could never resolve. There is no such part to
// reject now: the record is built from the blocks that were streamed, so a
// provider cannot describe content it did not send.
func TestStreamIgnoresBoundaryForBlockNeverStreamed(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "real", Key: "text"}, nil)
		yield(TextCompleteEvent{Key: "text"}, nil)
		yield(ReasoningCompleteEvent{Key: "ghost"}, nil) // never opened
		yield(ToolInputCompleteEvent{ID: "ghost-call", Key: "ghost-tool"}, nil)
		yield(StepCompleteEvent{StopReason: "stop"}, nil)
	}}

	next := uint64(1)
	var published []ContentPart
	frames := 0
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		IDGen:               func() uint64 { n := next; next++; return n },
		OnTextDelta:         func(string, uint64) error { return nil },
		OnTextComplete:      func(string, uint64) error { frames++; return nil },
		OnReasoningComplete: func(string, uint64) error { frames++; return nil },
		OnToolInputComplete: func(string, json.RawMessage, uint64) error { frames++; return nil },
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			published = append([]ContentPart{}, contents...)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if frames != 1 {
		t.Errorf("adapters saw %d complete frames, want 1 (the ghost boundary must not reach the display)", frames)
	}
	if len(published) != 1 {
		t.Fatalf("history holds %d parts, want 1: %#v", len(published), published)
	}
	if _, isText := published[0].(*TextPart); !isText {
		t.Errorf("part 0 = %T, want the one streamed TextPart", published[0])
	}
	if published[0].GetHistoryID() == 0 {
		t.Error("the streamed part lost its history ID")
	}
}

// A caller that does not persist content registers no IDGen. Blocks still
// assemble, still reach the display, and simply carry no number.
func TestStreamWithoutIDGenStillAssemblesContent(t *testing.T) {
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ReasoningDeltaEvent{Delta: "thought", Key: "reasoning"}, nil)
		yield(ReasoningCompleteEvent{Key: "reasoning"}, nil)
		yield(TextDeltaEvent{Delta: "answer", Key: "text"}, nil)
		yield(TextCompleteEvent{Key: "text"}, nil)
		yield(StepCompleteEvent{StopReason: "stop"}, nil)
	}}

	var published []ContentPart
	agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1})
	if _, err := agent.Stream(context.Background(), nil, StreamCallbacks{
		OnReasoningDelta:    func(string, uint64) error { return nil },
		OnReasoningComplete: func(string, uint64) error { return nil },
		OnTextDelta:         func(string, uint64) error { return nil },
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			published = append([]ContentPart{}, contents...)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if len(published) != 2 {
		t.Fatalf("assembled %d parts, want 2: %#v", len(published), published)
	}
	for _, p := range published {
		if p.GetHistoryID() != 0 {
			t.Errorf("%T got ID %d with no numbering configured", p, p.GetHistoryID())
		}
	}
}
