package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// wrapLines wraps content into lines at the given width.
// Returns a slice of wrapped lines.
func wrapLines(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := lipgloss.Wrap(content, width, " ")
	return strings.Split(wrapped, "\n")
}

// appendDeltaToLines incrementally wraps a delta onto existing lines.
// This is O(1) for typical streaming deltas instead of O(N) full rewrap.
func appendDeltaToLines(lines []string, delta string, width int) []string {
	if len(lines) == 0 {
		return wrapLines(delta, width)
	}

	if width <= 0 {
		// Just append to last line
		lines[len(lines)-1] += delta
		return lines
	}

	// Check if delta contains newlines - need to process each segment
	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlines(lines, delta, width)
	}

	// Simple case: no newlines in delta, just merge with last line and rewrap
	lastLine := lines[len(lines)-1]
	combined := lastLine + delta

	// Wrap the combined content
	newLines := wrapLines(combined, width)

	// Replace last line with new wrapped lines
	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	// Split delta by newlines
	deltaParts := strings.Split(delta, "\n")

	for i, part := range deltaParts {
		if i == 0 {
			// First part: merge with last existing line
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastLine := lines[len(lines)-1]
				combined := lastLine + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:len(lines)-1], newLines...)
			}
		} else {
			// Subsequent parts: each starts a new line
			newLines := wrapLines(part, width)
			lines = append(lines, newLines...)
		}
	}

	return lines
}

// rewrapLines forces a full rewrap of content (used on resize).
func rewrapLines(content string, width int) []string {
	return wrapLines(content, width)
}
