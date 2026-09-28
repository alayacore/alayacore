package terminal

// Overlay rendering for selectors and overlay lifecycle helpers.
// Shared logic for positioning overlay content centered horizontally with
// a consistent bottom alignment, plus trackOverlay for managing open/close state.

import (
	"fmt"
	"strings"
)

// overlayCloseTracker tracks whether an overlay was open before key handling,
// so the caller can restore focus if it closed itself.
type overlayCloseTracker struct {
	wasOpen bool
}

// trackOverlay records whether the overlay was open at the start of handling.
func trackOverlay(ov interface{ IsOpen() bool }) overlayCloseTracker {
	return overlayCloseTracker{wasOpen: ov.IsOpen()}
}

// JustClosed returns true if the overlay was open before and is now closed.
func (t overlayCloseTracker) JustClosed(ov interface{ IsOpen() bool }) bool {
	return t.wasOpen && !ov.IsOpen()
}

// renderOverlay positions a content box centered horizontally, with its bottom
// edge aligned at a consistent vertical position, plus a yOffset adjustment.
//
// RAW (soft-wrap) mode: the base content is written verbatim to the terminal
// and soft-wrapped, so an overlay cannot be composited by line (the base
// "lines" are continuous fragments, not rows). Instead each box row is
// written at its absolute screen position with a CUP sequence, padded to
// the box width so it fully covers the base content beneath.
//
// Every row this function emits is bounded by the columns the box actually has
// on screen, so none of them reaches the terminal wider than the pane and none
// soft-wraps. A box wider than the terminal — a narrow pane, or one row no
// component clamped, like the model selector's "No models match your search." —
// cannot be centered, so it starts at column 0 and its rows are cut at the
// right edge. Why an overlay row must not wrap is diffFrameRows' skip rule.
//
// The bound is this function's, not the frame's: ConfirmDialog.RenderOverlay
// has a second path that emits two of its rows as ONE continuous write wider
// than the pane, deliberately, so a long command copies without a fake newline
// in the middle. That is the one overlay soft-wrap run, and it is the run the
// skip rule has to handle.
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int, yOffset int) string {
	x, y := overlayOrigin(box, screenWidth, screenHeight)
	y = max(0, y+yOffset)

	boxWidth := Width(box)
	boxHeight := Height(box)
	// The columns the box has from its origin to the right edge of the
	// screen: the box width, or less when the box does not fit.
	avail := max(0, screenWidth-x)
	target := min(boxWidth, avail)

	var sb strings.Builder
	sb.Grow(len(baseContent) + len(box) + boxHeight*12)
	sb.WriteString(baseContent)

	rows := strings.Split(box, "\n")
	for i, row := range rows {
		rowY := y + i
		if rowY >= screenHeight {
			break
		}
		// Bound the row to the space available, then pad to it so the row
		// fully covers the base content beneath. keepCells preserves the
		// escapes past the cut, so a truncated row keeps its SGR reset.
		if w := cellWidth(row); w > target {
			row = keepCells(row, target)
		}
		if w := cellWidth(row); w < target {
			row += strings.Repeat(" ", target-w)
		}
		// Absolute cursor position (1-based rows/cols).
		fmt.Fprintf(&sb, "\x1b[%d;%dH", rowY+1, x+1)
		sb.WriteString(row)
	}
	return sb.String()
}

// renderHelpBar renders an overlay's bottom help bar: the text is
// truncated to the box width and padded by DISPLAY width so the bar
// fills the box exactly. An overflowing help row would widen the
// measured box (renderOverlay derives the box width from the widest
// row) and shift the whole overlay horizontally — which is exactly what
// happened when Tab toggled between the short input help and the longer
// list help (the Tab-focus flicker).
func renderHelpBar(helpStyle Style, help string, boxWidth int) string {
	help = truncateWithSuffix(help, max(0, boxWidth))
	if w := cellWidth(help); w < boxWidth {
		help += strings.Repeat(" ", boxWidth-w)
	}
	return helpStyle.Render(help)
}

// overlayOrigin returns the top-left screen position of an overlay box:
// centered horizontally, bottom edge aligned at 60% down the terminal.
// Mirrors renderOverlay's geometry so cursor positioning stays in sync with
// where the overlay content is actually drawn.
func overlayOrigin(box string, screenWidth, screenHeight int) (x, y int) {
	boxWidth := Width(box)
	boxHeight := Height(box)

	// Center horizontally
	x = max(0, (screenWidth-boxWidth)/2)

	// Align the bottom of all overlays at 60% down the terminal
	bottomY := screenHeight * 3 / 5
	y = max(0, bottomY-boxHeight)
	return x, y
}
