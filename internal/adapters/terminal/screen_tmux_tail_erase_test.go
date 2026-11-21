//go:build linux

package terminal

// Why the tail erase may not name a column past the last one, measured on a real
// pane.
//
// screen.go → tailErase aims the closing ED0 at the cell just after the frame's
// last base row. When that row fills the width there is no such cell: the "after"
// column is width+1. Whether that is harmless is a property of the HOST, not of
// the sequence — a host that clamps the out-of-range column puts the cursor ON the
// last cell, and the ED then erases it; a host that ignores the position leaves
// the cursor where it was and erases nothing. So this is measured the same way the
// soft-wrap promises are (screen_tmux_join_test.go): write the bytes into a pane
// and read the pane back.
//
// tmux 3.7c clamps, and this is the behavior the rule was written against: the
// status bar's right-aligned "R1" came back as "R" on a Windows console (conhost
// clamps too), while the same bytes erased nothing on xterm/VTE, which is why the
// bug was invisible on the machine the layout was developed on.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/testenv"
)

func TestTmuxErasesTheLastCellOnAColumnPastTheEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	tmux := testenv.RequireTool(t, "tmux", "the reference terminal for what a CUP past the last column does")
	const width, height = 20, 4
	row := strings.Repeat("A", width)

	for _, tc := range []struct {
		name string
		// tail is what follows the full-width row: the form that used to be
		// emitted, and the form tailErase emits now (nothing at all, because a
		// row that fills the width has no tail to clear).
		tail     string
		wantCell int // A's the pane keeps
	}{
		{"CUP to width+1, then ED0 (the shape that was wrong)", "\x1b[1;21H\x1b[J", width - 1},
		{"no erase at all (what a full-width row gets now)", "", width},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := replayInTmux(t, tmux, width, height, []byte(row+tc.tail), 0)
			got := 0
			if len(rows) > 0 {
				got = strings.Count(rows[0], "A")
			}
			if got != tc.wantCell {
				t.Errorf("pane kept %d of %d cells: %q\n"+
					"tmux 3.7c clamps an out-of-range column, so an ED aimed past the last cell erases it; a build that stops doing this is a reason to re-take the measurement, not to loosen the rule",
					got, width, rows)
			}
		})
	}
}
