package terminal

// Regression tests for the CUP-anchored blocks — the live edge, the input
// box and the status bar. Before the fix, the input box's position depended
// on the display area's actual row count (View() concatenated display, input
// and status as base rows in sequence), so when that count drifted from
// viewportHeight — an attachment path wider than the box that wrapLabels did
// not pre-wrap, fragment content changes from scrolling — the input box and
// the cursor landed on the wrong row. Anchoring each block with an absolute
// CUP makes its position invariant under display content and scroll position.
//
// The assertions name the whole anchor sequence rather than "the first CUP in
// the frame": that phrasing identified the input box while exactly two blocks
// were anchored, and stopped being true the moment the live edge became the
// third.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// cupRows returns the 1-indexed rows of every absolute-CUP anchor in
// content, in frame order. View() emits three of them — the live edge, the
// input box's top rule, the status bar — and the live edge only when its
// marker has something to show, so a test that names "the first CUP" is
// really naming whichever of the three comes first. These tests assert the
// whole sequence instead.
func cupRows(content string) []int {
	re := regexp.MustCompile(`\x1b\[(\d+);1H`)
	matches := re.FindAllStringSubmatch(content, -1)
	rows := make([]int, 0, len(matches))
	for _, m := range matches {
		row := 0
		for _, c := range m[1] {
			row = row*10 + int(c-'0')
		}
		rows = append(rows, row)
	}
	return rows
}

// wantCupRows is the anchor sequence the layout owes a terminal of this
// size, bottom-up: status bar on the last row, input box above it, live
// edge above that (omitted when the marker draws nothing).
func wantCupRows(m Terminal) []int {
	inputY := max(0, m.windowHeight-m.input.Height()-1) // 0-indexed top rule
	rows := make([]int, 0, 3)
	if inputY > 0 && m.renderLiveEdge() != "" {
		rows = append(rows, inputY) // 1-indexed row inputY-1+1
	}
	rows = append(rows, inputY+1, m.windowHeight)
	return rows
}

// TestInputBoxCUPAnchoredToBottom verifies that every anchored block is
// drawn at an absolute CUP whose row follows the layout formula, whatever
// the display contains. Independent of any display content, the live edge,
// the input box top rule and the status bar are always on the same rows.
func TestInputBoxCUPAnchoredToBottom(t *testing.T) {
	m := newTestTerminal()

	v := m.View()
	content := v.Content

	got := cupRows(content)
	want := wantCupRows(m)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CUP anchors = %v, want %v (live edge, input box, status bar)", got, want)
	}
	if len(want) != 3 {
		t.Fatalf("fixture expected 3 anchors (live edge, input, status), got %v — newTestTerminal changed", want)
	}
	if want[1] != m.windowHeight-m.input.Height() {
		t.Errorf("input box top rule row = %d, want %d (windowHeight - inputHeight)", want[1], m.windowHeight-m.input.Height())
	}
	if want[2] != m.windowHeight {
		t.Errorf("status bar CUP row = %d, want %d (last row)", want[2], m.windowHeight)
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

	rows0 := cupRows(m.View().Content)
	if len(rows0) == 0 {
		t.Fatal("expected CUP anchors at row 0 scroll")
	}

	// Scroll up so the viewport now shows a different window range.
	m.display = m.display.GotoTop()
	m = m.updateDisplayHeight()
	rows1 := cupRows(m.View().Content)

	if !reflect.DeepEqual(rows0, rows1) {
		t.Fatalf("anchored rows changed on scroll: was %v, now %v (must be invariant)", rows0, rows1)
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
	rows0 := cupRows(m.View().Content)
	if want := wantCupRows(m); !reflect.DeepEqual(rows0, want) {
		t.Errorf("anchors before the attachment = %v, want %v", rows0, want)
	}

	// Now add a long attachment path — wider than the box (80 wide), so
	// wrapLabels pre-wraps it and the box grows.
	m = m.addAttachment("/very/long/path/that/exceeds/the/box/width/by/lots/file.txt")
	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()
	rows1 := cupRows(m.View().Content)
	if want := wantCupRows(m); !reflect.DeepEqual(rows1, want) {
		t.Errorf("oversize attachment: anchors = %v, want %v", rows1, want)
	}

	// A taller box pushes the whole anchored stack up. What must hold
	// through it is the stack's internal spacing: the live edge exactly one
	// row above the box's top rule, and a status bar nothing can move,
	// because it is pinned to the last row.
	before, after := rows0[len(rows0)-2], rows1[len(rows1)-2]
	if after >= before {
		t.Errorf("growing the input box must push its top rule up: was row %d, now %d", before, after)
	}
	if rows0[len(rows0)-1] != rows1[len(rows1)-1] {
		t.Errorf("the status bar row must not move: was %d, now %d",
			rows0[len(rows0)-1], rows1[len(rows1)-1])
	}
}

// TestStatusBarSingleRow verifies the status bar never soft-wraps onto a
// second row — the terminal sees it as a single row after the CUP
// position. A runaway status text used to leak a soft-wrap onto the
// row above and visually smash into the input box bottom rule.
//
// Asserts on display width (cellWidth), not byte count: the
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
