package terminal

// Phase-2 soft-wrap viewport tests: renderVirtual clips the
// viewport to VISUAL lines and outputs soft-wrap fragments — continuous
// text within a window ('\n' only between windows), every line padded to
// the full width except the last, so the terminal soft-wraps exactly at
// the simulated breakpoints. Copying a selection therefore restores the
// original text (no fake newlines).

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/tlv"
)

// fragmentRows returns the number of terminal rows a soft-wrap fragment
// occupies at the test width (40): the fragment's display width divided
// by 40, rounded up (the last visual line may be short). This is the
// invariant that keeps the viewport exactly `height` rows tall.
func fragmentRows(fragment string) int {
	const width = 40
	rows := 0
	for _, line := range strings.Split(fragment, "\n") {
		if line == "" {
			continue // fragment boundary newline, not a row
		}
		w := cellWidth(line)
		rows += max(1, (w+width-1)/width)
	}
	return rows
}

// extractWindowContent returns the plain text of the content region that
// follows a window's opening rule — the rows after row 0, up to the row
// that begins the next window (a rule, or a window's own line), which is
// what now ends a window: an expanded window draws no closing rule. A
// window's own line opens with "+ " when folded and "- " when expanded,
// and the live-edge row above the input box also opens with "- "; all
// three are chrome, not content, so any of them ends the region.
//
// Rows are joined with '\n' (that is how they sit in the output), so a
// single soft-wrapped original line still arrives as one row with no
// newline inside it — which is exactly what the copy-fidelity tests assert.
func extractWindowContent(plain string) string {
	rows := strings.Split(plain, "\n")
	if len(rows) < 2 {
		return ""
	}
	chrome := func(row string) bool {
		return strings.HasPrefix(row, "─") ||
			strings.HasPrefix(row, foldArrow) ||
			strings.HasPrefix(row, unfoldArrow+" ")
	}
	body := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if chrome(row) {
			break
		}
		body = append(body, row)
	}
	return strings.Join(body, "\n")
}

// TestRenderVirtualFragmentOutput verifies the viewport output shape:
// each window is one fragment ending in an EL erase; within a fragment,
// rows of the SAME original line (soft-wrap continuations) join without
// '\n' and are padded to the full width, while UI rows (header, rules)
// and different original lines are hard '\n' separated.
func TestRenderVirtualFragmentOutput(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	// AT window: labeled rule + 4 content rows + rule = 6 visual lines.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning window: 1 visual line.
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")

	// Viewport exactly the document height (6 rows) → no blank padding.
	wb.SetViewportPosition(0, 6)
	out := wb.GetAll(-1, false)
	plain := stripANSI(out)

	// AT fragment: the single original long line's content rows join
	// without '\n' (soft wrap) — extract the region between the rules.
	if !strings.Contains(plain, "ASSISTANT") {
		t.Errorf("AT fragment missing the labeled top rule: %q", plain)
	}
	content := extractWindowContent(plain)
	trimmed := strings.Trim(content, "\n") // rule boundaries are hard newlines
	if strings.Contains(trimmed, "\n") {
		t.Errorf("single-line content must be continuous (no newline): %q", content)
	}
	// 25 words = 125 cells: rows of 40 + 40 + 40 + 5 (the last row is not
	// padded — it ends the original line, and the window ends with it).
	if w := cellWidth(trimmed); w != 125 {
		t.Errorf("AT content display width = %d, want 125 (25 words)", w)
	}
	if fragmentRows(plain) != 6 {
		t.Errorf("fragment rows = %d, want 6 (AT 5 + AR 1)", fragmentRows(plain))
	}

	// Folded fragment: single short line, unpadded (its EL erase clears
	// any previous frame's residue on the row, keeping selections free of
	// trailing spaces).
	if !strings.Contains(plain, foldArrow+" REASONING       short reasoning") {
		t.Errorf("AR folded line missing: %q", plain)
	}
}

// TestRenderVirtualScrolledIntoWindowPinsItsLine verifies what a fragment
// clipped into the middle of a tall window looks like now that the window's
// own line is pinned: the pinned line, then the body — continuous text (one
// soft-wrap run), with the body row that would have been at screen row 0
// displaced. No rules anywhere: a window has none.
func TestRenderVirtualScrolledIntoWindowPinsItsLine(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))

	// Window visual lines: [0]=own line, [1..4]=content (one long line's
	// four soft-wrap rows). Viewport [1,4): the own line is cut off, so it
	// is pinned and content row 0 — the row that would have been at the top
	// — is displaced, leaving content rows 1 and 2.
	wb.SetViewportPosition(1, 3)
	raw := wb.GetAll(-1, false)
	out := stripANSI(raw)

	if !strings.HasPrefix(out, unfoldArrow+" ASSISTANT") {
		t.Errorf("the window's own line must be pinned at row 0: %q", out)
	}
	if strings.Contains(out, "─") {
		t.Errorf("fragment must not contain rules: %q", out)
	}
	// No folded window here. The marker is column 0 of a row, so that is
	// where to look for it: a substring search would trip over the "+" of a
	// UTC offset in the pinned line's timestamp.
	for _, row := range strings.Split(out, "\n") {
		if strings.HasPrefix(row, foldArrow) {
			t.Errorf("fragment must not contain a folded row: %q", row)
		}
	}
	// One hard newline: after the pinned line the body is continuous (the
	// rows of one soft-wrapped line join without '\n').
	if got := strings.Count(out, "\n"); got != 1 {
		t.Errorf("fragment should be pinned line + continuous body, got %d hard newlines: %q", got, out)
	}
	// 3 visual rows: the pinned line (40 cells) + content rows 1,2 (40
	// each, padded so the soft-wrap lands on the row boundary).
	if w := cellWidth(out); w != 3*40 {
		t.Errorf("fragment display width = %d, want %d (3 rows × 40)", w, 3*40)
	}
	if !strings.HasSuffix(raw, "\x1b[K") {
		t.Errorf("fragment must end with an EL erase, got %q", raw)
	}
	// The body is the 9th through 24th words: content rows 1 and 2 of the
	// long line (row 0 — the first eight words — is the displaced row).
	body := strings.TrimRight(strings.SplitN(out, "\n", 2)[1], " ")
	want := strings.TrimRight(strings.Repeat("word ", 16), " ")
	if body != want {
		t.Errorf("scrolled body:\n  got:  %q\n  want: %q", body, want)
	}
}

// TestRenderVirtualCopyRestoresOriginal verifies the copy-fidelity goal:
// the plain text of an over-long SINGLE original line (its soft-wrap
// continuations inside the window), after stripping the layout padding,
// equals the original content with NO newlines — the terminal selection
// yields the original logical line.
func TestRenderVirtualCopyRestoresOriginal(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	original := strings.Repeat("word ", 25)
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", original)

	wb.SetViewportPosition(0, 5)
	raw := wb.GetAll(-1, false)
	out := stripANSI(raw)

	// The content region follows the window's opening rule and must be
	// continuous (no hard newline) — the single line's soft wraps.
	body := extractWindowContent(out)
	if body == "" {
		t.Fatal("content region not found")
	}
	body = strings.Trim(body, "\n")
	if strings.Contains(body, "\n") {
		t.Fatalf("single-line content must not contain newlines: %q", body)
	}
	// Remove trailing layout padding; the remainder must be the original.
	body = strings.TrimRight(body, " ")
	if body != strings.TrimRight(original, " ") {
		t.Errorf("copy restoration failed:\n  got:  %q\n  want: %q", body, strings.TrimRight(original, " "))
	}
}

// TestRenderVirtualWindowBoundary verifies a viewport spanning the end of
// one window and the start of the next produces exactly two fragments
// (each ending in an EL erase), whose terminal rows sum to the viewport
// height.
func TestRenderVirtualWindowBoundary(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	// AT window: 5 visual lines (0..4) — opening rule + 4 content rows.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning: 1 visual line (5).
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")

	// Viewport [3,6): the AT window's own line is cut off, so it is pinned
	// at row 0; its last body row (row 4) follows, then AR (row 5).
	wb.SetViewportPosition(3, 3)
	out := wb.GetAll(-1, false)
	plain := stripANSI(out)

	if fragmentRows(plain) != 3 {
		t.Errorf("viewport rows = %d, want 3 (fragments: %q)", fragmentRows(plain), plain)
	}
	// The AT window is cut off, so its own line is pinned at row 0 and the
	// body row that would have been there is displaced; the folded window
	// that follows brings its own line. No rules anywhere.
	if !strings.HasPrefix(plain, unfoldArrow+" ASSISTANT") {
		t.Errorf("the cut-off window's own line should be pinned at row 0: %q", plain)
	}
	if !strings.Contains(plain, "word") {
		t.Errorf("AT fragment should contain the content tail: %q", plain)
	}
	if strings.Contains(plain, "─") {
		t.Errorf("AT fragment should contain no rule: %q", plain)
	}
	// The folded window that follows brings its own opening row.
	if !strings.Contains(plain, foldArrow+" REASONING") {
		t.Errorf("viewport should reach the folded window after the AT tail: %q", plain)
	}
}

// TestScrollViewSoftWrap verifies ScrollView's new role: it holds the
// pre-clipped visible region and the document total line count; View
// returns the region as-is (renderVirtual already pads to the viewport
// height with blank rows — it knows the visual row count); yOffset
// clamping and AtBottom use the document total, not the clipped content
// length.
func TestScrollViewSoftWrap(t *testing.T) {
	sv := NewScrollView(5).WithTotalLines(100).WithYOffset(90)
	if sv.YOffset() != 90 {
		t.Errorf("YOffset = %d, want 90 (within document)", sv.YOffset())
	}
	// Clamp against document total (100-5 = 95 max).
	sv = sv.WithYOffset(200)
	if sv.YOffset() != 95 {
		t.Errorf("YOffset = %d, want 95 (clamped to document bottom)", sv.YOffset())
	}
	if !sv.AtBottom() {
		t.Error("AtBottom should be true at maxYOffset")
	}

	// Content is the pre-clipped region — returned verbatim (padding is
	// renderVirtual's job, since fragments occupy several terminal rows).
	sv = sv.WithContent("fragment-one\nfragment-two")
	if v := sv.View(); v != "fragment-one\nfragment-two" {
		t.Errorf("View() = %q, want the pre-clipped content verbatim", v)
	}

	// Empty buffer: erased blank rows to fill the viewport — the first
	// row is erased, then each following row is entered and erased.
	sv = NewScrollView(5).WithContent("")
	if v := sv.View(); v != "\x1b[K\n\x1b[K\n\x1b[K\n\x1b[K\n\x1b[K" {
		t.Errorf("View() with empty content = %q, want 5 erased blank rows", v)
	}

	// Content length is unrelated to the document total — yOffset must
	// survive WithContent (it is the scroll state, not a slice index).
	sv = sv.WithContent("clipped").WithTotalLines(100).WithYOffset(50)
	if sv.YOffset() != 50 {
		t.Errorf("YOffset = %d, want 50 (not clamped to clipped content)", sv.YOffset())
	}
}

// TestDisplayViewSoftWrapRows simulates the terminal on DisplayModel's
// final View output: soft-wrapping each fragment at the display width
// must reproduce exactly the viewport-height rows — the acceptance
// criterion that the screen looks identical to the old hard-wrapped
// layout while the bytes carry no fake newlines inside windows.
func TestDisplayViewSoftWrapRows(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	// AT window: its own line + 4 content rows = 5 visual lines.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning: 1 visual line.
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")
	// User prompt: expanded by default — its own line + 1 content row.
	wb.AppendOrUpdate(tlv.TagUserT, "ut-1", "hello user")

	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(5).updateContent()
	// Auto-follow: viewport at bottom — lines 3..7 (AT rows 3-4, AR, UT's
	// line, UT's content).
	if dm.scrollView.YOffset() != 3 {
		t.Fatalf("YOffset = %d, want 3 (document 8 lines - viewport 5)", dm.scrollView.YOffset())
	}
	v := dm.View().Content
	// Simulate the terminal soft-wrap at the display width.
	wrapped := ansi.Hardwrap(v, 40, true)
	rows := strings.Count(wrapped, "\n") + 1
	if rows != 5 {
		t.Errorf("terminal soft-wrap of View() = %d rows, want 5 (viewport height)", rows)
	}
	plain := stripANSI(wrapped)
	if !strings.Contains(plain, "word") {
		t.Errorf("terminal render should contain assistant content: %q", plain)
	}
	// The viewport shows the TAIL of the AT window (rows 2-4: its last
	// three content rows), and because its own line is scrolled off it is
	// PINNED at row 0 — the label stays visible while the body moves, which
	// is what the pin is for.
	if !strings.HasPrefix(plain, unfoldArrow+" ASSISTANT") {
		t.Errorf("the cut-off window's own line should be pinned at row 0: %q", plain)
	}
	if !strings.Contains(plain, "REASONING") || !strings.Contains(plain, "USER") {
		t.Errorf("viewport should include the folded windows: %q", plain)
	}
	// No rule at all: the viewport is mid-window and the folded windows
	// that follow have no box either.
	if strings.Contains(plain, "─") {
		t.Errorf("viewport should show no rule — mid-window and folded windows: %q", plain)
	}
}

// TestRenderVirtualCursorHighlight verifies the cursor render colors the
// one navigational element the window draws — for an expanded window that
// is the label inside the top rule — and that the highlight appears only
// while that row is visible.
func TestRenderVirtualCursorHighlight(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(40, styles)
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))

	// Viewport at top: the window's first row (the labeled rule) is
	// visible, so its label carries the accent color.
	wb.SetViewportPosition(0, 5)
	out := wb.GetAll(idx, false)
	if !containsANSI(out) {
		t.Error("cursor render should color the top rule's label")
	}
	wantLabel := NewStyle().Bold(true).Foreground(styles.ColorAccent).Render("ASSISTANT")
	if !strings.Contains(out, wantLabel) {
		t.Errorf("cursor window label is not in the accent color: %q", out)
	}
	// The rest of the row keeps its own style, and the non-cursor render
	// differs only in that label.
	plain := stripANSI(out)
	if !strings.HasPrefix(plain, unfoldArrow+" ASSISTANT") {
		t.Errorf("cursor render must keep the window's own line: %q", plain)
	}
	// The non-cursor render of the same window carries the dim label
	// instead — the whole point of the cursor highlight.
	if noCursor := wb.GetAll(-1, false); strings.Contains(noCursor, wantLabel) {
		t.Error("non-cursor render must not carry the accent color")
	}

	// Scrolled into the middle: the window's own line is off-screen but
	// pinned, so the cursor still shows — on the pinned row.
	wb.SetViewportPosition(2, 5)
	out = wb.GetAll(idx, false)
	if !strings.HasPrefix(out, NewStyle().Bold(true).Foreground(styles.ColorAccent).Render(unfoldArrow)+" ") {
		t.Errorf("the pinned row should carry the cursor highlight: %q", out)
	}
	if !strings.Contains(out, wantLabel) {
		t.Errorf("the pinned row's label should be in the accent color: %q", out)
	}
	// …and with the cursor on another window the pinned row is plain.
	wb.AppendOrUpdate(tlv.TagAssistantT, "other-1", "another window")
	other, _ := wb.LookupID("other-1")
	out = wb.GetAll(other, false)
	if strings.HasPrefix(out, NewStyle().Bold(true).Foreground(styles.ColorAccent).Render(unfoldArrow)+" ") {
		t.Errorf("the pinned row of a non-cursor window must not be highlighted: %q", out)
	}
}
