package terminal

// Regression test for resize residue: after a resize, the first
// full-screen render must clear the screen. The terminal still shows
// the pre-resize frame (rows reflowed/cut at the new size), so an
// ED2-less repaint leaves the old bottom rows / row tails behind.
// Screen.Resize resets the fill-mode flags so the next render takes
// the clear-first (ED2) path.

import (
	"bytes"
	"testing"
)

func TestResizeClearsBeforeRepaint(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	s.Resize(60, 8)

	// Frame 1: full-screen, 8 rows.
	frame1 := "row0\nrow1\nrow2\nrow3\nrow4\nrow5\nrow6\nrow7"
	if err := s.Render(frame1, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, buf.String(), 60)
	buf.Reset()

	// Terminal shrinks to 6 rows; the screen still shows rows 0-7
	// (terminals keep the buffer, just hide the bottom).
	s.Resize(60, 6)

	// Frame 2: full-screen, 6 rows (shorter than frame 1).
	frame2 := "row0\nrow1\nrow2\nrow3\nrow4\nrow5"
	if err := s.Render(frame2, nil, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	t.Logf("frame2 output: %q", out)
	grid = applyFrame(grid, out, 60)

	for r := 0; r < 6; r++ {
		want := "row" + string(rune('0'+r))
		if got := lineAt(grid, r); got != want {
			t.Errorf("row %d = %q, want %q", r, got, want)
		}
	}
	// Rows 6-7 must not survive from frame 1.
	if got := lineAt(grid, 6); got != "" {
		t.Errorf("row 6 after resize = %q, want empty (residue from pre-resize frame)", got)
	}
}
