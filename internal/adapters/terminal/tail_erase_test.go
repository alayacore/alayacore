package terminal

// The frame writes two erases after rows it has drawn, and both used to land on a
// cell the frame had just filled.
//
//   - the tail erase, `CUP(last row, width+1)` + ED0, which named a column that
//     does not exist;
//   - the EL that ends a written row, which after a row that fills the width falls
//     on that row's own last cell.
//
// Neither is a no-op on the hosts that matter. A host that clamps the
// out-of-range column — conhost does, and tmux does
// (screen_tmux_tail_erase_test.go) — puts the cursor ON the last column and then
// erases it; a host that keeps the cursor there after a full row erases the
// character the row ended with, while one that moves it past the end erases
// nothing. The status row is what made it visible: its right-hand group is
// right-aligned flush to the edge (tui_status.go → assembleStatusLeft), so the row
// is exactly `width` cells wide — and the prompt box's bottom rule, which is
// `strings.Repeat("─", width)`, lost its last cell the same way while the top rule
// (no EL after it) kept its own.
//
// The tests below are the pair that keeps the shape: the cell an erase names
// exists, and a row that fills the width keeps what it drew.

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// fullWidthRow returns a row of exactly `width` cells ending in last.
func fullWidthRow(width int, last string) string {
	fill := width - cellWidth(last)
	if fill < 0 {
		fill = 0
	}
	return strings.Repeat("s", fill) + last
}

// cupEmissions returns every CUP the frame wrote, as (row, col) 1-based pairs.
func cupEmissions(t *testing.T, frame string) [][2]int {
	t.Helper()
	var out [][2]int
	for i := 0; i+1 < len(frame); i++ {
		if frame[i] != 0x1b || frame[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(frame) && (frame[j] == ';' || (frame[j] >= '0' && frame[j] <= '9')) {
			j++
		}
		if j < len(frame) && frame[j] == 'H' {
			if row, col, ok := parseCUP(frame[i+1 : j+1]); ok {
				out = append(out, [2]int{row + 1, col + 1})
			}
			i = j
		}
	}
	return out
}

// TestTailEraseNeverPositionsPastTheLastCell is the invariant, asserted on the
// bytes rather than on the shape of the code: no steady frame may address a
// column past the terminal's width or a row past its height, because the hosts
// that clamp such a position act on the cell they land on. Every case below is a
// frame whose last base row is exactly the width — the case that used to emit
// `width+1` — in both shapes needTailErase knows: the text changed, and the frame
// shrank.
func TestTailEraseNeverPositionsPastTheLastCell(t *testing.T) {
	for _, width := range []int{9, 20, 40, 120} {
		for _, height := range []int{6, 24} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				var buf bytes.Buffer
				s := &Screen{out: &buf}
				s.Resize(width, height)

				// Every frame below ends on a row that is exactly `width`
				// cells, which is the shape that used to aim the erase at
				// `width+1`. Three of them, because needTailErase has three
				// ways to fire: the first render (no erase — there is nothing
				// behind it), the last row's text changing, and the frame
				// shrinking so the rows below must be cleared.
				frames := []string{
					"body\n" + fullWidthRow(width, "R1"), // first frame
					"body\n" + fullWidthRow(width, "R2"), // text changed
					// Grown by one row, so the next frame shrinks back onto a
					// full-width last row: the erase then has to start on the
					// row below it.
					"body\n" + fullWidthRow(width, "R2") + "\n" + strings.Repeat("t", width),
					fullWidthRow(width, "R3"),
				}

				for i, f := range frames {
					buf.Reset()
					if err := s.Render(f, nil, true); err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					for _, c := range cupEmissions(t, buf.String()) {
						if c[1] > width {
							t.Errorf("frame %d emitted CUP to column %d on a %d-cell terminal; a host that clamps it erases the cell it lands on:\n  %q",
								i, c[1], width, buf.String())
						}
						if c[0] > height {
							t.Errorf("frame %d emitted CUP to row %d on a %d-row terminal:\n  %q",
								i, c[0], height, buf.String())
						}
					}
				}
			})
		}
	}
}

// TestTailEraseLeavesAFullWidthLastRowIntact is the consequence, read off the
// terminal model: the last row is exactly the width, it is repainted with one
// character different (which is what a streaming status bar does on every tick),
// and its last cell must still hold what the frame drew there. With the erase
// aimed one column past the end, this row comes back one character short.
func TestTailEraseLeavesAFullWidthLastRowIntact(t *testing.T) {
	const width, height = 24, 6
	row1 := fullWidthRow(width, "R1")
	row2 := fullWidthRow(width, "R2")

	var buf bytes.Buffer
	s := &Screen{out: &buf}
	s.Resize(width, height)

	if err := s.Render("body\n"+row1, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, buf.String(), width)

	buf.Reset()
	if err := s.Render("body\n"+row2, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, buf.String(), width)

	got := strings.TrimRight(string(grid[1]), " ")
	if got != row2 {
		t.Errorf("last row after its own repaint = %q, want %q\n"+
			"a character of it is gone: the tail erase addressed a cell past the last column, and this host model clamps like conhost does",
			got, row2)
	}
}

// TestTailEraseStartsOnTheTailWhenThereIsOne pins the other half: a last row
// SHORTER than the width still gets its tail wiped, from the first cell after its
// text — the erase is not skipped for every steady frame, only aimed elsewhere
// when there is nothing on the row left to clear.
func TestTailEraseStartsOnTheTailWhenThereIsOne(t *testing.T) {
	const width, height = 20, 6
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	s.Resize(width, height)

	if err := s.Render("body\nshort row here", nil, true); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := s.Render("body\nshorter row", nil, true); err != nil {
		t.Fatal(err)
	}
	cups := cupEmissions(t, buf.String())
	if len(cups) == 0 {
		t.Fatalf("a shorter last row emitted no CUP at all:\n  %q", buf.String())
	}
	want := [2]int{2, cellWidth("shorter row") + 1}
	if cups[len(cups)-1] != want {
		t.Errorf("tail erase started at %v, want %v (the cell after the row's text):\n  %q",
			cups[len(cups)-1], want, buf.String())
	}
}

// TestFillsLastColumn pins the rule both erases share: a write that ends on the
// last column leaves the cursor there, so the next erase would take a cell the
// frame drew.
func TestFillsLastColumn(t *testing.T) {
	for _, tc := range []struct {
		cells, width int
		want         bool
		why          string
	}{
		{0, 80, false, "an empty row is cleared from its first column"},
		{1, 80, false, "one cell leaves a tail"},
		{79, 80, false, "one cell short leaves a tail"},
		{80, 80, true, "exactly full: the EL would land on the last cell"},
		{160, 80, true, "a soft-wrap run whose last row is full"},
		{81, 80, false, "the wrap moved it: the last row has a tail"},
		{5, 0, false, "unknown width: there is no boundary to be past"},
	} {
		if got := fillsLastColumn(tc.cells, tc.width); got != tc.want {
			t.Errorf("fillsLastColumn(%d, %d) = %v, want %v (%s)",
				tc.cells, tc.width, got, tc.want, tc.why)
		}
	}
}

// TestEraseTailAfterMeasuresOnlyTheLastRow: the erase follows a written block, so
// the row it would land on is the block's LAST one — a short box whose last rule
// fills the width gets no EL, whatever its other rows measure.
func TestEraseTailAfterMeasuresOnlyTheLastRow(t *testing.T) {
	const width = 20
	full := strings.Repeat("─", width)

	if got := eraseTailAfter("short row", width); got != "\x1b[K" {
		t.Errorf("eraseTailAfter on a short row = %q, want an EL", got)
	}
	if got := eraseTailAfter(full, width); got != "" {
		t.Errorf("eraseTailAfter on a full row = %q, want nothing", got)
	}
	if got := eraseTailAfter("short row\n"+full, width); got != "" {
		t.Errorf("eraseTailAfter with a full LAST row = %q, want nothing", got)
	}
	if got := eraseTailAfter(full+"\nshort", width); got != "\x1b[K" {
		t.Errorf("eraseTailAfter with a short LAST row = %q, want an EL", got)
	}
}

// TestTheFrameKeepsEveryFullRowsLastCharacter is the consequence, on the frame the
// real builders produce: paint a 120-column view into the terminal model and every
// row that is exactly the terminal width must still hold the character it ended
// with. Before the rule, the status row's trailing "R1" came back as "R" and the
// prompt box's bottom rule lost its last "─" — the two rows a Windows console
// showed a blank column in.
func TestTheFrameKeepsEveryFullRowsLastCharacter(t *testing.T) {
	const width, height = 120, 24
	m := testTUISized(width)
	// A session's status right group: the model name is what pushes the row out
	// to the last column, which is where the character that went missing was.
	m.statusLeft = "2.8K/1M 0.3% | 242.6 tok/s (ttft 0.3s) | 4/INF"
	m.statusRight = "DeepSeek / DeepSeek-Flash | R1"
	content := m.View().Content

	// The host's own newline handling, as Screen.Render does for a full frame.
	grid := applyFrame(nil, strings.ReplaceAll(content, "\n", "\r\n"), width)

	checked := 0
	for _, r := range positionedRows(content, width) {
		if r.cells != width || r.frameRow.row >= len(grid) {
			continue
		}
		plain := stripANSI(r.frameRow.text)
		runes := []rune(plain)
		if len(runes) == 0 {
			continue
		}
		want := runes[len(runes)-1]
		if want == ' ' {
			continue // a padded row: nothing at its end to lose
		}
		if got := grid[r.frameRow.row][width-1]; got != want {
			t.Errorf("row %d is %d cells wide and ends with %q, but the last column holds %q after painting:\n  %q",
				r.frameRow.row, r.cells, string(want), string(got), plain)
		}
		checked++
	}
	if checked == 0 {
		t.Fatalf("no row of the %dx%d frame fills the width, so this test checks nothing", width, height)
	}
}
