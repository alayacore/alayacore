package terminal

import (
	"bytes"
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

// TestDiffRepaintsEveryOverlappingOverlayRun covers the case the diff's skip
// rule exists for: two CUP-anchored rows that each soft-wrap to more than one
// terminal row, starting on consecutive rows, so their spans overlap.
//
// A wrapped run is repainted as ONE continuous write — that is what keeps it a
// single logical line in the terminal, so a copy of it has no hard newline in
// the middle (TestOverlayCloseRestoresWrappedLine is the same rule for a base
// line). The write covers the cells of every row it spans, so the diff skips
// those rows; but a row it spans may be where the NEXT run starts, and that run
// is a different row with different text. Skipping it left it undrawn: the box
// showed a blank where its rule belonged, or kept the previous frame's tail,
// until a full repaint.
//
// The confirm dialog is what produces such a run in this app — its two
// description rows are emitted as one continuous write when the box spans the
// full width, so a long command copies without fake newlines
// (ConfirmDialog.RenderOverlay). Two overlays on screen at once (a selector
// with a confirm dialog over it) is how their spans come to overlap.
func TestDiffRepaintsEveryOverlappingOverlayRun(t *testing.T) {
	const width, height = 20, 8

	// Two overlay rows, each wider than the pane so each spans two terminal
	// rows: rows 3-4 and rows 4-5, overlapping on row 4.
	frame := func(mark string) string {
		var b strings.Builder
		for _, row := range []int{3, 4} {
			b.WriteString(ansi.CursorPosition(1, row+1))
			b.WriteString(strings.Repeat(mark, width+4))
		}
		return b.String()
	}
	before, after := frame("A"), frame("B")

	// Confirm the fixture is what the test claims: both rows wrap, and their
	// spans overlap. A fixture that stopped wrapping would silently stop
	// testing anything.
	rows := positionedRows(after, width)
	if len(rows) != 2 {
		t.Fatalf("fixture: got %d overlay rows, want 2", len(rows))
	}
	for i, r := range rows {
		if r.base || r.terminalRows != 2 {
			t.Fatalf("fixture: row %d base=%v spans %d terminal rows, want an overlay spanning 2",
				i, r.base, r.terminalRows)
		}
	}
	if rows[0].frameRow.row+1 != rows[1].frameRow.row {
		t.Fatalf("fixture: the runs do not start on consecutive rows (%d, %d), so they cannot overlap",
			rows[0].frameRow.row, rows[1].frameRow.row)
	}

	// Paint the first frame, then the second through the same Screen, so the
	// second goes down the diff path.
	var sink bytes.Buffer
	scr := &Screen{out: &sink, width: width, height: height}
	grid := newGrid(width, height)
	if err := scr.Render(before, nil, true); err != nil {
		t.Fatal(err)
	}
	grid.write(sink.Bytes())
	sink.Reset()
	if err := scr.Render(after, nil, true); err != nil {
		t.Fatal(err)
	}
	diff := sink.Bytes()
	grid.write(diff)

	// Both runs must have been written: one CUP each, at their start rows.
	for _, row := range []int{3, 4} {
		if !bytes.Contains(diff, []byte(ansi.CursorPosition(1, row+1))) {
			t.Errorf("the diff never positioned on row %d, so the run starting there was not painted: %q",
				row, diff)
		}
	}

	// And the screen must be the one a full repaint of the same frame leaves.
	var full bytes.Buffer
	fresh := &Screen{out: &full, width: width, height: height}
	if err := fresh.Render(after, nil, true); err != nil {
		t.Fatal(err)
	}
	want := newGrid(width, height)
	want.write(full.Bytes())
	if d := gridDiff(grid, want); len(d) > 0 {
		t.Errorf("the diff left a different screen than a full repaint:\n%s", strings.Join(d, "\n"))
	}
}
