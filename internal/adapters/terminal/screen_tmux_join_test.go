//go:build linux

package terminal

// What the soft-wrap contract is worth, measured on a real pane.
//
// The frame pads a row to the frame width and lets the terminal wrap it, so that
// a long line stays ONE logical line: a selection copies it without a newline per
// row, and the terminal's own continuation features (a URL split across rows)
// work (docs/tui.md → Line Wrapping). That promise depends on something no
// self-test of this package can check — what the terminal does with the bytes —
// so it is checked here, against tmux, the way the width table is
// (sanitize_test.go): by writing a stream into a pane and reading the pane back.
//
// `capture-pane -pJ` joins the rows a pane considers one wrapped line, so the two
// captures of one stream answer the question: is the line still one line?
//
//   - what the row diff actually writes for a changed row — CUP, an EL, the
//     row's text, an EL — is measured first, because that is the form the
//     streaming path uses on every delta. If a lone CUP write into a
//     continuation row split the line, the promise would hold only until the
//     first delta, and this package's own join tests (window_join_test.go) would
//     not notice: they check the STRING the frame is built from, not the pane.
//   - the successor row (the write-set closure in screen.go → diffFrameRows)
//     writes a row whose own text did not change. If that split the line above
//     it, the closure would cost the feature it is protecting.
//
// The last case is why the status row's layout reserves what a host may charge
// its own text (host_width.go), and it is the one measurement here of a HOST's behavior rather than
// the frame's: a cell written past the bottom row scrolls the pane, which moves
// every row — including the rows a diff will never rewrite.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/testenv"
)

func TestTmuxKeepsARepaintedRowJoined(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	tmux := testenv.RequireTool(t, "tmux", "the reference terminal for what this package's writes do to a wrapped line")
	const width, height = 8, 4
	row1 := strings.Repeat("A", 8)
	row2 := strings.Repeat("B", 8)
	// The line, written as one run: rows 1 and 2 are one logical line.
	line := row1 + row2

	for _, tc := range []struct {
		name  string
		bytes []byte
		// split is what the pane did with the wrapped line. Every form the
		// frame writes keeps it joined; the clear-then-write form does not,
		// which is why the row diff clears LAST (screen.go → diffFrameRows:
		// CUP, the row's text, then the EL) — the EL is what drops the
		// terminal's wrap flag for the row, so a row cleared first and written
		// second is a hard row break, and a copy of the line comes back with a
		// newline in the middle of it.
		split bool
	}{
		{"as written by a full render", []byte(line), false},
		{"rewritten alone as a row (CUP + text)", []byte(line + "\x1b[2;1H" + row2), false},
		{
			// The form the row diff writes for a changed row: CUP, the text, an
			// EL. The EL after the text lands on the pending-wrap cell, which
			// erases nothing and leaves the flag alone.
			name:  "rewritten alone as a row (CUP + text + EL)",
			bytes: []byte(line + "\x1b[2;1H" + row2 + "\x1b[K"),
		},
		{
			// The same write, cleared first. This is the contrast the form
			// above exists for.
			name:  "cleared first, then written (CUP + EL + text + EL)",
			bytes: []byte(line + "\x1b[2;1H\x1b[K" + row2 + "\x1b[K"),
			split: true,
		},
		{
			// The write-set closure's successor write: the row below a written
			// row, rewritten alone.
			name:  "the successor row rewritten alone",
			bytes: []byte(line + "\x1b[2;1H" + row2 + "\x1b[3;1Hthird line"),
		},
		{"the whole line rewritten from its first row", []byte(line + "\x1b[1;1H" + line), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := replayInTmux(t, tmux, width, height, tc.bytes, 0)
			joined := replayInTmuxJoined(t, tmux, width, height, tc.bytes)
			got := firstNonEmpty(joined)
			if tc.split {
				if got == line {
					t.Errorf("the pane kept the line joined after a clear-then-write:\n  -p  : %q\n  -pJ : %q\n"+
						"this is what the row diff's write form avoids (it clears after the text); a host that stops splitting it is a reason to re-take this measurement, and the form may then be simplified",
						rows, joined)
				}
				return
			}
			if got != line {
				t.Errorf("the pane split the wrapped line after this write:\n  -p  : %q\n  -pJ : %q\n  want the joined line %q",
					rows, joined, line)
			}
		})
	}
}

// TestTmuxScrollsOnACellPastTheBottomRow records why the status row reserves what
// a host may charge its own text (tui_status.go → renderStatusBar, host_width.go):
// a cell
// written past the bottom row scrolls the pane, and the rows that move are the
// ones a row diff will never rewrite. The expectation is what tmux 3.7c did when
// it was measured — a build that stops scrolling is a reason to re-take the
// slack, not to loosen this test.
func TestTmuxScrollsOnACellPastTheBottomRow(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	tmux := testenv.RequireTool(t, "tmux", "the reference terminal for what a write past the bottom row does")
	const width, height = 8, 4

	// Fill the pane, then write exactly the row width on the last row (no
	// scroll), then one cell more (scroll: the first line is gone).
	for _, tc := range []struct {
		name       string
		last       string
		wantScroll bool
	}{
		{"exactly the row width", strings.Repeat("Z", width), false},
		{"one cell past the row width", strings.Repeat("Z", width+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := replayInTmux(t, tmux, width, height,
				[]byte("one\r\ntwo\r\nthree\r\n"+tc.last), 0)
			lost := len(rows) > 0 && !containsLine(rows, "one")
			if lost != tc.wantScroll {
				t.Errorf("pane %q: top line lost = %v, want %v (tmux 3.7c: %v)",
					rows, lost, tc.wantScroll, tc.wantScroll)
			}
		})
	}
}

// firstNonEmpty returns the first non-empty captured line: `-pJ` joins the
// wrapped rows into one, after which the rest of the pane is blank.
func firstNonEmpty(lines []string) string {
	for _, l := range lines {
		if l != "" {
			return l
		}
	}
	return ""
}

// containsLine reports whether any captured line equals want.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
