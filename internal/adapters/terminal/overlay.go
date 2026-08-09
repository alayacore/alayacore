package terminal

// Overlay rendering for selectors and overlay lifecycle helpers.
// Shared logic for positioning overlay content centered horizontally with
// a consistent bottom alignment, plus trackOverlay for managing open/close state.

import (
	"charm.land/lipgloss/v2"
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
func renderOverlay(baseContent string, box string, screenWidth, screenHeight int, yOffset int) string {
	x, y := overlayOrigin(box, screenWidth, screenHeight)
	y = max(0, y+yOffset)

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(box).X(x).Y(y).Z(0),
	)
	return c.Render()
}

// overlayOrigin returns the top-left screen position of an overlay box:
// centered horizontally, bottom edge aligned at 60% down the terminal.
// Mirrors renderOverlay's geometry so cursor positioning stays in sync with
// where the overlay content is actually drawn.
func overlayOrigin(box string, screenWidth, screenHeight int) (x, y int) {
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x = max(0, (screenWidth-boxWidth)/2)

	// Align the bottom of all overlays at 60% down the terminal
	bottomY := screenHeight * 3 / 5
	y = max(0, bottomY-boxHeight)
	return x, y
}
