package terminal

// Regression tests for the CUP-anchored input box and status bar:
// before the fix, the input box position depended on the display area's
// actual row count (because the View() concatenated display + input +
// status as base rows in sequence). When the display's actual row count
// drifted from viewportHeight — e.g. an attachment path wider than the
// box that wrapLabels did not pre-wrap, or fragment content changes
// from scrolling — the input box and cursor landed on the wrong row.
// After the fix, the input box is positioned with an absolute CUP and
// its location is invariant under display content / scroll position.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// findCupRow returns the 1-indexed row of the first CUP sequence in
// content. Returns (0, false) if no CUP sequence is present.
func findCupRow(t *testing.T, content string) (int, bool) {
	t.Helper()
	re := regexp.MustCompile(`\x1b\[(\d+);1H`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		return 0, false
	}
	row := 0
	for _, c := range m[1] {
		row = row*10 + int(c-'0')
	}
	return row, true
}

// TestInputBoxCUPAnchoredToBottom verifies that the input box is drawn
// at an absolute CUP whose row equals (windowHeight - inputHeight).
// Independent of any display content, the input box top rule is always
// on the same row.
func TestInputBoxCUPAnchoredToBottom(t *testing.T) {
	m := newTestTerminal()

	v := m.View()
	content := v.Content

	// The first CUP in the view content positions the input box top rule.
	wantRow := m.windowHeight - m.input.Height()
	row, ok := findCupRow(t, content)
	if !ok {
		t.Fatal("expected CUP sequence in View content (input box must be CUP-anchored)")
	}
	if row != wantRow {
		t.Errorf("input box CUP row = %d, want %d (windowHeight - inputHeight)", row, wantRow)
	}

	// The status bar CUP is the LAST CUP and lands on the last row.
	lastCupRe := regexp.MustCompile(`\x1b\[(\d+);1H`)
	matches := lastCupRe.FindAllStringSubmatch(content, -1)
	if len(matches) < 2 {
		t.Fatal("expected at least 2 CUPs in View content (input box + status bar)")
	}
	last := matches[len(matches)-1]
	statusRow := 0
	for _, c := range last[1] {
		statusRow = statusRow*10 + int(c-'0')
	}
	if statusRow != m.windowHeight {
		t.Errorf("status bar CUP row = %d, want %d (last row)", statusRow, m.windowHeight)
	}
}

// TestInputBoxPositionInvariantUnderScroll verifies that scrolling the
// display does NOT move the input box. Before the fix, scrolling could
// change fragment content / emittedRows, and the input box (which was
// appended as a base row after the display) would shift up or down with
// it. With CUP anchoring the input box stays put.
func TestInputBoxPositionInvariantUnderScroll(t *testing.T) {
	m := newTestTerminal()
	wb := m.out.WindowBuffer()

	// Fill the display with enough content that scrolling has visible
	// effect on the rendered display region.
	for i := 0; i < 40; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantT,
			"id-"+itoa(i),
			"line "+itoa(i)+" — a bit of content to scroll past")
	}
	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()

	v0 := m.View()
	row0, ok := findCupRow(t, v0.Content)
	if !ok {
		t.Fatal("expected CUP sequence at row 0 scroll")
	}

	// Scroll up so the viewport now shows a different window range.
	m.display = m.display.GotoTop()
	m = m.updateDisplayHeight()
	v1 := m.View()
	row1, ok := findCupRow(t, v1.Content)
	if !ok {
		t.Fatal("expected CUP sequence at scroll-up state")
	}

	if row0 != row1 {
		t.Fatalf("input box CUP row changed on scroll: was %d, now %d (must be invariant)", row0, row1)
	}
}

// TestInputBoxPositionInvariantUnderOversizeAttachment verifies that an
// attachment path wider than the input box (the case that historically
// caused the misalignment) does NOT shift the input box. With CUP
// anchoring + wrapLabels pre-wrapping, the input box stays where the
// layout invariant says it should.
func TestInputBoxPositionInvariantUnderOversizeAttachment(t *testing.T) {
	m := newTestTerminal()
	wb := m.out.WindowBuffer()

	// Display content (so display area is non-trivial).
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1",
		"some content that pushes the display around a bit")

	// Anchor without attachment.
	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()
	v0 := m.View()
	row0, _ := findCupRow(t, v0.Content)

	// Now add a long attachment path — wider than the box (80 wide).
	m = m.addAttachment("/very/long/path/that/exceeds/the/box/width/by/lots/file.txt")
	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()
	v1 := m.View()
	row1, _ := findCupRow(t, v1.Content)

	wantRow1 := m.windowHeight - m.input.Height()
	if row1 != wantRow1 {
		t.Errorf("oversize attachment: input box CUP row = %d, want %d", row1, wantRow1)
	}
	// row0 should differ from row1 because adding the attachment grows
	// the input box (pushing the top rule up). What matters is that
	// row1 matches the formula, not that row0 == row1.
	if row0 == row1 {
		// Possible only if attachment didn't add any rows — that's a
		// different bug, but not the one we're guarding against.
		t.Logf("note: row unchanged after adding attachment (row0=%d row1=%d)", row0, row1)
	}
}

// TestStatusBarSingleRow verifies the status bar never soft-wraps onto a
// second row — the terminal sees it as a single row after the CUP
// position. A runaway status text used to leak a soft-wrap onto the
// row above and visually smash into the input box bottom rule.
//
// Asserts on display width (ansi.StringWidth), not byte count: the
// indicator and ellipsis are multi-byte UTF-8 characters whose byte
// length exceeds their cell width.
func TestStatusBarSingleRow(t *testing.T) {
	m := newTestTerminal()

	// Force a long status text that would overflow without truncation.
	m.statusLeft = strings.Repeat("X", 200)
	m.inProgress = true

	v := m.View()
	content := stripANSI(v.Content)

	// Find the last non-empty row — should be the status bar.
	rows := strings.Split(strings.TrimRight(content, "\n"), "\n")
	last := rows[len(rows)-1]
	if w := cellWidth(last); w > m.windowWidth {
		t.Errorf("status bar row display width = %d, want ≤ %d (status text must be truncated)",
			w, m.windowWidth)
	}
}

// itoa is a tiny stdlib-free integer-to-string helper for test readability.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
