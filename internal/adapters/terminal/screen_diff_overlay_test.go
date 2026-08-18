package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"
)

// TestOverlayCloseRestoresWrappedLine: a floating overlay box covers a
// continuation row of a soft-wrapped base line; when it closes, the diff
// must restore the WHOLE line (one continuous write), not just the
// covered row — otherwise the CUP-rewritten row leaves the terminal's
// logical line split and copies get hard newlines.
func TestOverlayCloseRestoresWrappedLine(t *testing.T) {
	const width = 40
	line := strings.Repeat("a", 100) // wraps to rows [40,40,20]
	rule := strings.Repeat("─", width)

	base := line + ansi.EraseLine(0) + "\n" + rule + ansi.EraseLine(0)
	// Overlay box covering terminal row 1 (a continuation row of the line),
	// like a floating selector: 2 rows of box text at rows 1-2.
	overlayOpen := base[:0] + line + ansi.EraseLine(0) + "\n" +
		ansi.CursorPosition(1, 2) + "BOXROW1" + strings.Repeat(" ", width-7) + ansi.EraseLine(0) + "\n" +
		ansi.CursorPosition(1, 3) + "BOXROW2" + strings.Repeat(" ", width-7) + ansi.EraseLine(0) + "\n" +
		rule + ansi.EraseLine(0)

	// Simulate the transition overlay-open -> overlay-closed (base unchanged).
	diff := string(diffFrameRows(overlayOpen, base, width))
	// The vanished overlay rows must trigger a whole-line restore: the full
	// line text written at its start row, no CUP to the continuation rows.
	if !strings.Contains(diff, line) {
		t.Fatalf("diff must restore the full wrapped line, got %q", diff)
	}
	for _, r := range []int{2, 3} {
		if strings.Contains(diff, ansi.CursorPosition(1, r)) {
			t.Errorf("diff must not CUP-write continuation row %d (splits the line): %q", r, diff)
		}
	}
}
