package terminal

// Tool output parsing and rendering.
// Consolidates all tool-related logic: types, parsing, and rendering.

import (
	"strings"
	"time"
)

// ============================================================================
// Tool Status
// ============================================================================

// ToolStatus represents the execution status of a tool window.
type ToolStatus int

const (
	ToolStatusNone    ToolStatus = iota // Arguments still streaming in (spinner)
	ToolStatusSuccess                   // Tool completed successfully (plain ✓)
	ToolStatusError                     // Tool failed (plain ✗)
	ToolStatusPending                   // Executing, awaiting result (spinner)
)

// toolSpinnerFrames is the braille dot-segment rotation also used by the
// session-loading screen. While arguments stream in or the tool executes,
// the header shows the current frame in place of a static dot.
var toolSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// toolSpinnerFrame returns the spinner frame for the current moment. The
// frame advances with each re-render: the tool window's border cache is
// rebuilt on every delta append and status change, so the indicator
// rotates together with the delta refresh (no separate timer).
func toolSpinnerFrame() string {
	return toolSpinnerFrames[int(time.Now().UnixMilli()/150)%len(toolSpinnerFrames)]
}

// toolHeaderLabel is the fixed tool-window label shown in the header line.
// The status indicator (spinner while running, ✓/✗ when done) follows
// after one separating space: "TOOL CALL ⠋ execute_command".
const toolHeaderLabel = "TOOL CALL"

// toolLabelSep is the single space between the tool label and its status
// indicator ("TOOL CALL ⠋", "TOOL CALL ✓").
const toolLabelSep = " "

// toolLabelWithIndicator returns the fixed label-column text for a tool
// window: the label + separator space + the indicator glyph.
func toolLabelWithIndicator(dot string) string {
	return toolHeaderLabel + toolLabelSep + dot
}

// statusDot returns the tool status indicator character and its style,
// shown right after the "TOOL CALL" label in the header line
// ("TOOL CALL ⠋", "TOOL CALL ✓"). The indicator is deliberately colorless —
// the spinner replaces the old colored dots while the tool is running,
// and the result glyphs (✓/✗) render in the default foreground instead of
// green/red.
func (s ToolStatus) statusDot() (string, Style) {
	switch s {
	case ToolStatusSuccess:
		return "✓", NewStyle()
	case ToolStatusError:
		return "✗", NewStyle()
	case ToolStatusPending:
		return toolSpinnerFrame(), NewStyle()
	default: // ToolStatusNone — arguments still streaming in
		return toolSpinnerFrame(), NewStyle()
	}
}

// ============================================================================
// Rendering
// ============================================================================

// RenderDiffContent prepares a diff window's raw Content: the Content
// already has `- `, `+ `, `  ` prefixes. Removed rows (`- `) are colored
// with styles.DiffRemove, added rows (`+ `) with styles.DiffAdd; context
// rows and the first line stay plain. The first line ("tool_name: args")
// is rendered as the bare argument line (no status indicator, no
// tool-name prefix — both live in the header line). styles may be nil —
// the diff then renders plain. No wrapping is performed here — the caller
// wraps the combined window content once (wrapVisualLines) so
// original-line boundaries stay hard newlines and only over-long single
// lines soft-wrap (wrapContent re-applies the per-line color on wrapped
// continuations).
func RenderDiffContent(content, name string, styles *Styles) string {
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

	for i, line := range lines {
		if i == 0 {
			// Header line: "tool_name: args" → show args only, plain.
			if colon := strings.Index(line, ":"); colon >= 0 {
				line = strings.TrimSpace(line[colon+1:])
			}
			lines[i] = line
			continue
		}
		if line == "" {
			continue
		}
		// Diff rows keep their -/+ markers; changed rows carry their diff
		// colors (removed red, added green), context rows stay plain.
		switch {
		case styles != nil && strings.HasPrefix(line, "- "):
			lines[i] = styles.DiffRemove.Render(line)
		case styles != nil && strings.HasPrefix(line, "+ "):
			lines[i] = styles.DiffAdd.Render(line)
		default:
			lines[i] = line
		}
	}

	return strings.Join(lines, "\n")
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
