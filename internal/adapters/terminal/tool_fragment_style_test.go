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
	// continuation padded to the width, with an EL erase at the tail
	// (the overlay renderer's residue cleanup for the unpadded last row).
	want := ""
	for i := 3; i < len(allLines); i++ {
		if i > 3 && !allLines[i].Cont {
			want += "\n"
		}
		want += allLines[i].Text
		if i < len(allLines)-1 && allLines[i+1].Cont {
			want += strings.Repeat(" ", 40-ansi.StringWidth(allLines[i].Text))
		}
	}
	want += "\x1b[K"
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
			wantPlain += strings.Repeat(" ", 40-ansi.StringWidth(allLines[i].Text))
		}
	}
	if gotPlain != wantPlain {
		t.Errorf("fragment plain text mismatch:\n  got:  %q\n  want: %q", gotPlain, wantPlain)
	}
}

// TestToolFragmentMidColorWrap verifies that a long diff line wrapping
// across visual rows keeps its content when scrolled into the middle.
// Tool content is plain (no colors), so the continuation must carry no
// ANSI either.
func TestToolFragmentMidColorWrap(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())

	// A single long diff line (- prefix) far wider than the window: its
	// wrapped continuations stay plain (no diff colors).
	input := "edit_file: /tmp/x\n- " + strings.Repeat("X", 120)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t1", Name: "edit_file", Input: json.RawMessage(input)}, 1)
	wb.ToggleFold(0)

	wb.SetViewportPosition(4, 10) // inside the wrapped diff input
	frag := wb.GetAll(-1, false)

	// Tool content is plain: the wrapped diff rows carry no SGR codes.
	// The box rule and EL erase at the fragment tail are UI chrome (the
	// only ANSI present), so check the content region before them.
	plain := stripANSI(frag)
	if !strings.Contains(plain, strings.Repeat("X", 40)) {
		t.Errorf("scrolled fragment should contain wrapped diff content, got %q", plain)
	}
	head := frag
	if i := strings.Index(frag, "\x1b["); i >= 0 {
		head = frag[:i]
	}
	if strings.Contains(head, "\x1b") {
		t.Errorf("wrapped diff rows must be plain text, got %q", frag)
	}
	if !strings.Contains(stripANSI(head), strings.Repeat("X", 40)) {
		t.Errorf("content region should hold the wrapped diff rows, got %q", head)
	}
}
