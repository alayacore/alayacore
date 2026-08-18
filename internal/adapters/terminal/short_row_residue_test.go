package terminal

// Regression test for short-row residue: CUP-anchored rows (status
// bar, unfocused input field) are shorter than the terminal width and
// do not pad to it or emit EL themselves. When the row diff repaints
// such a row after its text shrinks (status segment dropped, focus
// change), the old frame's tail must be erased — the diff appends an
// EL after every repainted overlay row.

import (
	"bytes"
	"testing"
)

func TestStatusBarShrinkNoResidue(t *testing.T) {
	const W = 60
	base := "line0\nline1\nline2\n"
	// Frame 1: status bar with a trailing segment ("| F↓") — long.
	frame1 := base + "\n\x1b[5;1HINPUT\x1b[K\n\x1b[6;1H• 1.5K/8K 18.7% | F↓"
	// Frame 2: status bar without the segment — shorter.
	frame2 := base + "\n\x1b[5;1HINPUT\x1b[K\n\x1b[6;1H• 2.1K/8K 26.2%"

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, 8)

	if err := s.Render(frame1, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)
	t.Logf("frame1 row5: %q", lineAt(grid, 5))

	s.out.(*bytes.Buffer).Reset()
	if err := s.Render(frame2, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)
	t.Logf("frame2 row5: %q", lineAt(grid, 5))

	want := "• 2.1K/8K 26.2%"
	if got := lineAt(grid, 5); got != want {
		t.Errorf("status row after shrink = %q, want %q (residue: %q)", got, want, got[len(want):])
	}
}

func TestUnfocusedInputShrinkNoResidue(t *testing.T) {
	const W = 60
	base := "line0\nline1\nline2\n"
	// Frame 1: focused input row — padded to full width (long).
	frame1 := base + "\n\x1b[5;1H❯ this is a long input value                   " + "\n\x1b[6;1H• status"
	// Frame 2: unfocused input row — short, no padding.
	frame2 := base + "\n\x1b[5;1H❯ hi" + "\n\x1b[6;1H• status"

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
	t.Logf("frame2 row4: %q", lineAt(grid, 4))

	want := "❯ hi"
	if got := lineAt(grid, 4); got != want {
		t.Errorf("input row after shrink = %q, want %q (residue: %q)", got, want, got[len(want):])
	}
}
