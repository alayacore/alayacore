package common

import (
	_ "embed"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/config"
)

//go:embed welcome.txt
var welcomeText string

// WelcomeText returns the welcome text with version appended at the bottom-right
// edge of the ASCII art (not the terminal width).
func WelcomeText() string {
	lines := strings.Split(welcomeText, "\n")
	if len(lines) == 0 {
		return welcomeText
	}

	// Find the widest line without trailing spaces (the actual rightmost column).
	// Use display width (not byte length) to avoid misalignment with non-ASCII.
	maxWidth := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		w := lipgloss.Width(trimmed)
		if w > maxWidth {
			maxWidth = w
		}
	}

	// Find the last line with content
	lastContentLine := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimRight(lines[i], " ")
		if len(trimmed) > 0 {
			lastContentLine = i
			break
		}
	}

	// Add version to the last content line, aligned to rightmost column with gap
	if lastContentLine >= 0 {
		version := fmt.Sprintf("v%s", config.Version)
		currentLine := lines[lastContentLine]
		trimmedLine := strings.TrimRight(currentLine, " ")
		// Right-align version to the ASCII art's right edge.
		// If the line is shorter than the art width, pad out so the version ends at maxWidth.
		targetColumn := maxWidth - lipgloss.Width(version)
		if targetColumn < 1 {
			// Fallback when the art is narrower than the version.
			targetColumn = lipgloss.Width(trimmedLine) + 1
		}
		paddingLen := targetColumn - lipgloss.Width(trimmedLine)
		if paddingLen < 1 {
			paddingLen = 1
		}
		padding := strings.Repeat(" ", paddingLen)
		lines[lastContentLine] = trimmedLine + padding + version
	}

	return strings.Join(lines, "\n")
}
