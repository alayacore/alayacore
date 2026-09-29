package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

// renderVirtual pads a row to the buffer width only when the next row continues
// it, and it measures the row it is drawing in order to do so. Two properties
// make that safe, and both are load-bearing rather than incidental — these hold
// them.

// TestWindowRow0IsNeverSoftWrapped is the first. Row 0 is the window's own
// line, built whole by Window.buildLines, and the row after it always starts a
// new original line — so row 0 is never padded. That is what lets the cursor's
// register swap row 0 for a recolored copy without any padding decision
// changing: a recolored row is the same width, but "is the same width" is then
// a fact nothing depends on rather than one the frame relies on.
func TestWindowRow0IsNeverSoftWrapped(t *testing.T) {
	long := "content that is long enough to wrap at any of the widths below "
	// Narrow widths, so anything that can wrap does: a soft-wrapped row is
	// exactly the case that would put a continuation after row 0.
	for _, width := range []int{12, 24, 40, 80} {
		wb := NewWindowBuffer(width, DefaultStyles())
		// One window per renderer kind, in both fold states.
		wb.AppendOrUpdate("AT", "text", strings.Repeat(long, 6))
		wb.AppendOrUpdate("UT", "user", strings.Repeat(long, 6))
		wb.HandleToolInputEvent(protocol.ToolInputData{
			ID: "t1", Name: "edit_file", Input: json.RawMessage(strings.Repeat(long+"\n", 6)),
		}, 1)

		for _, folded := range []bool{false, true} {
			for i := range wb.windows {
				w := wb.WindowAt(i)
				w.Folded = folded
				w.Invalidate()
				w.buildLines(width, DefaultStyles(), false)
				if len(w.cache.lines) < 2 {
					continue // nothing after row 0 that could continue it
				}
				if w.cache.lines[1].Cont {
					t.Errorf("width %d, window %d (folded=%v): row 1 continues row 0, so row 0 would be padded:\n row0 %q\n row1 %q",
						width, i, folded, w.cache.lines[0].Text, w.cache.lines[1].Text)
				}
			}
		}
	}
}

// TestCursorFramePadsTheRowsItDraws is the second, at frame level: with the
// window under the cursor and the fragment starting at its first row, the frame
// is still exactly the rows as drawn, each padded one joined against the next.
// The expectation is rebuilt here from the window's own rows rather than read
// back out of the renderer, so a width taken from the wrong row shows up as a
// misaligned pad.
func TestCursorFramePadsTheRowsItDraws(t *testing.T) {
	const width = 40
	wb := makeDiffWindow(t, width)
	_ = wb.GetAll(-1, false) // full render populates the rows
	w := wb.WindowAt(0)
	allLines := append([]visualLine(nil), w.cache.lines...)
	if len(allLines) < 6 {
		t.Fatalf("expected a multi-row tool window, got %d rows", len(allLines))
	}
	wraps := false
	for _, vl := range allLines[1:] {
		wraps = wraps || vl.Cont
	}
	if !wraps {
		t.Fatal("the fixture does not soft-wrap, so the frame would pad nothing")
	}

	wb.SetViewportPosition(0, len(allLines))
	frame := wb.GetAll(0, false) // cursorIndex 0: row 0 is swapped for its register

	// Row 0 in the cursor's register, then the body rows joined by
	// continuation mark. The branch structure mirrors renderVirtual's: a row
	// followed by a continuation is padded to the width so the terminal's own
	// soft wrap lands on the row boundary; a row ending an original line is
	// erased instead (no trailing spaces in a selection); the fragment's last
	// row is erased once, after the loop.
	want := w.cursorLine0() + "\x1b[K"
	for i := 1; i < len(allLines); i++ {
		if !allLines[i].Cont {
			want += "\n"
		}
		want += allLines[i].Text
		switch {
		case i < len(allLines)-1 && allLines[i+1].Cont:
			want += strings.Repeat(" ", width-cellWidth(allLines[i].Text))
		case i < len(allLines)-1:
			want += "\x1b[K"
		}
	}
	want += "\x1b[K"

	if frame != want {
		t.Errorf("cursor frame differs from the rows as drawn:\n got  %q\n want %q", frame, want)
	}
}
