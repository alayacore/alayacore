package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// counter is a numbering source that behaves like a session's: one ID per block,
// ascending, never reused.
func counter(start uint64) func() uint64 {
	n := start
	return func() uint64 {
		got := n
		n++
		return got
	}
}

// render summarizes a record as "kind:text id=N" entries for comparison.
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

// Two different orders, both deliberate, and the assembler must keep them
// apart: a block's history ID is minted when it first appears (which is what the
// display windows are keyed by), while its position in the record is where the
// provider closed it (which is what gets persisted and replayed).
func TestAssemblerNumbersByArrivalAndLaysOutByClose(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.text(0, "text", "Answer.")            // arrives first, so takes the lower ID
	a.reasoning(0, "reasoning", "thinking") // arrives second
	a.close("reasoning")                    // closed first, so leads the record
	a.close("text")

	got := render(a.parts())
	want := "reasoning(thinking) id=2 | text(Answer.) id=1"
	if got != want {
		t.Errorf("record = [%s], want [%s]", got, want)
	}
}

// A step that was cut has no close order for the blocks that never closed. They
// must still reach the record, in the only order left — the order they appeared —
// and after the blocks that did close, so a partial tail can never shuffle a
// finished head.
func TestAssemblerAppendsUnclosedAfterClosed(t *testing.T) {
	a := newStreamAssembler(nil)
	a.reasoning(0, "r1", "first thought")
	a.close("r1")
	a.text(0, "t1", "second, never closed")
	a.reasoning(0, "r2", "third, never closed")

	got := render(a.parts())
	want := "reasoning(first thought) id=0 | text(second, never closed) id=0 | reasoning(third, never closed) id=0"
	if got != want {
		t.Errorf("record = [%s], want [%s]", got, want)
	}
}

// Naming the end of a block that never sent content must not create one. This is
// the fabrication case that used to need an explicit rejection where the step was
// assembled: with one assembler and content arriving through one channel there
// is nothing to reject, because the record is built from what was streamed.
func TestAssemblerCannotBeToldContentItNeverReceived(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.text(0, "text", "real")
	a.close("text")
	a.close("ghost") // a provider naming a block it never streamed

	if got := len(a.touched); got != 1 {
		t.Fatalf("assembler holds %d blocks, want 1", got)
	}
	if got := render(a.parts()); got != "text(real) id=1" {
		t.Errorf("record = [%s], want only the streamed block", got)
	}
}

// OpenAI's protocol shape: ID and name arrive once, then argument fragments
// keyed by index with an empty ID. The assembler joins them, and the part it
// freezes at the boundary is the same object the record emits — so a tool cannot
// run with one input and be stored with another.
func TestAssemblerJoinsToolArgsAndFreezesOnePart(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.toolStart(0, "tool:0", "c1", "read_file")
	a.toolArgs(0, "tool:0", `{"path":`)
	a.toolArgs(0, "tool:0", `"README.md"}`)
	a.close("tool:0")

	id, name, args := a.toolCall("tool:0")
	if id != "c1" || name != "read_file" {
		t.Errorf("identity = (%q,%q), want (c1,read_file)", id, name)
	}
	if args != `{"path":"README.md"}` {
		t.Errorf("args = %q", args)
	}
	if !json.Valid([]byte(args)) {
		t.Errorf("joined arguments are not valid JSON: %q", args)
	}

	part := a.beginToolCall("tool:0", json.RawMessage(args))
	if a.beginToolCall("tool:0", json.RawMessage(`{"path":"OTHER"}`)) != part {
		t.Error("a repeated boundary replaced the call's input")
	}
	got := render(a.parts())
	if got != "call(c1) id=1" {
		t.Errorf("record = [%s], want the call once", got)
	}
	stored := a.parts()[0].(*ToolInputPart)
	if string(stored.Input) != args {
		t.Errorf("stored input = %q, want the input execution got (%q)", stored.Input, args)
	}
}

// A call whose arguments never completed must stay out of the record: an
// assistant tool_use without its tool_result is an unloadable conversation.
func TestAssemblerDropsToolWithoutPart(t *testing.T) {
	a := newStreamAssembler(nil)
	a.toolStart(0, "tool:0", "c1", "read_file")
	a.toolArgs(0, "tool:0", `{"path":`) // cut before the arguments finished
	if got := len(a.parts()); got != 0 {
		t.Errorf("record holds %d parts, want 0: %#v", got, a.parts())
	}
}

// An empty slot is not content. Providers open a reasoning and a text block for
// every turn, and the one that was never used must not be saved as an empty part
// the model then sees.
func TestAssemblerSkipsEmptyBlocks(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.reasoning(0, "reasoning", "")
	a.close("reasoning")
	a.text(0, "text", "hi")
	a.close("text")

	// The ID the empty reasoning slot consumed is not a mistake to fix: only a
	// block that streamed content opens one, and no provider opens a delta with
	// an empty string. What matters is that the empty part is gone.
	if got := render(a.parts()); got != "text(hi) id=2" {
		t.Errorf("record = [%s], want the empty reasoning slot dropped", got)
	}
}

// A caller with no numbering configured still gets its content; nothing is
// invented for it.
func TestAssemblerToleratesNoNumbering(t *testing.T) {
	a := newStreamAssembler(nil)
	a.reasoning(0, "r", "thought")
	a.text(0, "t", "answer")
	a.close("r")
	a.close("t")

	parts := a.parts()
	if len(parts) != 2 {
		t.Fatalf("assembled %d parts, want 2: %#v", len(parts), parts)
	}
	for _, p := range parts {
		if p.GetHistoryID() != 0 {
			t.Errorf("%T got ID %d with no numbering configured", p, p.GetHistoryID())
		}
		if p.GetRole() != RoleAssistant {
			t.Errorf("%T role = %q, want assistant", p, p.GetRole())
		}
	}
}

// Closing twice must not place a block twice, and a key reused across kinds
// keeps the kind the stream opened it as.
func TestAssemblerIsIdempotentOnCloseAndKind(t *testing.T) {
	a := newStreamAssembler(counter(7))
	a.text(0, "t", "one")
	a.close("t")
	a.close("t")
	a.reasoning(0, "t", "-smuggled") // same key, different kind

	if got := render(a.parts()); got != "text(one-smuggled) id=7" {
		t.Errorf("record = [%s]", got)
	}
}

// The declaration travels with the content, not only with the closure, because a
// stream can be cut before any block closes. Here nothing closes, and the deltas
// arrive in the wrong order — the case that left the persisted record inverted
// against the protocol's shape for an assistant turn, and replayed that way.
func TestDeclaredPositionOrdersBlocksThatNeverClosed(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.text(2, "text", "ANSWER")          // arrives first, declared second
	a.reasoning(1, "reasoning", "THINK") // arrives second, declared first

	if got := render(a.parts()); got != "reasoning(THINK) id=2 | text(ANSWER) id=1" {
		t.Errorf("record = [%s], want the declared slots to decide layout"+
			" (IDs stay arrival-based: numbering is display identity, layout is protocol shape)", got)
	}
}

// A declared position is a claim about layout, not about numbering: the block that
// arrives second still takes the second ID even though it leads the record. If
// these ever coincide again in the implementation, one of the two has stopped
// being what it is for.
func TestPositionDoesNotReorderIDs(t *testing.T) {
	a := newStreamAssembler(counter(10))
	a.toolStart(3, "tool:0", "c1", "noop")
	a.reasoning(1, "reasoning", "think")
	a.text(2, "text", "answer")
	if got := render(a.parts()); got != "reasoning(think) id=11 | text(answer) id=12" {
		t.Errorf("record = [%s]", got)
	}
	if a.historyID("reasoning") != 11 || a.historyID("text") != 12 {
		t.Errorf("IDs moved with layout: reasoning=%d text=%d, want 11 and 12 by arrival",
			a.historyID("reasoning"), a.historyID("text"))
	}
}

// Mixed: a provider that declares for some blocks only. The declared ones keep
// their slots, the undeclared ones fall back to arrival behind them — the fallback
// is per block, and an absent claim never drags a declared one around.
func TestUndeclaredBlocksLandAfterDeclaredOnes(t *testing.T) {
	a := newStreamAssembler(counter(1))
	a.text(0, "mystery", "surprise") // no declaration
	a.text(1, "text", "answer")
	a.close("text")
	a.close("mystery")

	if got := render(a.parts()); got != "text(answer) id=2 | text(surprise) id=1" {
		t.Errorf("record = [%s], want the declared block placed and the undeclared one appended", got)
	}
}
