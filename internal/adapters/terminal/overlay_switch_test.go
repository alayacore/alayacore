package terminal

// Regression test for overlay switching: one overlay closes while a
// different one opens in the same rows but at a different column
// (centered boxes with different widths). The vanished-row cleanup must
// run BEFORE the changed-row repaint — otherwise its EL (from the old
// column to end of line) erases the new overlay row, leaving base
// content where the new box should be.

import (
	"bytes"
	"strings"
	"testing"
)

func TestOverlaySwitchDifferentColumn(t *testing.T) {
	const W = 60
	base := "line0\nline1\nline2\nline3\nline4\n"
	// Frame 1: narrow overlay box centered at col 15, row 2.
	frame1 := base + "\n\x1b[5;1HINPUT\n\x1b[3;16HOLD-BOX"
	// Frame 2: wider overlay box centered at col 20, same row 2.
	frame2 := base + "\n\x1b[5;1HINPUT\n\x1b[3;21HNEW-BOX"

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, 8)

	if err := s.Render(frame1, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)

	s.out.(*bytes.Buffer).Reset()
	if err := s.Render(frame2, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)

	got := lineAt(grid, 2)
	// The new overlay row must be visible (the vanished-row cleanup used to
	// erase it); the base content remains visible left of the centered box.
	if !strings.Contains(got, "NEW-BOX") {
		t.Errorf("row 2 after overlay switch = %q, want it to contain %q (new overlay erased by vanished-row cleanup)", got, "NEW-BOX")
	}
	if !strings.HasPrefix(got, "line2") {
		t.Errorf("row 2 after overlay switch = %q, want base content left of the box", got)
	}
}
