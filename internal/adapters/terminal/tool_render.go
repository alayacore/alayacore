package terminal

// Tool output parsing and rendering.
// Consolidates all tool-related logic: types, parsing, and rendering.

import (
	"strings"
)

// ============================================================================
// Tool Status
// ============================================================================

// ToolStatus represents the execution status of a tool window.
type ToolStatus int

const (
	ToolStatusNone    ToolStatus = iota // Not yet executing — args still streaming in (dimmed hollow dot)
	ToolStatusSuccess                   // Tool completed successfully (green solid dot)
	ToolStatusError                     // Tool failed (red solid dot)
	ToolStatusPending                   // Executing, awaiting result (primary solid dot)
)

// statusDot returns the plain status dot character and its style, shown
// right after "TOOL" in the header line ("TOOL•").
func (s ToolStatus) statusDot(styles *Styles) (string, Style) {
	switch s {
	case ToolStatusSuccess:
		return "•", NewStyle().Foreground(styles.ColorSuccess)
	case ToolStatusError:
		return "•", NewStyle().Foreground(styles.ColorError)
	case ToolStatusPending:
		return "•", NewStyle().Foreground(styles.ColorAccent)
	default:
		return "·", NewStyle().Foreground(styles.ColorDim)
	}
}

// ============================================================================
// Rendering
// ============================================================================

// RenderDiffContent renders a diff window from its raw Content.
// The Content already has `- `, `+ `, `  ` prefixes. Content is plain
// (no color, no bold); the first line ("tool_name: args") is rendered as
// the bare argument line (no status indicator, no tool-name prefix — both
// live in the header line). innerWidth controls line wrapping; pass 0 to
// disable. Wraps per-line.
func RenderDiffContent(content, name string, innerWidth int) string {
	// Prepare content: strip ANSI and expand tabs before processing
	content = prepareContent(content)
	if name != "" {
		if stripped, ok := strings.CutPrefix(content, name+":"); ok {
			content = strings.TrimSpace(stripped)
		}
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// Header line: "tool_name: args" → show args only, plain.
			if colon := strings.Index(line, ":"); colon >= 0 {
				line = strings.TrimSpace(line[colon+1:])
			}
			if innerWidth > 0 {
				line = wrapContent(line, innerWidth)
			}
			result = append(result, line)
			continue
		}
		if line == "" {
			continue
		}

		// Plain text (diff +/- markers stay as-is, no color).
		if innerWidth > 0 {
			line = wrapContent(line, innerWidth)
		}
		result = append(result, line)
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
