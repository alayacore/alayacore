package terminal

// Phase-3 soft-wrap refactor tests (REFACTOR.md): tool content styling
// across fragment boundaries. Tool content lines are styled per visual
// line (wrapContent's WrapWriter re-applies the active style at every
// hard-wrap break), so each border.lines element is self-contained:
// clipping a window mid-content must yield fragments whose ANSI styles
// match the same lines rendered in the full window — no style context
// replay needed at fragment starts.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

// makeDiffWindow builds a WindowBuffer with one expanded edit_file window
// carrying diff input (colored - / + lines) and a tool output with a long
// line that wraps across visual rows.
func makeDiffWindow(t *testing.T, width int) *WindowBuffer {
	t.Helper()
	wb := NewWindowBuffer(width, DefaultStyles())

	// Input is raw tool-call text with REAL newlines (as produced by the
	// agent) — the diff prefixes drive RenderDiffContent's coloring.
	input := "edit_file: /tmp/x\n- old line one\n+ new line one\n  context line\n- another old line"
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t1", Name: "edit_file", Input: json.RawMessage(input)}, 1)
	// Output: a separator + a long line that wraps across visual rows.
	wb.HandleToolOutput("t1", "long output line that definitely wraps across several visual rows at this width", false, 2)
	wb.ToggleFold(0) // expand
	return wb
}

// TestToolFragmentStylesMatchFullRender verifies that a viewport clipped
// into the middle of an expanded diff/tool window produces fragments with
// exactly the same ANSI styling as the corresponding visual lines of a
// full window render (styles are self-contained per visual line).
func TestToolFragmentStylesMatchFullRender(t *testing.T) {
	wb := makeDiffWindow(t, 40)
	_ = wb.GetAll(-1, false) // full render populates border.lines
	w := wb.WindowAt(0)
	if w == nil {
		t.Fatal("tool window not found")
	}
	allLines := append([]visualLine(nil), w.border.lines...)
	if len(allLines) < 6 {
		t.Fatalf("expected a multi-row tool window, got %d lines", len(allLines))
	}

	// Scrolled into the middle: viewport [3, len) — the remaining rows,
	// exactly the viewport height, so no blank padding is appended.
	height := len(allLines) - 3
	wb.SetViewportPosition(3, height)
	frag := wb.GetAll(-1, false)

	// Reconstruct the expected fragment from the full render's rows:
	// rows joined per continuation marks (Cont rows follow without '\n',
	// new original lines are separated by '\n'), rows followed by a
	// continuation padded to the width, and rows ending an original line
	// (or the fragment's last row) get an EL erase (row-tail residue
	// cleanup under the overlay renderer).
	want := ""
	for i := 3; i < len(allLines); i++ {
		if i > 3 && !allLines[i].Cont {
			want += "\n"
		}
		want += allLines[i].Text
		if i < len(allLines)-1 && allLines[i+1].Cont {
			want += strings.Repeat(" ", 40-cellWidth(allLines[i].Text))
		} else {
			want += "\x1b[K"
		}
	}
	if frag != want {
		t.Errorf("fragment styles differ from full render:\n  got:  %q\n  want: %q", frag, want)
	}

	// Sanity: the fragment equals the reconstruction (already asserted) and
	// contains the same plain text as the corresponding full-render rows.
	gotPlain := stripANSI(frag)
	wantPlain := ""
	for i := 3; i < len(allLines); i++ {
		if i > 3 && !allLines[i].Cont {
			wantPlain += "\n"
		}
		wantPlain += stripANSI(allLines[i].Text)
		if i < len(allLines)-1 && allLines[i+1].Cont {
			wantPlain += strings.Repeat(" ", 40-cellWidth(allLines[i].Text))
		}
	}
	if gotPlain != wantPlain {
		t.Errorf("fragment plain text mismatch:\n  got:  %q\n  want: %q", gotPlain, wantPlain)
	}
}

// TestToolFragmentMidColorWrap verifies that a long diff line wrapping
// across visual rows keeps its content AND its diff color when scrolled
// into the middle: every wrapped continuation row is self-contained —
// the WrapWriter re-applies the DiffRemove color at each hard-wrap break.
func TestToolFragmentMidColorWrap(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(40, styles)

	// A single long diff line (- prefix) far wider than the window: its
	// wrapped continuations keep the DiffRemove color.
	input := "edit_file: /tmp/x\n- " + strings.Repeat("X", 120)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t1", Name: "edit_file", Input: json.RawMessage(input)}, 1)
	wb.ToggleFold(0)

	wb.SetViewportPosition(4, 10) // inside the wrapped diff input
	frag := wb.GetAll(-1, false)

	// The content region holds the wrapped diff rows with their color.
	plain := stripANSI(frag)
	if !strings.Contains(plain, strings.Repeat("X", 40)) {
		t.Errorf("scrolled fragment should contain wrapped diff content, got %q", plain)
	}
	// Every wrapped row carries the DiffRemove color (self-contained):
	// the styled 40-X continuation row appears with its SGR prefix + reset.
	styledRow := styles.DiffRemove.Render(strings.Repeat("X", 40))
	if !strings.Contains(frag, styledRow) {
		t.Errorf("wrapped diff rows should carry the DiffRemove color, got %q", frag)
	}
}
