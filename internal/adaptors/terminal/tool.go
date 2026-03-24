package terminal

// Tool output parsing and rendering.
// Consolidates all tool-related logic: types, parsing, and rendering.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ============================================================================
// Tool Status
// ============================================================================

// ToolStatus represents the execution status of a tool window.
type ToolStatus int

const (
	ToolStatusNone    ToolStatus = iota // No status indicator (dimmed hollow dot)
	ToolStatusSuccess                   // Tool completed successfully (green dot)
	ToolStatusError                     // Tool failed (red dot)
	ToolStatusPending                   // Tool is running (dimmed dot)
)

// Indicator returns the styled status indicator string.
func (s ToolStatus) Indicator(styles *Styles) string {
	switch s {
	case ToolStatusSuccess:
		return lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("• ")
	case ToolStatusError:
		return lipgloss.NewStyle().Foreground(styles.ColorError).Render("• ")
	case ToolStatusPending:
		return lipgloss.NewStyle().Foreground(styles.ColorDim).Render("• ")
	default:
		return lipgloss.NewStyle().Foreground(styles.ColorDim).Render("· ")
	}
}

// ParseToolStatus converts a status string to ToolStatus.
func ParseToolStatus(status string) ToolStatus {
	switch status {
	case "success":
		return ToolStatusSuccess
	case "error":
		return ToolStatusError
	case "pending":
		return ToolStatusPending
	default:
		return ToolStatusNone
	}
}

// ============================================================================
// Diff Types
// ============================================================================

// DiffContainer holds two panes side by side for diff display.
type DiffContainer struct {
	Path  string         // file path for header
	Lines []DiffLinePair // raw line pairs
}

// DiffLinePair represents a pair of old/new lines in a diff.
type DiffLinePair struct {
	Old string
	New string
}

// WriteFileContainer holds the path and content for write_file display.
type WriteFileContainer struct {
	Path    string // file path for header
	Content string // file content
}

// ============================================================================
// Parsing (for diff/write_file special rendering)
// ============================================================================

// parseDiffFromFormatted checks if content is an edit_file with raw diff data.
// Returns (path, lines) if it's a raw diff, or ("", nil) otherwise.
func parseDiffFromFormatted(content string) (path string, lines []DiffLinePair) {
	lines = nil // ensure nil on failure

	contentLines := strings.Split(content, "\n")
	if len(contentLines) < 2 {
		return "", nil
	}

	// Check first line is "edit_file: <path>"
	if !strings.HasPrefix(contentLines[0], "edit_file: ") {
		return "", nil
	}
	path = strings.TrimPrefix(contentLines[0], "edit_file: ")

	// Check if remaining lines have raw diff format (\x00 prefix)
	diffLines := make([]DiffLinePair, 0, len(contentLines)-1)
	for _, line := range contentLines[1:] {
		if !strings.HasPrefix(line, "\x00") {
			return "", nil
		}
		// Parse: \x00old\x00new
		parts := strings.SplitN(line[1:], "\x00", 2)
		if len(parts) != 2 {
			return "", nil
		}
		diffLines = append(diffLines, DiffLinePair{
			Old: parts[0],
			New: parts[1],
		})
	}

	if len(diffLines) == 0 {
		return "", nil
	}

	return path, diffLines
}

// parseWriteFileFromFormatted checks if content is a write_file with path and content.
// Returns (path, content, true) if it's a write_file, or ("", "", false) otherwise.
func parseWriteFileFromFormatted(content string) (path string, fileContent string, ok bool) {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) < 2 {
		return "", "", false
	}

	// Check first line is "write_file: <path>"
	if !strings.HasPrefix(lines[0], "write_file: ") {
		return "", "", false
	}
	path = strings.TrimPrefix(lines[0], "write_file: ")
	return path, lines[1], true
}

// ============================================================================
// Stream ID Parsing (for text deltas and status updates)
// ============================================================================

// ParseStreamID extracts stream ID prefix from value.
// Format: "[:id:]content". Returns id, content, true if prefix found.
func ParseStreamID(value string) (id string, content string, ok bool) {
	const prefixStart = "[:"
	const prefixEnd = ":]"
	if !strings.HasPrefix(value, prefixStart) {
		return "", value, false
	}
	endIdx := strings.Index(value, prefixEnd)
	if endIdx == -1 {
		return "", value, false
	}
	id = value[len(prefixStart):endIdx]
	content = value[endIdx+len(prefixEnd):]
	return id, content, true
}

// ============================================================================
// Rendering
// ============================================================================

// ColorizeTool applies tool-specific styling to tool output.
func ColorizeTool(value string, styles *Styles) string {
	// Expand tabs BEFORE styling to ensure correct column counting
	value = expandTabs(value)

	lines := strings.Split(value, "\n")
	if len(lines) == 1 {
		return colorizeSingleLineTool(value, styles)
	}
	return colorizeMultiLineTool(lines, styles)
}

func colorizeSingleLineTool(value string, styles *Styles) string {
	colonIdx := strings.Index(value, ":")
	if colonIdx > 0 {
		toolName := value[:colonIdx]
		rest := value[colonIdx:]
		return styles.Tool.Render(toolName) + styles.ToolContent.Render(rest)
	}
	return styles.Tool.Render(value)
}

func colorizeMultiLineTool(lines []string, styles *Styles) string {
	var result strings.Builder
	firstLine := lines[0]
	colonIdx := strings.Index(firstLine, ":")

	if colonIdx > 0 {
		toolName := firstLine[:colonIdx]
		restFirst := firstLine[colonIdx:]
		result.WriteString(styles.Tool.Render(toolName))
		result.WriteString(styles.ToolContent.Render(restFirst))
	} else {
		result.WriteString(styles.Tool.Render(firstLine))
	}

	for _, line := range lines[1:] {
		result.WriteString("\n")
		// Fallback for other lines
		switch {
		case strings.HasPrefix(line, "- "):
			result.WriteString(styles.DiffRemove.Render(line))
		case strings.HasPrefix(line, "+ "):
			result.WriteString(styles.DiffAdd.Render(line))
		default:
			result.WriteString(styles.ToolContent.Render(line))
		}
	}
	return result.String()
}

// RenderWriteFileContent renders a write_file container.
func RenderWriteFileContent(wf *WriteFileContainer, status ToolStatus, styles *Styles) string {
	lines := make([]string, 0, 2)

	header := status.Indicator(styles) + styles.Tool.Render("write_file: ") + styles.ToolContent.Render(wf.Path)
	lines = append(lines, header)

	// Render content lines with Text style
	contentLines := strings.Split(wf.Content, "\n")
	for _, line := range contentLines {
		lines = append(lines, styles.Text.Render(expandTabs(line)))
	}

	return strings.Join(lines, "\n")
}

// RenderDiffContent renders a diff container in unified diff style.
func RenderDiffContent(diff *DiffContainer, status ToolStatus, styles *Styles) string {
	lines := make([]string, 0, 1+len(diff.Lines))

	header := status.Indicator(styles) + styles.Tool.Render("edit_file: ") + styles.ToolContent.Render(diff.Path)
	lines = append(lines, header)

	for _, pair := range diff.Lines {
		oldPart := strings.ReplaceAll(expandTabs(pair.Old), "\n", "\\n")
		newPart := strings.ReplaceAll(expandTabs(pair.New), "\n", "\\n")

		oldEmpty := pair.Old == ""
		newEmpty := pair.New == ""
		isSame := pair.Old == pair.New

		switch {
		case isSame:
			lines = append(lines, styles.Text.Render("  "+oldPart))
		case oldEmpty:
			lines = append(lines, styles.DiffAdd.Render("+ "+newPart))
		case newEmpty:
			lines = append(lines, styles.DiffRemove.Render("- "+oldPart))
		default:
			lines = append(lines, styles.DiffRemove.Render("- "+oldPart))
			lines = append(lines, styles.DiffAdd.Render("+ "+newPart))
		}
	}

	return strings.Join(lines, "\n")
}

// expandTabs converts tabs to spaces, treating tabs as 8-space width.
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		} else if r == '\n' {
			result.WriteRune(r)
			col = 0
		} else {
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
}
