package terminal

// Tests for the sticky window line.
//
// While a window taller than the viewport is scrolled through, its own line
// (marker + label + timestamp) stays pinned to screen row 0 for as long as
// any of its body is still visible below. The pinned row DISPLACES the body
// row that would have been at the top of the screen, so the frame still
// spends exactly viewportHeight rows and the document geometry
// (lineHeights/totalLines) is not involved at all — the two properties this
// file exists to keep true: the displaced row, and "the pin is a
// screen-space composite, not a document row".
//
// The cost of the pin is part of its contract, not an implementation detail:
// it must be a cached-row read, so TestStickyPinAddsNoAllocations fails if
// anyone rebuilds, re-measures or copies the row per frame.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// stickyFixture returns a width-40 buffer holding one tall expanded window
// whose ten body rows are individually identifiable ("row00" … "row09"),
// plus a folded window after it. Ten body rows at viewport height 4 give
// room for every boundary case below.
func stickyFixture() *WindowBuffer {
	wb := NewWindowBuffer(40, DefaultStyles())
	var body strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&body, "row%02d\n", i)
	}
	wb.AppendOrUpdate(tlv.TagAssistantT, "tall", strings.TrimSuffix(body.String(), "\n"))
	wb.AppendOrUpdate(tlv.TagUserT, "after", "next message")
	return wb
}

// stickyRows renders the buffer at the given scroll offset and viewport
// height and returns the plain rows of the fragment.
func stickyRows(t *testing.T, wb *WindowBuffer, yOffset, height int) []string {
	t.Helper()
	wb.SetViewportPosition(yOffset, height)
	return strings.Split(stripANSI(wb.GetAll(-1, false)), "\n")
}

// windowLine is the tall window's own line as the fixture renders it.
func windowLine(t *testing.T, wb *WindowBuffer) string {
	t.Helper()
	wb.SetViewportPosition(0, 1) // row 0 of the document, unpinned
	row := firstRow(wb.GetAll(-1, false))
	if !strings.HasPrefix(stripANSI(row), unfoldArrow+" ASSISTANT") {
		t.Fatalf("fixture: row 0 = %q, want the window's own line", stripANSI(row))
	}
	return row
}

// TestStickyPinDisplacesTheTopBodyRow is the core of the behavior: with the
// window's own line scrolled off, it is drawn at screen row 0 and the body
// row that would have been there is dropped — the reader loses one row of
// content, not one row of frame.
func TestStickyPinDisplacesTheTopBodyRow(t *testing.T) {
	wb := stickyFixture()
	own := windowLine(t, wb)

	// Offset 0: the line is in its natural place, body starts at row00.
	rows := stickyRows(t, wb, 0, 4)
	want := []string{stripANSI(own), "row00", "row01", "row02"}
	if got := strings.Join(rows, "|"); got != strings.Join(want, "|") {
		t.Errorf("unpinned rows:\n  got:  %q\n  want: %q", rows, want)
	}

	// Offset 1: the line would be gone; instead it is pinned and row00 —
	// the row that would have been at screen row 0 — is displaced.
	rows = stickyRows(t, wb, 1, 4)
	want = []string{stripANSI(own), "row01", "row02", "row03"}
	if got := strings.Join(rows, "|"); got != strings.Join(want, "|") {
		t.Errorf("pinned rows:\n  got:  %q\n  want: %q", rows, want)
	}
	if strings.Contains(strings.Join(rows, "|"), "row00") {
		t.Errorf("the displaced row must not be on screen twice: %q", rows)
	}

	// The pinned row is followed by a hard newline, never soft-joined into
	// the body row beneath it (those rows can be continuations of one long
	// line, and a full-width row would then swallow one).
	if got := stripANSI(wb.GetAll(-1, false)); !strings.HasPrefix(got, stripANSI(own)+"\n") {
		t.Errorf("the pinned line must end its own row: %q", got)
	}
}

// TestStickyPinEntersAndLeavesAtTheRightBoundaries pins the transitions: the
// pin appears as soon as the line is cut off and disappears when the window's
// last row would be the only thing left above the body (never an orphan
// header with nothing under it).
func TestStickyPinEntersAndLeavesAtTheRightBoundaries(t *testing.T) {
	wb := stickyFixture()
	own := stripANSI(windowLine(t, wb))
	const h = 3

	// The document: [0] window line, [1..10] row00..row09, [11] the expanded
	// user prompt's line, [12] its content.
	cases := []struct {
		yOffset   int
		wantFirst string
		why       string
	}{
		{0, own, "the line is in its natural place"},
		{1, own, "the line is cut off: pinned"},
		{9, own, "still pinned: body rows remain below it"},
		{10, "row09", "the line's last row is on screen: no pin (never an orphan header)"},
		{11, unfoldArrow + " USER PROMPT", "the window is entirely above the viewport: the next window's line"},
	}
	for _, tc := range cases {
		rows := stickyRows(t, wb, tc.yOffset, h)
		if len(rows) != h {
			t.Errorf("y=%d: %d rows, want %d (the frame height)", tc.yOffset, len(rows), h)
		}
		if !strings.HasPrefix(rows[0], tc.wantFirst) {
			t.Errorf("y=%d: row 0 = %q, want it to start with %q — %s", tc.yOffset, rows[0], tc.wantFirst, tc.why)
		}
		if strings.Contains(strings.Join(rows, "|"), own) && tc.yOffset >= 11 {
			t.Errorf("y=%d: the pinned line must be gone once the window scrolled past: %q", tc.yOffset, rows)
		}
	}
}

// TestStickyPinIsScreenSpaceOnly: rendering with the pin must not touch the
// document geometry. lineHeights drives viewport clipping, scroll clamping
// and every cursor-position calculation; a pin that changed it would make
// all of those depend on the scroll position.
func TestStickyPinIsScreenSpaceOnly(t *testing.T) {
	wb := stickyFixture()
	wb.SetViewportPosition(0, 4)
	_ = wb.GetAll(-1, false)

	heights := append([]int(nil), wb.lineHeights...)
	total := wb.GetTotalLines()
	start, end := wb.GetWindowLineRange(0)

	for _, y := range []int{1, 2, 5, 9, 10, 11, 20} {
		wb.SetViewportPosition(y, 4)
		_ = wb.GetAll(-1, false)
		if got := wb.lineHeights; strings.Trim(fmt.Sprint(got), "[]") != strings.Trim(fmt.Sprint(heights), "[]") {
			t.Fatalf("y=%d: lineHeights changed: %v → %v", y, heights, got)
		}
		if got := wb.GetTotalLines(); got != total {
			t.Fatalf("y=%d: totalLines = %d, want %d", y, got, total)
		}
		if s, e := wb.GetWindowLineRange(0); s != start || e != end {
			t.Fatalf("y=%d: window 0 range = [%d,%d), want [%d,%d)", y, s, e, start, end)
		}
	}
}

// TestStickyPinAddsNoAllocations is the cost contract in test form: the pin
// is a read of a row the same frame already built plus one write into the
// fragment, so it must allocate exactly as much as the same viewport with
// the line in its natural place. A per-frame rebuild (Style.Render), a
// re-measure (cellWidth) or a copied row slice would all show up here.
func TestStickyPinAddsNoAllocations(t *testing.T) {
	wb := stickyFixture()
	render := func(y int) func() {
		return func() {
			wb.SetViewportPosition(y, 4)
			_ = wb.GetAll(-1, false)
		}
	}
	// Warm every cache first: the first render of a window is not the frame
	// this contract is about.
	render(0)()
	render(1)()

	plain := testing.AllocsPerRun(50, render(0)) // the line in its natural place
	pinned := testing.AllocsPerRun(50, render(1))
	if pinned > plain {
		t.Errorf("the pin allocated %v per frame, the same viewport without it %v — the pin must add nothing", pinned, plain)
	}
}

// TestStickyPinFollowsTheCursor: the pinned row is the window's own line, so
// when the cursor is on that window it is the highlighted variant — the
// selected window's line stays visible while its body scrolls.
func TestStickyPinFollowsTheCursor(t *testing.T) {
	styles := DefaultStyles()
	wb := stickyFixture()
	wb.SetViewportPosition(1, 4)
	pinCreatedAt(wb)

	cursorRow := firstRow(wb.GetAll(0, false))
	if !strings.HasPrefix(cursorRow, cursorLineStyle(styles).Render(unfoldArrow)+" ") {
		t.Errorf("with the cursor on the scrolled window its pinned row should be highlighted: %q", cursorRow)
	}
	// …and only then: with the cursor elsewhere the pinned row is the plain
	// one.
	other, _ := wb.LookupID("after")
	plainRow := firstRow(wb.GetAll(other, false))
	if plainRow != firstRow(wb.GetAll(-1, false)) {
		t.Errorf("the pinned row must not be highlighted when the cursor is on another window: %q", plainRow)
	}
}

// TestStickyPinUnderAutoFollow: while auto-following a long streamed answer
// the viewport sits at the bottom of it, so the pin is active for the whole
// generation — the newest content stays visible at the bottom, and the
// window's name stays visible at the top.
func TestStickyPinUnderAutoFollow(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.TrimSuffix(strings.Repeat("streamed answer line\n", 6), "\n"))
	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(4).updateContent()

	if !dm.shouldFollow() {
		t.Fatal("fixture: a fresh display follows")
	}
	rows := strings.Split(stripANSI(dm.View().Content), "\n")
	if !strings.HasPrefix(rows[0], unfoldArrow+" ASSISTANT") {
		t.Errorf("auto-followed stream should keep the window's line pinned: %q", rows[0])
	}
	if got := strings.TrimRight(rows[len(rows)-1], " "); got != "streamed answer line" {
		t.Errorf("the newest line must stay visible at the bottom, got %q", got)
	}
	// A delta that widens the answer keeps both properties.
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "\nand one more line")
	dm = dm.updateContent()
	rows = strings.Split(stripANSI(dm.View().Content), "\n")
	if !strings.HasPrefix(rows[0], unfoldArrow+" ASSISTANT") {
		t.Errorf("after a delta the line should still be pinned: %q", rows[0])
	}
	if got := strings.TrimRight(rows[len(rows)-1], " "); got != "and one more line" {
		t.Errorf("the newest line must be visible after the delta, got %q", got)
	}
}
