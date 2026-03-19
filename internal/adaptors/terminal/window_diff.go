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

// renderDiffContent renders a diff container in unified diff style
func (wb *WindowBuffer) renderDiffContent(diff *DiffContainer, innerWidth int, status string) string {
	// Preallocate lines: header + diff lines
	lines := make([]string, 0, 1+len(diff.Lines))

	// Add header with file path and status indicator
	var indicator string
	if status == "success" {
		indicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorSuccess)).
			Render("• ")
	} else if status == "error" {
		indicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorError)).
			Render("• ")
	} else if status == "pending" {
		indicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorDim)).
			Render("• ")
	} else {
		indicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorDim)).
			Render("· ")
	}
	header := indicator + wb.styles.Tool.Render("edit_file: ") + wb.styles.ToolContent.Render(diff.Path)
	lines = append(lines, header)

	// Calculate available width for content (subtract prefix width: 2 for "- " or "+ ")
	contentWidth := innerWidth - 2
	if contentWidth < 10 {
		contentWidth = 10 // minimum width
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
		oldPart = truncateByWidth(oldPart, contentWidth)
		newPart = truncateByWidth(newPart, contentWidth)

		switch {
		case isSame:
			// Unchanged content - show with space prefix
			lines = append(lines, "  "+oldPart)
		case oldEmpty:
			// Old side is empty (added line) - show + with green
			lines = append(lines, wb.styles.DiffAdd.Render("+ "+newPart))
		case newEmpty:
			// New side is empty (removed line) - show - with red
			lines = append(lines, wb.styles.DiffRemove.Render("- "+oldPart))
		default:
			// Both sides differ: show - first, then +
			lines = append(lines, wb.styles.DiffRemove.Render("- "+oldPart))
			lines = append(lines, wb.styles.DiffAdd.Render("+ "+newPart))
		}
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
