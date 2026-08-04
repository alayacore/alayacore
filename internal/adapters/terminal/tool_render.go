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
	case ToolStatusNone:
		return lipgloss.NewStyle().Foreground(styles.ColorDim).Render("· ")
	}
	return ""
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
// innerWidth controls line wrapping; pass 0 to disable wrapping.
// Wraps per-line to preserve diff coloring on wrapped continuations.
func RenderDiffContent(content string, status ToolStatus, styles *Styles, innerWidth int) string {
	// Prepare content: strip ANSI and expand tabs before processing
	content = prepareContent(content)

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// Header line: "tool_name: args"
			// Re-render with status indicator prefix
			header := status.Indicator(styles) + colorizeSingleLineTool(line, styles)
			if innerWidth > 0 {
				header = wrapContent(header, innerWidth)
			}
			result = append(result, header)
			continue
		}
		if line == "" {
			continue
		}

		// Apply color based on prefix
		var styled string
		switch {
		case strings.HasPrefix(line, "- "):
			styled = styles.DiffRemove.Render(line)
		case strings.HasPrefix(line, "+ "):
			styled = styles.DiffAdd.Render(line)
		default:
			// Unchanged line (starts with "  ") or anything else
			styled = styles.Text.Render(line)
		}
		if innerWidth > 0 {
			styled = wrapContent(styled, innerWidth)
		}
		result = append(result, styled)
	}

	return strings.Join(result, "\n")
}

// prepareContent normalizes content for rendering by stripping ANSI escape
// sequences and expanding tabs to spaces (8-space width).
func prepareContent(s string) string {
	s = stripANSI(s)
	s = expandTabs(s)
	return s
}

// expandTabs converts tabs to spaces, treating tabs as TabWidth-space width.
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			next := ((col / TabWidth) + 1) * TabWidth
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
		if s[i] == '\r' {
			// Replace carriage return with newline (handles progress bars)
			result.WriteByte('\n')
			i++
			continue
		}

		if s[i] == 0x1b && i+1 < len(s) {
			i = stripANSISequence(s, i, &result)
			continue
		}

		// Regular character
		result.WriteByte(s[i])
		i++
	}

	return result.String()
}

// stripANSISequence handles an ANSI escape sequence starting at position i.
// Returns the new position after the sequence.
func stripANSISequence(s string, i int, _ *strings.Builder) int {
	next := s[i+1]

	// CSI sequence: ESC [ <params> <command>
	if next == '[' {
		return skipUntilCommand(s, i+2)
	}

	// OSC sequence: ESC ] ... BEL or ESC ] ... ST
	if next == ']' {
		return skipUntilTerminator(s, i+2)
	}

	// Other escape sequences: skip ESC and next char
	return i + 2
}

// skipUntilCommand skips CSI parameters until the command character (0x40-0x7E).
func skipUntilCommand(s string, i int) int {
	for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
		i++
	}
	if i < len(s) {
		i++ // Skip the command character
	}
	return i
}

// skipUntilTerminator skips OSC sequence until BEL (0x07) or ST (ESC \).
func skipUntilTerminator(s string, i int) int {
	for i < len(s) {
		if s[i] == 0x07 {
			return i + 1
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return i
}
