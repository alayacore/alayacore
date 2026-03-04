package adaptors

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// wordwrap breaks text to fit the given width
func wordwrap(text string, width int) string {
	if width <= 0 || text == "" {
		return text
	}

	var result strings.Builder

	for line := range strings.SplitSeq(text, "\n") {
		// Extract escape sequences prefix (styling) at start of line
		prefix := extractEscapePrefix(line)
		remaining := line[len(prefix):]
		suffix := extractEscapeSuffix(remaining)
		middle := remaining[:len(remaining)-len(suffix)]

		if lipgloss.Width(line) <= width {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// Break middle at width limit - simple character-by-character without word boundaries
		for len(middle) > 0 {
			breakAt := 0
			currentWidth := 0

			for breakAt < len(middle) {
				// Skip escape sequences in the middle (shouldn't happen but just in case)
				skip := skipEscapeSequence(middle[breakAt:])
				if skip > 0 {
					breakAt += skip
					continue
				}

				r := rune(middle[breakAt])
				charWidth := lipgloss.Width(string(r))

				if currentWidth+charWidth > width {
					break
				}
				currentWidth += charWidth
				breakAt++
			}

			// Ensure we make progress even if the first character exceeds width
			if breakAt == 0 {
				breakAt = 1
			}

			// Write prefix + this segment + suffix
			result.WriteString(prefix)
			result.WriteString(middle[:breakAt])
			result.WriteString(suffix)
			result.WriteString("\n")
			middle = middle[breakAt:]
		}
	}

	return result.String()
}

// skipEscapeSequence returns the length of an ANSI escape sequence at the start of s,
// or 0 if there is no escape sequence.
func skipEscapeSequence(s string) int {
	if len(s) == 0 || s[0] != '\x1b' {
		return 0
	}
	if len(s) < 2 {
		return 0
	}

	switch s[1] {
	case '[':
		return skipCSI(s)
	case ']':
		return skipOSC(s)
	default:
		return 2
	}
}

// skipCSI skips a CSI (Control Sequence Introducer) sequence: ESC [ ... <final byte>
// Final byte is in range 0x40-0x7E (@A-Z[\]^_`a-z{|}~)
func skipCSI(s string) int {
	if len(s) < 3 {
		return len(s)
	}

	pos := 2
	for pos < len(s) {
		c := s[pos]

		if c >= 0x40 && c <= 0x7E {
			return pos + 1
		}

		if c >= 0x20 && c <= 0x3F {
			pos++
		} else {
			break
		}
	}

	return pos
}

// skipOSC skips an OSC (Operating System Command) sequence: ESC ] ... ST
// ST (String Terminator) is either BEL (\x07) or ESC \ (\x1b\\)
func skipOSC(s string) int {
	if len(s) < 3 {
		return len(s)
	}

	pos := 2
	for pos < len(s) {
		c := s[pos]

		if c == '\x07' {
			return pos + 1
		}

		if c == '\x1b' && pos+1 < len(s) && s[pos+1] == '\\' {
			return pos + 2
		}

		pos++
	}

	return pos
}

// extractEscapePrefix returns all consecutive ANSI escape sequences at the start of s.
func extractEscapePrefix(s string) string {
	var prefix strings.Builder
	i := 0
	for i < len(s) {
		skip := skipEscapeSequence(s[i:])
		if skip == 0 {
			break
		}
		prefix.WriteString(s[i : i+skip])
		i += skip
	}
	return prefix.String()
}

// extractEscapeSuffix returns all consecutive ANSI escape sequences at the end of s.
func extractEscapeSuffix(s string) string {
	var sequences []string
	i := len(s)
	for i > 0 {
		// Look for escape sequence ending at i
		found := false
		// Escape sequences are at most 30 bytes
		maxLookback := min(30, i)
		for start := i - 1; start >= i-maxLookback; start-- {
			if s[start] == '\x1b' {
				skip := skipEscapeSequence(s[start:])
				if skip > 0 && start+skip == i {
					// Found escape sequence
					sequences = append(sequences, s[start:i])
					i = start
					found = true
					break
				}
			}
		}
		if !found {
			break
		}
	}
	// Build suffix from first sequence to last (since we collected from end to start)
	var suffix strings.Builder
	for j := len(sequences) - 1; j >= 0; j-- {
		suffix.WriteString(sequences[j])
	}
	return suffix.String()
}
