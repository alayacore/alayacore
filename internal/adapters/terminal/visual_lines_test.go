package terminal

// Phase-1 soft-wrap refactor tests (REFACTOR.md): the rendering pipeline
// produces VISUAL line arrays — each element is one terminal row with no
// '\n' inside. Window.Render caches these in border.lines, and
// WindowBuffer.ensureLineHeights derives lineHeights from their count.
// These tests lock in that structure so the Phase-2 viewport fragment
// output can consume it.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestWindowVisualLinesExpanded verifies that an expanded window renders to
// a visual line array with the expected shape:
//
//	[0] header (" ASSISTANT")
//	[1] top rule
//	[2..n-2] wrapped content lines (one terminal row each)
//	[n-1] bottom rule
//
// and that no element contains a hard newline.
func TestWindowVisualLinesExpanded(t *testing.T) {
	styles := DefaultStyles()
	w := NewWindow("at-1", tlv.TagAssistantT, styles)
	w.Visible = true
	w.Folded = false
	// 25 words * 5 chars = 125 chars → wraps to several rows at width 40.
	w.AppendContent(strings.Repeat("word ", 25))

	width := 40
	rendered := w.Render(width, false, styles,
		NewStyle().Foreground(styles.ColorDim), NewStyle(), false)

	lines := w.border.lines
	if len(lines) != w.LineCount() {
		t.Errorf("border.lines len = %d, LineCount() = %d, want equal", len(lines), w.LineCount())
	}
	if len(lines) < 4 {
		t.Fatalf("expected header + rule + content + rule (>= 4 lines), got %d: %q", len(lines), joinVisualLines(lines))
	}
	if !strings.Contains(stripANSI(lines[0].Text), "ASSISTANT") {
		t.Errorf("line 0 should be the header, got %q", lines[0].Text)
	}
	// Top and bottom rules: plain '─' runs (strip ANSI).
	if plain := stripANSI(lines[1].Text); plain != strings.Repeat("─", width) {
		t.Errorf("line 1 should be the top rule of %d '─', got %q", width, plain)
	}
	if plain := stripANSI(lines[len(lines)-1].Text); plain != strings.Repeat("─", width) {
		t.Errorf("last line should be the bottom rule of %d '─', got %q", width, plain)
	}
	// Every visual line is exactly one terminal row (no hard newline).
	for i, ln := range lines {
		if strings.Contains(ln.Text, "\n") {
			t.Errorf("visual line %d contains a hard newline: %q", i, ln.Text)
		}
	}
	// The rendered output is exactly arrow + the joined visual lines (the
	// current line-based output path stays consistent).
	want := arrowStyle(styles).Render(w.arrowChar()) + joinVisualLines(lines)
	if rendered != want {
		t.Errorf("rendered mismatch:\n  got:  %q\n  want: %q", rendered, want)
	}
}

// TestWindowVisualLinesFolded verifies the folded window is exactly one
// visual line (no box).
func TestWindowVisualLinesFolded(t *testing.T) {
	styles := DefaultStyles()
	w := NewWindow("at-1", tlv.TagAssistantT, styles)
	w.Visible = true
	w.Folded = true
	w.AppendContent("hello world")

	w.Render(40, false, styles,
		NewStyle().Foreground(styles.ColorDim), NewStyle(), false)

	if len(w.border.lines) != 1 {
		t.Fatalf("folded window should be 1 visual line, got %d: %q", len(w.border.lines), joinVisualLines(w.border.lines))
	}
	if w.LineCount() != 1 {
		t.Errorf("LineCount() = %d, want 1", w.LineCount())
	}
	if strings.Contains(w.border.lines[0].Text, "\n") {
		t.Errorf("folded visual line contains hard newline: %q", w.border.lines[0].Text)
	}
}

// TestWindowBufferLineHeightsAreVisual verifies ensureLineHeights derives
// line heights from the visual line count (not '\n' counting), matching
// Window.Render's border.lines for both folded and expanded windows.
func TestWindowBufferLineHeightsAreVisual(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())

	// Expanded AT with multi-row content.
	atIdx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning (default folded).
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short")

	wb.GetTotalLines() // triggers ensureLineHeights

	at := wb.WindowAt(atIdx)
	if at == nil {
		t.Fatal("AT window not found")
	}
	// Force a full render so border.lines is populated.
	at.Render(40, false, wb.styles, wb.borderStyle, wb.cursorStyle, false)
	if got, want := wb.lineHeights[atIdx], len(at.border.lines); got != want {
		t.Errorf("AT lineHeight = %d, border.lines = %d, want equal", got, want)
	}
	if wb.lineHeights[atIdx] != at.LineCount() {
		t.Errorf("AT lineHeight = %d, LineCount() = %d, want equal", wb.lineHeights[atIdx], at.LineCount())
	}

	// Folded window: exactly 1 visual line (ensureLineHeights uses the
	// O(1) fast path for folded windows without rendering).
	arIdx := mustLookupIdx(wb, "ar-1")
	ar := wb.WindowAt(arIdx)
	if ar == nil {
		t.Fatal("AR window not found")
	}
	if wb.lineHeights[arIdx] != 1 {
		t.Errorf("folded AR lineHeight = %d, want 1", wb.lineHeights[arIdx])
	}
	if !ar.Folded {
		t.Error("AR window should start folded")
	}
}

// mustLookupIdx is a test helper returning the window index for an ID,
// panicking if absent.
func mustLookupIdx(wb *WindowBuffer, id string) int {
	idx, ok := wb.LookupID(id)
	if !ok {
		panic("window not found: " + id)
	}
	return idx
}
