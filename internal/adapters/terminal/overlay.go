package terminal

// Overlay rendering for selectors and overlay lifecycle helpers.
// Shared logic for positioning overlay content centered horizontally with
// a consistent bottom alignment, plus trackOverlay for managing open/close state.

import (
	"fmt"
	"strings"

	ansi "github.com/charmbracelet/x/ansi"
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
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int, yOffset int) string {
	x, y := overlayOrigin(box, screenWidth, screenHeight)
	y = max(0, y+yOffset)

	boxWidth := Width(box)
	boxHeight := Height(box)

	var sb strings.Builder
	sb.Grow(len(baseContent) + len(box) + boxHeight*12)
	sb.WriteString(baseContent)

	rows := strings.Split(box, "\n")
	for i, row := range rows {
		rowY := y + i
		if rowY >= screenHeight {
			break
		}
		// Pad to the box width so the row fully covers the base content.
		if w := ansi.StringWidth(row); w < boxWidth {
			row += strings.Repeat(" ", boxWidth-w)
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
	if w := ansi.StringWidth(help); w < boxWidth {
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
