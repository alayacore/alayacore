package terminal

// Phase-2 soft-wrap viewport tests (REFACTOR.md): renderVirtual clips the
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
		w := ansi.StringWidth(line)
		rows += max(1, (w+width-1)/width)
	}
	return rows
}

// extractWindowContent returns the plain text between a window's two box
// rules (the content region), or "" when the rules are not found. The
// test content contains no '─', so the first '─' run is the top rule.
func extractWindowContent(plain string) string {
	i := strings.Index(plain, "─")
	if i < 0 {
		return ""
	}
	// Top rule = the run of '─' (3-byte UTF-8 each) starting at i.
	j := i
	for j+3 <= len(plain) && plain[j:j+3] == "─" {
		j += 3
	}
	k := strings.Index(plain[j:], "─")
	if k < 0 {
		return ""
	}
	return plain[j : j+k]
}

// TestRenderVirtualFragmentOutput verifies the viewport output shape:
// each window is one fragment ending in an EL erase; within a fragment,
// rows of the SAME original line (soft-wrap continuations) join without
// '\n' and are padded to the full width, while UI rows (header, rules)
// and different original lines are hard '\n' separated.
func TestRenderVirtualFragmentOutput(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	// AT window: header + rule + 4 content rows + rule = 7 visual lines.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning window: 1 visual line.
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")

	// Viewport exactly the document height (8 rows) → no blank padding.
	wb.SetViewportPosition(0, 8)
	out := wb.GetAll(-1, false)
	plain := stripANSI(out)

	// AT fragment: the single original long line's content rows join
	// without '\n' (soft wrap) — extract the region between the rules.
	if !strings.Contains(plain, "ASSISTANT") {
		t.Errorf("AT fragment missing header: %q", plain)
	}
	content := extractWindowContent(plain)
	trimmed := strings.Trim(content, "\n") // rule boundaries are hard newlines
	if strings.Contains(trimmed, "\n") {
		t.Errorf("single-line content must be continuous (no newline): %q", content)
	}
	// 25 words = 125 cells: rows of 40 + 40 + 40 + 5 (the last row is not
	// padded — it ends the original line before the bottom rule).
	if w := ansi.StringWidth(trimmed); w != 125 {
		t.Errorf("AT content display width = %d, want 125 (25 words)", w)
	}
	if fragmentRows(plain) != 8 {
		t.Errorf("fragment rows = %d, want 8 (AT 7 + AR 1)", fragmentRows(plain))
	}

	// Folded fragment: single short line, unpadded (its EL erase clears
	// any previous frame's residue on the row, keeping selections free of
	// trailing spaces).
	if !strings.Contains(plain, "▶ REASONING   short reasoning") {
		t.Errorf("AR folded line missing: %q", plain)
	}
}

// TestRenderVirtualScrollToMiddle verifies scrolling into the middle of a
// tall window shows the correct content fragment: no header, no box rules,
// no arrow, starting at the visual line at the scroll offset.
func TestRenderVirtualScrollToMiddle(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))

	// Window visual lines: [0]=header, [1]=rule, [2..5]=content, [6]=rule.
	// Viewport [2,5): content rows 2,3,4 only.
	wb.SetViewportPosition(2, 3)
	raw := wb.GetAll(-1, false)
	out := stripANSI(raw)

	if strings.Contains(out, "ASSISTANT") {
		t.Errorf("scrolled-into-window fragment must not contain the header: %q", out)
	}
	if strings.Contains(out, "─") {
		t.Errorf("scrolled-into-window fragment must not contain box rules: %q", out)
	}
	if strings.Contains(out, "▶") || strings.Contains(out, "▼") {
		t.Errorf("scrolled-into-window fragment must not contain the fold arrow: %q", out)
	}
	if strings.Contains(out, "\n") {
		t.Errorf("fragment must be continuous text: %q", out)
	}
	// 3 visual lines of 40 cells each (the content rows happen to fill
	// the width exactly); the fragment tail carries an EL erase for the
	// last row.
	if w := ansi.StringWidth(out); w != 3*40 {
		t.Errorf("fragment display width = %d, want %d (3 lines × 40)", w, 3*40)
	}
	if !strings.HasSuffix(raw, "\x1b[K") {
		t.Errorf("fragment must end with an EL erase, got %q", raw)
	}
	// Content rows are word-runs; the fragment must contain the 3rd
	// through 5th rows' text in order (rows of 8 words each).
	want := strings.TrimRight(strings.Repeat("word ", 24), " ")
	got := strings.TrimRight(out, " ")
	if got != want {
		t.Errorf("scrolled fragment:\n  got:  %q\n  want: %q", got, want)
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

	wb.SetViewportPosition(0, 7)
	raw := wb.GetAll(-1, false)
	out := stripANSI(raw)

	// The content region sits between the window's two box rules and must
	// be continuous (no hard newline) — the single line's soft wraps.
	body := extractWindowContent(out)
	if body == "" {
		t.Fatal("content region not found")
	}
	body = strings.Trim(body, "\n") // rule boundaries are hard newlines
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
	// AT window: 7 visual lines (0..6).
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded reasoning: 1 visual line (7).
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")

	// Viewport [5,8): AT rows 5,6 + AR row 7 = 3 rows.
	wb.SetViewportPosition(5, 3)
	out := wb.GetAll(-1, false)
	plain := stripANSI(out)

	if fragmentRows(plain) != 3 {
		t.Errorf("viewport rows = %d, want 3 (fragments: %q)", fragmentRows(plain), plain)
	}
	// AT fragment rows 5,6: content row 5 + bottom rule — must contain
	// the rule (padded to width) but not the top rule/header.
	if !strings.Contains(plain, "─") {
		t.Errorf("AT fragment should contain the bottom rule: %q", plain)
	}
	if strings.Contains(plain, "ASSISTANT") {
		t.Errorf("AT fragment must not contain the header: %q", plain)
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
	// AT window: header + rule + 4 content + rule = 7 visual lines.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))
	// Folded windows: 1 visual line each.
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")
	wb.AppendOrUpdate(tlv.TagUserT, "ut-1", "hello user")

	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(5).updateContent()
	// Auto-follow: viewport at bottom — lines 4..9 (AT rows 4-6, AR, UT).
	if dm.scrollView.YOffset() != 4 {
		t.Fatalf("YOffset = %d, want 4 (document 9 lines - viewport 5)", dm.scrollView.YOffset())
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
	// The viewport shows the TAIL of the AT window (rows 4-6: content
	// rows 3-4 + bottom rule) — the header/rule are scrolled off.
	if strings.Contains(plain, "ASSISTANT") {
		t.Errorf("viewport is mid-window — the AT header must not be visible: %q", plain)
	}
	if !strings.Contains(plain, "REASONING") || !strings.Contains(plain, "USER") {
		t.Errorf("viewport should include the folded windows: %q", plain)
	}
	// And the bottom row is the bottom rule, not the top rule.
	if strings.Contains(plain, "REASONING") && !strings.Contains(plain, "─") {
		t.Errorf("viewport should include the AT bottom rule: %q", plain)
	}
}

// TestRenderVirtualCursorArrow verifies the cursor window's fold arrow is
// colored and only appears when the window's first line is visible.
func TestRenderVirtualCursorArrow(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	idx := wb.AppendOrUpdate(tlv.TagAssistantT, "at-1", strings.Repeat("word ", 25))

	// Viewport at top: cursor window's arrow is present (colored).
	wb.SetViewportPosition(0, 5)
	out := wb.GetAll(idx, false)
	if !containsANSI(out) {
		t.Error("cursor render should color the arrow")
	}
	if !strings.Contains(stripANSI(out), "▼") {
		t.Errorf("cursor window arrow missing at top: %q", stripANSI(out))
	}

	// Scrolled into the middle: no arrow (header not visible).
	wb.SetViewportPosition(2, 5)
	out = wb.GetAll(idx, false)
	if strings.Contains(stripANSI(out), "▼") {
		t.Errorf("arrow must not appear when scrolled into the window: %q", stripANSI(out))
	}
}
