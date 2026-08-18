package terminal

// Regression test for the "overlay closes, black block left behind"
// complaint: when an overlay (model selector, help window, ...) closes,
// the rows it covered must be repainted with the base content — the row
// diff must not just EL-erase the vanished overlay rows (that leaves a
// blank block until a manual Ctrl+R full repaint).
//
// Real TUI frames ALWAYS contain CUP rows (the input box and status bar
// are anchored with absolute CUP), so containsCUP is true for every
// frame — the "overlay vanished → full repaint" fallback never fires and
// the steady frame goes through the row diff, which must restore the
// base rows under a closed overlay itself.

import (
	"bytes"
	"strings"
	"testing"
)

// TestOverlayCloseRepaintsBaseRows builds two frames — base content plus
// an overlay covering rows 2-4 (both frames also carry the CUP-anchored
// input row), then the same base without the overlay — and asserts the
// rows under the overlay show the base content again.
func TestOverlayCloseRepaintsBaseRows(t *testing.T) {
	const W = 40
	base := "line0\nline1\nline2\nline3\nline4\nline5\n"
	// The CUP-anchored input box: present in EVERY frame, which keeps
	// containsCUP true and forces the row-diff path (no ED2 fallback).
	anchor := "\n\x1b[10;1HINPUT-BOX"
	overlay := "\x1b[3;1HOVERLAY-1\x1b[K\n\x1b[4;1HOVERLAY-2\x1b[K\n\x1b[5;1HOVERLAY-3\x1b[K"

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, 10)

	// Frame 1: base + overlay covering terminal rows 2,3,4 (+ anchor).
	if err := s.Render(base+overlay+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)
	for r := 2; r <= 4; r++ {
		if got := lineAt(grid, r); got != "OVERLAY-"+string(rune('0'+r-1)) {
			t.Fatalf("frame1 row %d = %q, want overlay content", r, got)
		}
	}

	// Frame 2: overlay closed — same base + anchor, no overlay rows.
	// Both frames contain CUP, so this is a steady row-diff frame.
	s.out.(*bytes.Buffer).Reset()
	if err := s.Render(base+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)

	// The rows under the overlay must show the base content again, not a
	// blank block.
	for r := 0; r < 6; r++ {
		want := "line" + string(rune('0'+r))
		if got := lineAt(grid, r); got != want {
			t.Errorf("row %d after overlay close = %q, want %q (base not repainted)", r, got, want)
		}
	}
	// And no overlay residue anywhere.
	for r := 0; r < 8; r++ {
		if strings.Contains(lineAt(grid, r), "OVERLAY") {
			t.Errorf("row %d trails overlay residue: %q", r, lineAt(grid, r))
		}
	}
	// The anchored input row must still be present.
	if got := lineAt(grid, 9); got != "INPUT-BOX" {
		t.Errorf("row 9 (anchored input) = %q, want INPUT-BOX", got)
	}
}

// TestOverlayCloseRepaintsWrappedBaseRow covers the soft-wrap case: the
// overlay hides the MIDDLE terminal row of a multi-row base fragment.
// Closing it must restore that exact terminal row's segment (the other
// rows of the fragment are still on screen and must not be disturbed).
func TestOverlayCloseRepaintsWrappedBaseRow(t *testing.T) {
	const W = 20
	// One logical base row 62 cells wide → wraps to 4 terminal rows
	// (20+20+20+2) at width 20: "0123456789" repeated.
	long := "0123456789" + "0123456789" + "0123456789" + "0123456789" + "0123456789" + "0123456789" + "01" // 62
	base := long + "\nline-after\n"
	anchor := "\n\x1b[10;1HINPUT-BOX"
	// Overlay covers terminal row 2 (the third segment of the fragment).
	overlay := "\x1b[3;1HOVERLAY\x1b[K"

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, 10)

	if err := s.Render(base+overlay+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)
	if got := lineAt(grid, 2); got != "OVERLAY" {
		t.Fatalf("frame1 row 2 = %q, want overlay", got)
	}

	s.out.(*bytes.Buffer).Reset()
	if err := s.Render(base+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)

	// The covered terminal row must show the fragment's third segment.
	want := long[40:60]
	if got := lineAt(grid, 2); got != want {
		t.Errorf("row 2 after overlay close = %q, want %q (fragment segment not restored)", got, want)
	}
	// Neighboring rows of the fragment must be intact.
	if got := lineAt(grid, 1); got != long[20:40] {
		t.Errorf("row 1 = %q, want %q", got, long[20:40])
	}
	if got := lineAt(grid, 3); got != long[60:62] {
		t.Errorf("row 3 = %q, want %q", got, long[60:62])
	}
	if got := lineAt(grid, 4); got != "line-after" {
		t.Errorf("row 4 = %q, want line-after", got)
	}
}
