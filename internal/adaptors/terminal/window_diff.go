package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// DiffContainer holds two panes side by side for diff display
type DiffContainer struct {
	Path  string         // file path for header
	Lines []DiffLinePair // raw line pairs
}

// DiffLinePair represents a pair of old/new lines in a diff
type DiffLinePair struct {
	Old string
	New string
}

// renderDiffContent renders a diff container side by side
func (wb *WindowBuffer) renderDiffContent(diff *DiffContainer, innerWidth int) string {
	// Preallocate lines: header + diff lines
	lines := make([]string, 0, 1+len(diff.Lines))

	// Add header with file path
	lines = append(lines, wb.styles.Tool.Render("edit_file: ")+wb.styles.ToolContent.Render(diff.Path))

	// Calculate width for each side
	// Line format: "= " + paddedOld + " " + "|" + " " + "+ " + newPart
	// Total: 2 + sideWidth + 3 + 2 + sideWidth = 2*sideWidth + 7
	// We need: 2*sideWidth + 7 <= innerWidth
	// So: sideWidth <= (innerWidth - 7) / 2
	sideWidth := (innerWidth - 7) / 2
	if sideWidth < 10 {
		sideWidth = 10 // minimum width
	}

	for _, pair := range diff.Lines {
		// Escape any literal newlines in content (shouldn't happen, but be safe)
		oldPart := strings.ReplaceAll(expandTabs(pair.Old), "\n", "\\n")
		newPart := strings.ReplaceAll(expandTabs(pair.New), "\n", "\\n")

		// Check if one side is empty (different line counts)
		oldEmpty := pair.Old == ""
		newEmpty := pair.New == ""

		// Check if content is the same (before truncation)
		isSame := pair.Old == pair.New

		// Truncate if needed (use display width for proper Unicode handling)
		oldPart = truncateByWidth(oldPart, sideWidth)
		newPart = truncateByWidth(newPart, sideWidth)

		// Pad old part to fixed width (use display width)
		paddedOld := oldPart + strings.Repeat(" ", max(0, sideWidth-lipgloss.Width(oldPart)))

		var left, right string
		switch {
		case isSame:
			// Unchanged content - use spaces, no sign
			left = wb.styles.DiffSame.Render("  " + paddedOld)
			right = wb.styles.DiffSame.Render("  " + newPart)
		case oldEmpty:
			// Old side is empty (new has more lines) - use spaces, no sign
			left = "  " + paddedOld
			right = wb.styles.DiffAdd.Render("+ " + newPart)
		case newEmpty:
			// New side is empty (old has more lines) - use spaces, no sign
			left = wb.styles.DiffRemove.Render("- " + paddedOld)
			right = "  " + newPart
		default:
			// Colored style for changed content
			left = wb.styles.DiffRemove.Render("- " + paddedOld)
			right = wb.styles.DiffAdd.Render("+ " + newPart)
		}
		sep := wb.styles.DiffSep.Render("|")
		lines = append(lines, left+" "+sep+" "+right)
	}

	return strings.Join(lines, "\n")
}

// expandTabs converts tabs to spaces, treating tabs as 8-space width
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			// Calculate spaces needed to reach next 8-space boundary
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		} else {
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
}

// truncateByWidth truncates a string to fit within maxDisplayWidth using lipgloss.Width
// which properly handles wide Unicode characters and ANSI escape sequences
func truncateByWidth(s string, maxDisplayWidth int) string {
	if lipgloss.Width(s) <= maxDisplayWidth {
		return s
	}

	// Binary search or incremental build to find truncation point
	var result strings.Builder

	for _, r := range s {
		test := result.String() + string(r)
		w := lipgloss.Width(test)
		if w > maxDisplayWidth-3 { // Reserve space for "..."
			break
		}
		result.WriteRune(r)
	}

	return result.String() + "..."
}
