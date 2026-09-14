package terminal

// Tests for the cursor highlight and, with it, the styles a window's row is
// assembled from.
//
// The row is lineStyleForTag's style — bold, one color: the fold marker, the
// label and the arrival timestamp are painted with it, so the row reads as
// one unit rather than as a dim marker followed by a bold word and a dim
// clock — and its cursor register is the same object with the label (and
// error) colors swapped for the selection color (Styles.Selected()).
//
// What the highlight must NOT touch: the folded line's content summary
// (content, not chrome), anything below row 0, and a tool window's name —
// which is the row's payload and takes toolNameStyle in both fold states,
// the invariant TestToolNameIsIdenticalFoldedAndExpanded pins.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// lineStyle is the style the whole window line is drawn in for the styles
// given, derived the way the renderer derives it (a non-user, non-error
// window: the label color, bold). The expectations below build rows out of
// this one object, so a row painted with more than one style fails.
func lineStyle(styles *Styles) Style { return styles.Label.Bold(true) }

// cursorLineStyle is the same style in the cursor's register.
func cursorLineStyle(styles *Styles) Style { return styles.Selected().Label.Bold(true) }

// toolRow returns the transcript row belonging to the tool window (the one
// carrying "TOOL CALL"), whatever its index — the window above it may be
// expanded and push it down.
func toolRow(out string) string {
	for _, row := range strings.Split(out, "\n") {
		if strings.Contains(stripANSI(row), "TOOL CALL") {
			return row
		}
	}
	return ""
}

// TestWindowLineIsOneStyle: the expanded row — marker, label, timestamp —
// is painted with a single style, both normally and under the cursor. The
// expectation is the whole row, built from that one style, so any glyph
// rendered in a different color or weight fails here.
func TestWindowLineIsOneStyle(t *testing.T) {
	const width = 60
	styles := DefaultStyles()
	wb := NewWindowBuffer(width, styles)
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "hello there")
	pinCreatedAt(wb)

	label, stamp := "ASSISTANT", pinnedTime.Format(timeStampLayout)
	gap := strings.Repeat(" ", width-collapsedPrefixWidth-len(label)-timeStampWidth)

	rowWith := func(st Style) string {
		return st.Render(unfoldArrow) + " " + st.Render(label) + gap + st.Render(stamp)
	}
	if got, want := firstRow(wb.GetAll(-1, false)), rowWith(lineStyle(styles)); got != want {
		t.Errorf("plain window line:\n  got:  %q\n  want: %q", got, want)
	}
	if got, want := firstRow(wb.GetAll(idx, false)), rowWith(cursorLineStyle(styles)); got != want {
		t.Errorf("cursor window line:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestWindowLineCollapsedIsOneStyle: the collapsed row's marker and label
// share one style too; the content summary behind them is the exception by
// design — it is content, and it keeps the muted color even under the
// cursor.
func TestWindowLineCollapsedIsOneStyle(t *testing.T) {
	const width = 60
	styles := DefaultStyles()
	wb := NewWindowBuffer(width, styles)
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "hello there")
	pinCreatedAt(wb)
	wb.ToggleFold(idx)

	rowWith := func(st Style) string {
		return st.Render(foldArrow) + " " + st.Render(padLabel("ASSISTANT")) + styles.System.Render("hello there")
	}
	if got, want := firstRow(wb.GetAll(-1, false)), rowWith(lineStyle(styles)); got != want {
		t.Errorf("plain collapsed line:\n  got:  %q\n  want: %q", got, want)
	}
	if got, want := firstRow(wb.GetAll(idx, false)), rowWith(cursorLineStyle(styles)); got != want {
		t.Errorf("cursor collapsed line:\n  got:  %q\n  want: %q", got, want)
	}
}

// styleRun returns the escape-delimited run a plain token is painted with
// inside a rendered row — "\x1b[1;38;2;108;112;134mexecute_command\x1b[m" —
// or "" when the token is not there. Comparing runs is how the tests below
// ask "same style?" without naming the style: two rows that paint a token
// the same way have the same run.
func styleRun(row, token string) string {
	i := strings.Index(row, token)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(row[:i], "\x1b[")
	if start < 0 {
		return ""
	}
	end := strings.Index(row[i:], "\x1b[m")
	if end < 0 {
		return ""
	}
	return row[start : i+end+len("\x1b[m")]
}

// TestToolNameIsIdenticalFoldedAndExpanded: a tool window's name is the one
// token on the row that is not the line's style, and it must be painted the
// same whichever way the window is folded.
//
// Folding a tool window is how a reader gets scaffolding out of the way; it
// must not repaint the token that says which tool ran. The name is not the
// line's style because it is the row's payload: the highlight covers the
// chrome that names the window (the marker, the label, the timestamp) and
// leaves the name and the arguments in the content color — see
// toolNameStyle.
//
// The four runs (plain/cursor × folded/expanded) are compared against each
// other rather than against an expected style, so what is pinned is the
// property itself: folded == expanded, in both registers. The two extra
// assertions keep that from passing vacuously — a row that highlights
// nothing, and a row that highlights everything, both satisfy "all four
// equal" — by requiring that the name does not carry the cursor's color and
// that the label beside it does.
func TestToolNameIsIdenticalFoldedAndExpanded(t *testing.T) {
	const width, name = 80, "execute_command"
	styles := DefaultStyles()
	wb := NewWindowBuffer(width, styles)
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "x") // the tool row is not row 0
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  name,
		Input: []byte(name + ": lscpu"),
	}, 0)
	pinCreatedAt(wb)
	ti, _ := wb.LookupID("t1")

	// A tool window arrives folded; expand it halfway down.
	wb.HandleToolOutput("t1", "ok", false, 0)
	pinCreatedAt(wb)

	type rowCase struct {
		what   string
		row    string
		cursor bool
		folded bool
	}
	cases := []rowCase{
		{"folded, plain", toolRow(wb.GetAll(-1, false)), false, true},
		{"folded, cursor", toolRow(wb.GetAll(ti, false)), true, true},
	}
	wb.ToggleFold(ti)
	cases = append(cases,
		rowCase{"expanded, plain", toolRow(wb.GetAll(-1, false)), false, false},
		rowCase{"expanded, cursor", toolRow(wb.GetAll(ti, false)), true, false},
	)

	want := styleRun(cases[0].row, name)
	if want == "" {
		t.Fatalf("fixture: the folded row does not carry the tool name: %q", cases[0].row)
	}
	cursorName := cursorLineStyle(styles).Render(name)
	for _, tc := range cases {
		if got := styleRun(tc.row, name); got != want {
			t.Errorf("%s: the tool name is painted %q, want %q (the run the folded row uses)",
				tc.what, got, want)
		}
		if strings.Contains(tc.row, cursorName) {
			t.Errorf("%s: the name must not take the cursor's color — it is the row's payload, "+
				"not chrome: %q", tc.what, tc.row)
		}
		if tc.cursor && !strings.Contains(tc.row, cursorLineStyle(styles).Render("TOOL CALL")) {
			t.Errorf("%s: the label must still highlight when the cursor is here: %q", tc.what, tc.row)
		}
		if tc.folded && !strings.Contains(tc.row, styles.ToolContent.Render(" lscpu")) {
			t.Errorf("%s: arguments are content and keep the content color: %q", tc.what, tc.row)
		}
	}

	// The rest of the expanded row is still one style: the timestamp beside
	// the name moves to the cursor register with the label.
	stamp := pinnedTime.Format(timeStampLayout)
	expandedCursor := cases[len(cases)-1]
	if got := styleRun(expandedCursor.row, stamp); got != cursorLineStyle(styles).Render(stamp) {
		t.Errorf("expanded, cursor: the timestamp should take the line's cursor style:\n  got:  %q\n"+
			"  want: %q", got, cursorLineStyle(styles).Render(stamp))
	}
}

// TestCursorHighlightBlockedIsDim: under an overlay every color the line
// draws with is the dim color — marker, label and timestamp alike — so the
// highlight disappears while a modal owns the screen.
func TestCursorHighlightBlockedIsDim(t *testing.T) {
	const width = 60
	styles := DefaultStyles()
	wb := NewWindowBuffer(width, styles)
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "hello there")
	pinCreatedAt(wb)

	dimLine := styles.Dimmed().Label.Bold(true)
	label, stamp := "ASSISTANT", pinnedTime.Format(timeStampLayout)
	gap := strings.Repeat(" ", width-collapsedPrefixWidth-len(label)-timeStampWidth)
	want := dimLine.Render(unfoldArrow) + " " + dimLine.Render(label) + gap + dimLine.Render(stamp)
	if got := firstRow(wb.GetAll(idx, true)); got != want {
		t.Errorf("blocked window line:\n  got:  %q\n  want: %q", got, want)
	}

	wb.ToggleFold(idx)
	wantCollapsed := dimLine.Render(foldArrow) + " " + dimLine.Render(padLabel("ASSISTANT")) +
		styles.System.Foreground(styles.ColorDim).Render("hello there")
	if got := firstRow(wb.GetAll(idx, true)); got != wantCollapsed {
		t.Errorf("blocked collapsed line:\n  got:  %q\n  want: %q", got, wantCollapsed)
	}
}

// TestCursorHighlightInViewportFragments: the same two states through the
// viewport path (renderVirtual), which replaces row 0 per fragment rather
// than re-rendering the window.
func TestCursorHighlightInViewportFragments(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(40, styles)
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "hello there")
	pinCreatedAt(wb)
	wb.SetViewportPosition(0, 3)

	if got := wb.GetAll(idx, false); !strings.Contains(got, cursorLineStyle(styles).Render(unfoldArrow)) {
		t.Errorf("expanded fragment should highlight the marker: %q", got)
	}
	if got := wb.GetAll(-1, false); strings.Contains(got, cursorLineStyle(styles).Render(unfoldArrow)) {
		t.Errorf("a non-cursor fragment must not highlight anything: %q", got)
	}

	wb.ToggleFold(idx)
	wb.SetViewportPosition(0, 2)
	got := wb.GetAll(idx, false)
	if !strings.Contains(got, cursorLineStyle(styles).Render(foldArrow)) {
		t.Errorf("collapsed fragment should highlight the marker: %q", got)
	}
	if !strings.Contains(got, cursorLineStyle(styles).Render(padLabel("ASSISTANT"))) {
		t.Errorf("collapsed fragment should highlight the label: %q", got)
	}
	// Scrolled into the window: nothing of row 0 is on screen, so there is
	// nothing to highlight.
	wb.SetViewportPosition(1, 1)
	if got := wb.GetAll(idx, false); strings.Contains(got, cursorLineStyle(styles).Render(foldArrow)) {
		t.Errorf("off-screen row 0 must not be highlighted: %q", got)
	}
}

// TestCursorHighlightFollowsContent: the highlighted row is memoized per
// render-cache generation, so it must be rebuilt when the content that
// feeds it changes (a longer summary, a tool status flip).
func TestCursorHighlightFollowsContent(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(40, styles)
	// Reasoning windows start folded — no toggle needed.
	idx := wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "first")
	pinCreatedAt(wb)

	if got := wb.GetAll(idx, false); !strings.Contains(got, cursorLineStyle(styles).Render(padLabel("REASONING"))) {
		t.Fatalf("initial cursor render should highlight the label: %q", got)
	}

	// New content → the window is invalidated → the highlight is rebuilt
	// against it. AppendOrUpdate appends to the existing window, so the
	// summary now reads "firstsecond thought" (muted) under the still
	// highlighted label.
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "second thought")
	pinCreatedAt(wb)
	got := wb.GetAll(idx, false)
	if !strings.Contains(got, cursorLineStyle(styles).Render(padLabel("REASONING"))) {
		t.Errorf("highlight must survive a content change: %q", got)
	}
	if !strings.Contains(got, styles.System.Render("firstsecond thought")) {
		t.Errorf("summary should show the new content, muted: %q", got)
	}
}
