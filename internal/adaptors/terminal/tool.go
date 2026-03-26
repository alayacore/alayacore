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
// Assumes content has already been prepared (ANSI stripped, tabs expanded).
func ColorizeTool(value string, styles *Styles) string {
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
		// Content lines use Text style for readability
		// Note: Diff coloring is handled by RenderDiffContent for edit_file windows
		result.WriteString(styles.Text.Render(line))
	}
	return result.String()
}

// RenderDiffContent renders a diff window from its raw Content.
// The Content already has `- `, `+ `, `  ` prefixes - we just apply colors.
func RenderDiffContent(content string, status ToolStatus, styles *Styles) string {
	// Prepare content: strip ANSI and expand tabs before processing
	content = prepareContent(content)

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// Header line: "edit_file: /path"
			// Need to re-render with status indicator
			path := strings.TrimPrefix(line, "edit_file: ")
			result = append(result, status.Indicator(styles)+styles.Tool.Render("edit_file: ")+styles.ToolContent.Render(path))
			continue
		}
		if line == "" {
			continue
		}

		// Apply color based on prefix
		switch {
		case strings.HasPrefix(line, "- "):
			result = append(result, styles.DiffRemove.Render(line))
		case strings.HasPrefix(line, "+ "):
			result = append(result, styles.DiffAdd.Render(line))
		default:
			// Unchanged line (starts with "  ") or anything else
			result = append(result, styles.Text.Render(line))
		}
	}

	return strings.Join(result, "\n")
}

// prepareContent normalizes content for rendering by stripping ANSI escape
// sequences and expanding tabs to spaces (8-space width).
func prepareContent(s string) string {
	// Strip ANSI codes first - this prevents escape sequences from interfering
	// with rendering, column counting, and lipgloss styling
	s = stripANSI(s)

	var result strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		case '\n':
			result.WriteRune(r)
			col = 0
		default:
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
}

// stripANSI removes ANSI escape sequences and normalizes carriage returns.
// This prevents tool output from interfering with terminal rendering.
func stripANSI(s string) string {
	// Fast path: no escape sequences
	if !strings.Contains(s, "\x1b") && !strings.Contains(s, "\r") {
		return s
	}

	var result strings.Builder
	result.Grow(len(s))

	i := 0
	for i < len(s) {
		// Handle carriage return
		if s[i] == '\r' {
			// Replace with newline (handles progress bars)
			result.WriteByte('\n')
			i++
			continue
		}

		// Handle ANSI escape sequence
		if s[i] == 0x1b && i+1 < len(s) {
			next := s[i+1]

			// CSI sequence: ESC [ <params> <command>
			if next == '[' {
				i += 2
				// Skip until we find the command character (0x40-0x7E)
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
					i++
				}
				if i < len(s) {
					i++ // Skip the command character
				}
				continue
			}

			// OSC sequence: ESC ] ... BEL or ESC ] ... ST
			if next == ']' {
				i += 2
				// Skip until BEL (0x07) or ST (ESC \)
				for i < len(s) {
					if s[i] == 0x07 {
						i++
						break
					}
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}

			// Other escape sequences: skip ESC and next char
			i += 2
			continue
		}

		// Regular character
		result.WriteByte(s[i])
		i++
	}

	return result.String()
}
