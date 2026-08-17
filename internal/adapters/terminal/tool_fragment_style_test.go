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

	ansi "github.com/charmbracelet/x/ansi"

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
	allLines := append([]string(nil), w.border.lines...)
	if len(allLines) < 6 {
		t.Fatalf("expected a multi-row tool window, got %d lines", len(allLines))
	}

	// Scrolled into the middle: viewport [3, 3+height) inside the window.
	height := 12
	wb.SetViewportPosition(3, height)
	frag := wb.GetAll(-1, false)
	if strings.Contains(frag, "\n") {
		t.Fatalf("single-window fragment must be continuous text: %q", frag)
	}

	// Reconstruct the expected fragment from the full render's lines:
	// lines[3:] joined continuously, padded to width except the last.
	want := ""
	for i, ln := range allLines[3:] {
		want += ln
		if i < len(allLines)-4 { // pad all but the last line
			want += strings.Repeat(" ", 40-ansi.StringWidth(ln))
		}
	}
	if frag != want {
		t.Errorf("fragment styles differ from full render:\n  got:  %q\n  want: %q", frag, want)
	}

	// Sanity: the fragment equals the reconstruction (already asserted) and
	// contains the same plain text as the corresponding full-render lines.
	gotPlain := stripANSI(frag)
	wantPlain := ""
	for i, ln := range allLines[3:] {
		wantPlain += stripANSI(ln)
		if i < len(allLines)-4 {
			wantPlain += strings.Repeat(" ", 40-ansi.StringWidth(ln))
		}
	}
	if gotPlain != wantPlain {
		t.Errorf("fragment plain text mismatch:\n  got:  %q\n  want: %q", gotPlain, wantPlain)
	}
}

// TestToolFragmentMidColorWrap verifies that a long colored line wrapping
// across visual rows keeps its color on continuation rows when scrolled
// into the middle — the wrap continuation carries the style, so the
// fragment start needs no explicit style replay.
func TestToolFragmentMidColorWrap(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())

	// A single long diff line (- prefix) far wider than the window: its
	// wrapped continuations must stay DiffRemove-colored.
	input := "edit_file: /tmp/x\n- " + strings.Repeat("X", 120)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t1", Name: "edit_file", Input: json.RawMessage(input)}, 1)
	wb.ToggleFold(0)

	wb.SetViewportPosition(4, 10) // inside the wrapped diff input
	frag := wb.GetAll(-1, false)

	// The continuation rows must carry DiffRemove color — the exact SGR
	// code from a DiffRemove-styled glyph must appear in the fragment.
	removeCode := sgrCode(DefaultStyles().DiffRemove.Render("X"))
	if !strings.Contains(frag, removeCode) {
		t.Errorf("scrolled fragment lost the diff color code %q: %q", removeCode, frag)
	}
	plain := stripANSI(frag)
	if !strings.Contains(plain, strings.Repeat("X", 40)) {
		t.Errorf("scrolled fragment should contain wrapped diff content, got %q", plain)
	}
}

// sgrCode extracts the first SGR escape sequence ("\x1b[...m") from a
// styled string.
func sgrCode(styled string) string {
	if i := strings.Index(styled, "\x1b["); i >= 0 {
		if j := strings.Index(styled[i:], "m"); j >= 0 {
			return styled[i : i+j+1]
		}
	}
	return ""
}
