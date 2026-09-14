package terminal

// Tests for the cursor highlight and, with it, the one property the window
// line has to hold in every state: **every glyph of the row shares a single
// style** — the fold marker, the label, a tool window's name and the
// arrival timestamp are painted with one Style, so the row reads as one
// unit rather than as four differently-weighted pieces.
//
// The style itself is lineStyleForTag's (bold + the window's color: the
// label color, a user prompt's accent, or the error color), and its cursor
// register is the same object with those colors swapped for the selection
// color (Styles.Selected()) — which is what these tests compare against.
//
// What the highlight must NOT touch: the folded line's content summary
// (content, not chrome) and anything below row 0.

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

// TestWindowLineToolNameSharesTheStyle: a tool window's name is part of the
// line, not a second style on it — so it moves with the line when the
// cursor arrives, exactly like the label beside it. The arguments after it,
// and the result inside it, stay content.
func TestWindowLineToolNameSharesTheStyle(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", "x") // occupies row 0 of the transcript
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: []byte("execute_command: lscpu"),
	}, 0)
	pinCreatedAt(wb)
	ti, _ := wb.LookupID("t1")

	// Folded (a tool window starts folded): the name follows the label
	// column, the arguments follow the name.
	plain, cursor := wb.GetAll(-1, false), wb.GetAll(ti, false)
	if !strings.Contains(toolRow(plain), lineStyle(styles).Render("execute_command")) {
		t.Errorf("the tool name should take the line's style: %q", toolRow(plain))
	}
	if !strings.Contains(toolRow(cursor), cursorLineStyle(styles).Render("execute_command")) {
		t.Errorf("the tool name should move to the cursor register with the rest of the line: %q", toolRow(cursor))
	}
	// …and the arguments stay content (the content color, no line weight).
	if !strings.Contains(toolRow(plain), styles.ToolContent.Render(" lscpu")) {
		t.Errorf("arguments are content and keep the content color: %q", toolRow(plain))
	}

	// Expanded: the same name, on the line that also carries the timestamp.
	wb.ToggleFold(ti)
	plain = wb.GetAll(-1, false)
	if !strings.Contains(toolRow(plain), lineStyle(styles).Render("execute_command")) {
		t.Errorf("the expanded line should name the tool in the line's style: %q", toolRow(plain))
	}
	if !strings.Contains(toolRow(plain), lineStyle(styles).Render(pinnedTime.Format(timeStampLayout))) {
		t.Errorf("and the timestamp beside it in the same style: %q", toolRow(plain))
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
// border-cache generation, so it must be rebuilt when the content that
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
