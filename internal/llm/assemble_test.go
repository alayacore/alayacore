package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// render summarizes a record as "kind:text(id=N)" entries for comparison.
func render(parts []ContentPart) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		var label string
		switch v := p.(type) {
		case *ReasoningPart:
			label = "reasoning(" + v.Text + ")"
		case *TextPart:
			label = "text(" + v.Text + ")"
		case *ToolInputPart:
			label = "call(" + v.ID + ")"
		}
		out = append(out, label+" id="+strconv.FormatUint(p.GetHistoryID(), 10))
	}
	return strings.Join(out, " | ")
}

// The record's layout is the order blocks were *closed*, not the order bytes
// arrived. This is the property that replaces each provider's private
// getContents(): Anthropic closes in the index order its server declared, OpenAI
// closes its three slots in assistant-turn order, and neither order is arrival
// order. The assembler must therefore not sort by touch time.
func TestAssemblerLaysOutByCloseOrderNotArrival(t *testing.T) {
	a := newStreamAssembler()
	a.text("text", "Answer.")               // arrives first
	a.reasoning("reasoning", "thinking")    // arrives second
	a.close("reasoning")                    // closed first
	a.close("text")                         // closed second

	got := render(a.parts(map[string]uint64{"reasoning": 1, "text": 2}))
	want := "reasoning(thinking) id=1 | text(Answer.) id=2"
	if got != want {
		t.Errorf("record = [%s], want [%s]", got, want)
	}
}

// A step that was cut has no close order for its last blocks. They must still
// reach the record, in the only order that is left — the order they appeared —
// and after the blocks that did close, so a partial tail can never shuffle a
// completed head.
func TestAssemblerAppendsUnclosedAfterClosed(t *testing.T) {
	a := newStreamAssembler()
	a.reasoning("r1", "first thought")
	a.close("r1")
	a.text("t1", "second, never closed")
	a.reasoning("r2", "third, never closed")

	got := render(a.parts(map[string]uint64{"r1": 1, "t1": 2, "r2": 3}))
	want := "reasoning(first thought) id=1 | text(second, never closed) id=2 | reasoning(third, never closed) id=3"
	if got != want {
		t.Errorf("record = [%s], want [%s]", got, want)
	}
}

// A boundary event for a block that never sent content must not create one.
// This is the fabrication case that used to need an explicit rejection at the
// step's end: with one assembler and content arriving through one channel, there
// is nothing to reject, because the record is built from what was streamed.
func TestAssemblerCannotBeToldContentItNeverReceived(t *testing.T) {
	a := newStreamAssembler()
	a.text("text", "real")
	a.close("text")
	a.close("ghost") // a provider naming a block it never streamed

	if got := len(a.touched); got != 1 {
		t.Fatalf("assembler holds %d blocks, want 1", got)
	}
	got := render(a.parts(map[string]uint64{"text": 1}))
	if got != "text(real) id=1" {
		t.Errorf("record = [%s], want only the streamed block", got)
	}
}

// OpenAI's protocol: the ID and name arrive once and argument fragments are
// keyed by index, with an empty ID on the continuation chunks. The assembler
// joins the fragments; the boundary handler executes the result.
func TestAssemblerJoinsToolArgsFromFragments(t *testing.T) {
	a := newStreamAssembler()
	a.toolStart("tool:0", "c1", "read_file")
	a.toolArgs("tool:0", `{"path":`)
	a.toolArgs("tool:0", `"README.md"}`)
	a.close("tool:0")

	id, name, args := a.toolCall("tool:0")
	if id != "c1" || name != "read_file" {
		t.Errorf("identity = (%q,%q), want (c1,read_file)", id, name)
	}
	if json.Valid([]byte(args)) != true {
		t.Errorf("joined arguments are not valid JSON: %q", args)
	}
	if args != `{"path":"README.md"}` {
		t.Errorf("args = %q", args)
	}
	// A tool call is only history-worthy once its result exists, so parts()
	// must not carry it.
	if got := a.parts(map[string]uint64{"tool:0": 1}); len(got) != 0 {
		t.Errorf("parts() returned %d entries, want 0 (tool pairs are built elsewhere): %#v", len(got), got)
	}
}

// An empty slot is not content. Providers open a reasoning and a text slot for
// every turn, and the one that was never used must not be saved as an empty
// part the model then sees.
func TestAssemblerSkipsEmptyBlocks(t *testing.T) {
	a := newStreamAssembler()
	a.reasoning("reasoning", "")
	a.close("reasoning")
	a.text("text", "hi")
	a.close("text")

	got := render(a.parts(map[string]uint64{"text": 1}))
	if got != "text(hi) id=1" {
		t.Errorf("record = [%s], want the empty reasoning slot dropped", got)
	}
}

// IDs are claimed by key, the same numbers the display windows were built with,
// and a step with no numbering configured still assembles its content.
func TestAssemblerBindsIDsByKeyAndToleratesNone(t *testing.T) {
	a := newStreamAssembler()
	a.reasoning("r", "thought")
	a.text("t", "answer")
	a.close("r")
	a.close("t")

	withIDs := a.parts(map[string]uint64{"r": 41, "t": 42})
	if render(withIDs) != "reasoning(thought) id=41 | text(answer) id=42" {
		t.Errorf("record = [%s]", render(withIDs))
	}
	for _, p := range withIDs {
		if p.GetBlockKey() == "" {
			t.Errorf("%T carries no block key", p)
		}
		if p.GetRole() != RoleAssistant {
			t.Errorf("%T role = %q, want assistant", p, p.GetRole())
		}
	}
	if none := a.parts(nil); len(none) != 2 {
		t.Errorf("parts(nil) dropped content: %d", len(none))
	} else {
		for _, p := range none {
			if p.GetHistoryID() != 0 {
				t.Errorf("%T got ID %d with no numbering configured", p, p.GetHistoryID())
			}
		}
	}
}

// Closing twice must not place a block twice, and a key reused across kinds
// keeps the kind the stream opened it as.
func TestAssemblerIsIdempotentOnCloseAndKind(t *testing.T) {
	a := newStreamAssembler()
	a.text("t", "one")
	a.close("t")
	a.close("t")
	a.reasoning("t", "-smuggled") // same key, different kind

	got := render(a.parts(map[string]uint64{"t": 7}))
	want := "text(one-smuggled) id=7"
	if got != want {
		t.Errorf("record = [%s], want [%s]", got, want)
	}
}
