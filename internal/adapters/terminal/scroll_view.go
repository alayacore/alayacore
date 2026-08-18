package terminal

// ScrollView is a vertically scrollable viewport that replaces
// Bubbles' viewport component. It provides:
//   - Content management (pre-clipped visible region)
//   - Vertical scrolling (scroll up/down, goto top/bottom)
//   - YOffset management
//   - Height/width control
//
// SOFT-WRAP MODEL (REFACTOR.md): WindowBuffer.renderVirtual clips the
// window buffer to the viewport and produces the visible region as
// soft-wrap fragments — continuous text within a window ('\n' only
// between windows), already padded to the viewport height with blank
// rows. ScrollView therefore does NOT re-split or slice content; it
// stores the pre-clipped region and returns it verbatim. Scroll
// clamping is based on the full document visual line count
// (WithTotalLines), not the clipped content length.
//
// It does NOT support soft wrapping, gutters, highlights, or
// horizontal scrolling — features AlayaCore doesn't use.

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ScrollView is a simple scrollable viewport.
type ScrollView struct {
	width      int
	height     int
	yOffset    int
	content    string // pre-clipped visible region (soft-wrap fragments)
	totalLines int    // full document visual line count (clamping basis)
}

// NewScrollView creates a new ScrollView with the given dimensions.
func NewScrollView(width, height int) ScrollView {
	return ScrollView{
		width:  max(0, width),
		height: max(0, height),
	}
}

// SetWidth sets the viewport width (unused in rendering, kept for API compat).
func (m ScrollView) WithWidth(w int) ScrollView {
	m.width = max(0, w)
	return m
}

// SetHeight sets the viewport height.
func (m ScrollView) WithHeight(h int) ScrollView {
	m.height = max(0, h)
	return m.clampYOffset()
}

// Height returns the viewport height.
func (m ScrollView) Height() int {
	return m.height
}

// WithTotalLines sets the full document visual line count — the clamping
// basis for yOffset. The stored content is the pre-clipped visible
// region, so its length is unrelated to the document length.
func (m ScrollView) WithTotalLines(n int) ScrollView {
	m.totalLines = max(0, n)
	return m.clampYOffset()
}

// WithContent sets the content to display — the pre-clipped visible
// region produced by WindowBuffer.renderVirtual. yOffset is NOT clamped
// against the content length (it is a viewport clip, not the document).
func (m ScrollView) WithContent(s string) ScrollView {
	m.content = s
	return m
}

// YOffset returns the current vertical scroll position.
func (m ScrollView) YOffset() int {
	return m.yOffset
}

// SetYOffset sets the vertical scroll position.
func (m ScrollView) WithYOffset(y int) ScrollView {
	m.yOffset = max(0, y)
	return m.clampYOffset()
}

// ScrollDown scrolls down by n lines.
func (m ScrollView) ScrollDown(n int) ScrollView {
	m.yOffset += n
	return m.clampYOffset()
}

// ScrollUp scrolls up by n lines.
func (m ScrollView) ScrollUp(n int) ScrollView {
	m.yOffset -= n
	return m.clampYOffset()
}

// GotoBottom scrolls to the bottom of the content.
func (m ScrollView) GotoBottom() ScrollView {
	m.yOffset = m.maxYOffset()
	return m
}

// GotoTop scrolls to the top of the content.
func (m ScrollView) GotoTop() ScrollView {
	m.yOffset = 0
	return m
}

// AtBottom returns whether the viewport is at the bottom.
func (m ScrollView) AtBottom() bool {
	return m.yOffset >= m.maxYOffset()
}

// AtTop returns whether the viewport is at the top.
func (m ScrollView) AtTop() bool {
	return m.yOffset <= 0
}

// PastBottom returns whether the viewport is scrolled past the last line.
func (m ScrollView) PastBottom() bool {
	return m.yOffset > m.maxYOffset()
}

// View returns the rendered content — the pre-clipped visible region
// produced by WindowBuffer.renderVirtual, which already pads to the
// viewport height with blank rows (it knows the visual row count; a
// soft-wrap fragment occupies several terminal rows, so ScrollView
// cannot count them from the string). The empty-buffer case is padded
// here as erased blank rows: the first row is erased, then each
// following row is entered with '\n' and erased — the overlay renderer
// leaves no previous frame content on those rows.
func (m ScrollView) View() string {
	if m.height <= 0 {
		return ""
	}
	if m.content == "" {
		return ansi.EraseLine(0) + strings.Repeat("\n"+ansi.EraseLine(0), max(0, m.height-1))
	}
	return m.content
}

// maxYOffset returns the maximum valid Y offset.
func (m ScrollView) maxYOffset() int {
	return max(0, m.totalLines-m.height)
}

// clampYOffset ensures Y offset is within valid bounds.
func (m ScrollView) clampYOffset() ScrollView {
	m.yOffset = clampInt(m.yOffset, 0, max(0, m.totalLines-m.height))
	return m
}

// clampInt clamps v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if hi < lo {
		lo, hi = hi, lo
	}
	return min(hi, max(lo, v))
}
