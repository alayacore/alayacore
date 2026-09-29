package terminal

// A row carries the padding a frame appends after it, counted by the pass that
// broke it. renderVirtual trusts that number where it pads a row for the terminal
// to soft-wrap, and a row that asks for the wrong number of spaces draws the break
// in the wrong place: the terminal wraps where the padding put it, not where the
// row ends, and every row after that one is a terminal row out.
//
// So the number is checked against a measurement of the text it describes, at
// every point a row is made. The measurement is the oracle and it is deliberately
// the expensive one — cellWidth strips a styled row's escapes and walks what is
// left, which is exactly what a frame used to do per padded row per frame. Paying
// it here is the point of not paying it there.
//
// The rule being checked is the one the frame used to compute for itself:
//
//	pad == max(0, width - cellWidth(text))   for a row a continuation follows
//	pad == 0                                 for every other row
//
// Stated that way it also pins the assumption Pad makes — that a row was wrapped
// at the width the frame renders at. A row wrapped narrower would satisfy neither
// branch.

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"unsafe"

	"github.com/alayacore/alayacore/internal/protocol"
)

// rowPadCorpus is what a row can be made of: plain ASCII, text that wraps and text
// that does not, wide characters whose clusters are two cells, a cluster wider than
// the narrowest widths below, combining marks, tabs, empty lines, and styled text
// whose escapes must charge nothing.
var rowPadCorpus = []string{
	"",
	"a",
	"abcdefghij",
	"content that is long enough to wrap at any of the widths under test",
	strings.Repeat("x", 300),
	"你你你你你你你你你你你你",
	"a你b好c世d界e",
	"e\u0301e\u0301e\u0301",
	"❤️❤️x",
	"👨\u200d👩\u200d👧\u200d👦ab",
	"1️⃣2️⃣3️⃣",
	"one\ttwo\tthree",
	"\t\tindented",
	"line\nline\nline",
	"\n\n",
	"trailing\n",
	DefaultStyles().Body.Render("styled body text that is long enough to wrap at a narrow width"),
	"\x1b[31mred\x1b[0m and \x1b[1mbold\x1b[0m and plain, long enough to wrap too",
}

// rowPadWidths are the widths rows are built at. The narrow ones matter most: a
// width of 1 or 2 is where a cluster wider than the row gets a line to itself, and
// where an off-by-one in the count moves a break by a whole row.
var rowPadWidths = []int{1, 2, 3, 5, 8, 13, 40, 80}

// wantPad is the frame's old arithmetic, restated: the spaces that bring a row up
// to width, and none for a row already at or past it.
func wantPad(text string, width int) int {
	return max(0, width-cellWidth(text))
}

// checkPaddedRows requires every row to ask for the padding the frame would have
// measured for it, and every row no continuation follows to ask for none.
func checkPaddedRows(t *testing.T, where string, rows []visualLine, width int) {
	t.Helper()
	for i, vl := range rows {
		followed := i+1 < len(rows) && rows[i+1].Cont
		want := 0
		if followed {
			want = wantPad(vl.Text, width)
		}
		if got := int(vl.Pad); got != want {
			t.Errorf("%s: row %d asks for %d spaces, want %d (followed by a continuation: %v): %q",
				where, i, got, want, followed, vl.Text)
		}
	}
}

// TestRowsPadToTheWidthTheyDraw is the check on the wrap, which is where the bulk
// of rows come from and the only place padding is counted rather than measured.
func TestRowsPadToTheWidthTheyDraw(t *testing.T) {
	checked := 0
	for _, width := range rowPadWidths {
		for _, s := range rowPadCorpus {
			rows := wrapVisualLines(s, width)
			checkPaddedRows(t, fmt.Sprintf("wrapVisualLines(width=%d, %q)", width, s), rows, width)
			checked += len(rows)
		}
	}
	// Without this the test can pass by producing no rows at all, which is what an
	// empty corpus or a width guard that skips everything would look like.
	if checked < 1000 {
		t.Fatalf("only %d rows were checked; the sweep is not exercising what it claims", checked)
	}
	t.Logf("checked %d rows", checked)
}

// TestHardwrapWidthsMatchTheRowsItBroke pins the two halves of the wrap to each
// other: hardwrapCellsWidths must break exactly where hardwrapCells does, and
// report one width per row it made. The break rule lives in hardwrapWalk and is
// shared, but "is shared" is a claim about the code and this is a claim about the
// output.
func TestHardwrapWidthsMatchTheRowsItBroke(t *testing.T) {
	for _, width := range rowPadWidths {
		for _, s := range rowPadCorpus {
			joined, cells := hardwrapCellsWidths(s, width, nil)
			if want := hardwrapCells(s, width); joined != want {
				t.Fatalf("width %d, %q: hardwrapCellsWidths broke differently from hardwrapCells:\n got %q\nwant %q",
					width, s, joined, want)
			}
			rows := strings.Split(joined, "\n")
			if len(cells) != len(rows) {
				t.Fatalf("width %d, %q: %d widths for %d rows", width, s, len(cells), len(rows))
			}
			for i, r := range rows {
				if got, want := cells[i], cellWidth(r); got != want {
					t.Errorf("width %d, %q: row %d reports %d cells but measures %d: %q",
						width, s, i, got, want, r)
				}
			}
		}
	}
}

// TestWrapRowsMatchesWrapContent is the same claim one level up: the rows wrapRows
// returns, rejoined, are the string wrapContent returns — so a caller that switches
// from one to the other changes what it knows and not what it draws.
func TestWrapRowsMatchesWrapContent(t *testing.T) {
	for _, width := range rowPadWidths {
		for _, s := range rowPadCorpus {
			rows, cells := wrapRows(s, width, nil)
			if len(cells) != len(rows) {
				t.Fatalf("width %d, %q: %d widths for %d rows", width, s, len(cells), len(rows))
			}
			if got, want := strings.Join(rows, "\n"), wrapContent(s, width); got != want {
				t.Errorf("width %d, %q: wrapRows and wrapContent disagree:\n got %q\nwant %q",
					width, s, got, want)
			}
			for i, r := range rows {
				if got, want := cells[i], cellWidth(r); got != want {
					t.Errorf("width %d, %q: row %d reports %d cells but measures %d: %q",
						width, s, i, got, want, r)
				}
			}
		}
	}
}

// TestWindowRowsPadToTheBufferWidth is the check on what a frame actually reads:
// every row of every window, in both fold states, under both style registers, at
// several widths — including row 0 after windowFragment swaps it for the cursor's
// register, and including the dimmed register, where a row's text is a recolored
// copy that carries its padding across rather than earning it again.
func TestWindowRowsPadToTheBufferWidth(t *testing.T) {
	long := "content that is long enough to wrap at any of the widths below "
	padded := 0
	for _, width := range []int{12, 13, 24, 25, 40, 41, 80} {
		for _, blocked := range []bool{false, true} {
			styles := DefaultStyles()
			if blocked {
				styles = styles.Dimmed()
			}
			wb := NewWindowBuffer(width, styles)
			wb.AppendOrUpdate("AT", "text", strings.Repeat(long, 6))
			wb.AppendOrUpdate("UT", "user", strings.Repeat(long, 6))
			// Wide content, because an ASCII row that a hard wrap broke is exactly
			// the width and so asks for no padding at all: the count is non-zero
			// only where a cluster is wider than the space left at the break. A
			// sweep without this checks the rule on rows that never exercise it.
			wb.AppendOrUpdate("AT", "wide", strings.Repeat("你好世界中文", 20))
			wb.HandleToolInputEvent(protocol.ToolInputData{
				ID: "t1", Name: "edit_file", Input: []byte(strings.Repeat(long+"\n", 6)),
			}, 1)

			for _, folded := range []bool{false, true} {
				for i := range wb.windows {
					w := wb.WindowAt(i)
					w.Folded = folded
					w.Invalidate()
					w.buildLines(width, styles, blocked)
					where := fmt.Sprintf("built rows (width=%d blocked=%v folded=%v window=%d)", width, blocked, folded, i)
					checkPaddedRows(t, where, w.cache.lines, width)
					padded += countPadded(w.cache.lines)

					// The cursor's register: row 0 replaced by a recolored copy.
					frag := wb.windowFragment(w, 0, len(w.cache.lines), true, blocked)
					checkPaddedRows(t, "cursor "+where, frag, width)
					if want := w.cursorLine0(); frag[0].Text != want {
						t.Errorf("%s: the fragment's row 0 is not the cursor register's row:\n got  %q\n want %q", where, frag[0].Text, want)
					}
				}
			}
		}
	}
	// A sweep over windows that never soft-wrap would check the padding of nothing.
	if padded == 0 {
		t.Fatal("no row in the sweep asked for padding, so the rule was never exercised")
	}
	t.Logf("%d rows asked for padding", padded)
}

// TestDeltaRowsPadToTheWidthTheyDraw covers the streaming path, where rows are not
// rebuilt but appended to and the last one re-wrapped — so a row's successor
// changes underneath it, which is the one way the "followed by a continuation"
// half of the rule could come apart from the padding counted when the row was made.
// Width 0 and negative are here too: that branch grows a row's text in place, the
// only place a row is edited rather than replaced.
func TestDeltaRowsPadToTheWidthTheyDraw(t *testing.T) {
	deltas := []string{"hello ", "world", " and more text until it wraps", "\n", "a new line", "你", "好", "\t", "x"}
	for _, width := range append(rowPadWidths, 0, -1) {
		var rows []visualLine
		for n, d := range deltas {
			rows = appendDeltaToVisualLines(rows, d, width)
			checkPaddedRows(t, fmt.Sprintf("appendDeltaToVisualLines delta %d (width=%d)", n, width), rows, width)
		}
		var withBreaks []visualLine
		for n, d := range []string{"a\nb", "c", "\nd\ne", "f"} {
			withBreaks = appendDeltaWithNewlinesVisual(withBreaks, d, width)
			checkPaddedRows(t, fmt.Sprintf("appendDeltaWithNewlinesVisual delta %d (width=%d)", n, width), withBreaks, width)
		}
	}
}

// TestOpenBoxRowsAskForNoPadding covers the floating surfaces. Nothing in a box is
// a soft-wrap predecessor — a box brackets content whose rows each end their own
// original line, and its one caller flattens the result to a string without a frame
// reading it — so the zero every row here carries is the answer, not an omission.
func TestOpenBoxRowsAskForNoPadding(t *testing.T) {
	styles := DefaultStyles()
	border := color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff}
	for _, width := range []int{1, 12, 40, 80} {
		content := wrapVisualLines("a content row\nand another", width)
		box := styles.RenderOpenBoxLines(content, width, border)
		if len(box) != len(content)+2 {
			t.Fatalf("width %d: the box has %d rows, want %d", width, len(box), len(content)+2)
		}
		for i, vl := range []visualLine{box[0], box[len(box)-1]} {
			if vl.Pad != 0 || vl.Cont {
				t.Errorf("width %d: box rule %d asks for %d spaces and continues=%v; a rule brackets content and is padded by nobody",
					width, i, vl.Pad, vl.Cont)
			}
		}
		if box[0].Text != box[len(box)-1].Text {
			t.Errorf("width %d: the box's two rules differ", width)
		}
	}
}

// TestVisualLineCarriesItsPaddingForFree pins the claim visualLine's doc makes: the
// count rides in the bytes Cont's bool already left unused, so carrying it costs no
// memory. A 128 KB message is some ~2,600 of these rows held in a window's cache,
// and 8 bytes each would be 20 KB a window that a performance change has no business
// adding — which is why the field is int32 and not int.
func TestVisualLineCarriesItsPaddingForFree(t *testing.T) {
	const withPadding = 24 // a string (16) and a bool, rounded up to the alignment
	if got := unsafe.Sizeof(struct {
		Text string
		Cont bool
	}{}); got != withPadding {
		t.Fatalf("a row without its padding is %d bytes, not %d, so the premise of this check has changed", got, withPadding)
	}
	if got := unsafe.Sizeof(visualLine{}); got != withPadding {
		t.Errorf("visualLine is %d bytes; the count was meant to fit in the padding the bool already leaves, and at %d bytes it does not", got, withPadding)
	}
}

// countPadded is the number of rows that ask for padding.
func countPadded(rows []visualLine) int {
	n := 0
	for _, vl := range rows {
		if vl.Pad > 0 {
			n++
		}
	}
	return n
}

// TestFrameWritesThePaddingARowAsksFor is the frame-level half. The checks above
// are about the rows a window holds; this one is about the bytes a frame sends, and
// it needs content wide enough that the count is not zero.
//
// An ASCII row that a hard wrap broke is exactly the width, so it asks for no
// padding — which is why TestCursorFramePadsTheRowsItDraws, whose fixture is ASCII,
// passes whether the frame reads the count a row carries or ignores it. Two cells
// against an odd width cannot divide evenly, and then a frame that stopped
// consulting the count draws a short row and every row after it lands one terminal
// row out.
func TestFrameWritesThePaddingARowAsksFor(t *testing.T) {
	// Odd, so a two-cell cluster straddles the break and the row before it asks
	// for the one space that brings it up to the width.
	const width = 41
	styles := DefaultStyles()
	wb := NewWindowBuffer(width, styles)
	wb.AppendOrUpdate("AT", "wide", strings.Repeat("你好世界中文", 20))

	_ = wb.GetAll(-1, false) // full render populates the rows
	w := wb.WindowAt(0)
	rows := append([]visualLine(nil), w.cache.lines...)
	if countPadded(rows) == 0 {
		t.Fatalf("no row at width %d asks for padding, so this case cannot check what a frame writes", width)
	}

	wb.SetViewportPosition(0, len(rows))
	frame := wb.GetAll(-1, false)

	// Rebuilt from the rows and a measurement of their text — not from the counts
	// the rows carry — so a count the frame trusted too far shows up here. The
	// branch structure mirrors renderVirtual's: a row a continuation follows is
	// padded to the width, a row ending its original line is erased instead so a
	// selection carries no trailing spaces, and the fragment's last row is erased
	// once after the loop.
	var b strings.Builder
	for i, vl := range rows {
		if i > 0 && !vl.Cont {
			b.WriteString("\n")
		}
		b.WriteString(vl.Text)
		switch {
		case i < len(rows)-1 && rows[i+1].Cont:
			b.WriteString(strings.Repeat(" ", wantPad(vl.Text, width)))
		case i < len(rows)-1:
			b.WriteString("\x1b[K")
		}
	}
	b.WriteString("\x1b[K")
	want := b.String()

	if !strings.HasPrefix(frame, want) {
		t.Errorf("the frame is not the rows with the padding they ask for:\n got  %q\n want %q", frame, want)
	}
}
