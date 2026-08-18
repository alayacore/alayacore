package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"
)

// TestDiffFrameRowsWrappedLineWholeRewrite: when a soft-wrapped base line
// changes, the diff must repaint the WHOLE logical line at its start row
// as one continuous write — the terminal auto-wraps it and keeps it ONE
// logical line. Per-terminal-row CUP rewrites would split the terminal's
// logical line, so copying the wrapped line afterwards yields hard
// newlines at the row boundaries (mid-word, since the wrapping is
// character-based).
func TestDiffFrameRowsWrappedLineWholeRewrite(t *testing.T) {
	const width = 40

	mkContent := func(n int) string {
		// One soft-wrapped base line (n chars → ceil(n/40) terminal rows)
		// followed by a full-width rule (a separate logical line).
		return strings.Repeat("a", n) + ansi.EraseLine(0) + "\n" +
			strings.Repeat("─", width) + ansi.EraseLine(0)
	}

	oldContent := mkContent(100) // terminal rows: [40, 40, 20]
	newContent := mkContent(105) // tail changed → rows [40, 40, 25]

	out := string(diffFrameRows(oldContent, newContent, width))

	// The whole new line must be written at its start row (row 1) in one
	// continuous write.
	if !strings.HasPrefix(out, ansi.CursorPosition(1, 1)) {
		t.Fatalf("diff should start with CUP to row 1, got %q", out[:min(len(out), 20)])
	}
	line := strings.Repeat("a", 105)
	if !strings.Contains(out, line) {
		t.Fatalf("diff must contain the full new line text (105 chars), got %q", out)
	}
	// The continuation rows must NOT be repainted with their own CUP
	// writes — that would split the terminal's logical line.
	for _, row := range []int{2, 3} {
		if strings.Contains(out, ansi.CursorPosition(1, row)) {
			t.Errorf("diff must not CUP-rewrite continuation row %d separately (splits the logical line): %q", row, out)
		}
	}
	// The unchanged rule (row 4) must not be repainted at all.
	if strings.Contains(out, ansi.CursorPosition(1, 4)) {
		t.Errorf("diff must not repaint the unchanged rule row: %q", out)
	}
}

// TestDiffFrameRowsSingleLineStillPerRow: single-terminal-row base rows
// keep the per-row composite repaint (no behavior change there).
func TestDiffFrameRowsSingleLineStillPerRow(t *testing.T) {
	const width = 40

	oldContent := "old text" + ansi.EraseLine(0) + "\n" + strings.Repeat("─", width) + ansi.EraseLine(0)
	newContent := "new text" + ansi.EraseLine(0) + "\n" + strings.Repeat("─", width) + ansi.EraseLine(0)

	out := string(diffFrameRows(oldContent, newContent, width))
	if !strings.Contains(out, "new text") {
		t.Fatalf("diff must contain the new single-line text, got %q", out)
	}
	if strings.Contains(out, strings.Repeat("─", width)) {
		t.Errorf("diff must not repaint the unchanged rule, got %q", out)
	}
}
