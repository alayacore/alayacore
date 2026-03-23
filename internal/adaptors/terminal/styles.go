package terminal

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Styles holds all lipgloss styles for the terminal UI
type Styles struct {
	// Output text styles
	Text        lipgloss.Style
	UserInput   lipgloss.Style
	Tool        lipgloss.Style
	ToolContent lipgloss.Style
	Reasoning   lipgloss.Style
	Error       lipgloss.Style
	System      lipgloss.Style
	Prompt      lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffAdd     lipgloss.Style
	DiffSep     lipgloss.Style // dimmed separator |

	// Display styles
	Input       lipgloss.Style
	Status      lipgloss.Style
	Confirm     lipgloss.Style
	InputBorder lipgloss.Style

	// Component-specific colors (exposed as color.Color for dynamic use)
	// Border colors
	BorderFocused color.Color
	BorderBlurred color.Color
	BorderDimmed  color.Color
	BorderCursor  color.Color

	// Text colors for dynamic use
	ColorAccent  color.Color
	ColorDim     color.Color
	ColorMuted   color.Color
	ColorError   color.Color
	ColorSuccess color.Color
	ColorBase    color.Color
	CursorColor  color.Color
}

// RenderBorderedBox renders content with consistent border, padding, and width.
// This ensures all bordered boxes (input, model selector, queue manager) have the same width.
// The width calculation is: borderStyle.Padding(0, 1).Render(innerStyle.Width(width-4).Render(content))
func (s *Styles) RenderBorderedBox(content string, width int, borderColor color.Color, height ...int) string {
	borderStyle := s.InputBorder.
		BorderForeground(borderColor).
		Padding(0, 1)

	innerStyle := s.Input.Width(max(0, width-4))
	if len(height) > 0 {
		innerStyle = innerStyle.Height(height[0])
	}

	return borderStyle.Render(innerStyle.Render(content))
}

// NewStyles creates a Styles instance from a Theme
func NewStyles(theme *Theme) *Styles {
	baseStyle := lipgloss.NewStyle()
	return &Styles{
		// Output text styles
		Text:        baseStyle.Foreground(lipgloss.Color(theme.Text)).Bold(true),
		UserInput:   baseStyle.Foreground(lipgloss.Color(theme.Accent)).Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color(theme.Warning)),
		ToolContent: baseStyle.Foreground(lipgloss.Color(theme.Muted)),
		Reasoning:   baseStyle.Foreground(lipgloss.Color(theme.Muted)).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color(theme.Error)),
		System:      baseStyle.Foreground(lipgloss.Color(theme.Muted)),
		Prompt:      baseStyle.Foreground(lipgloss.Color(theme.Accent)).Bold(true),
		DiffRemove:  baseStyle.Foreground(lipgloss.Color(theme.DiffRemove)),
		DiffAdd:     baseStyle.Foreground(lipgloss.Color(theme.DiffAdd)),
		DiffSep:     baseStyle.Foreground(lipgloss.Color(theme.Base)),

		// Display styles
		Input:       baseStyle,
		Status:      baseStyle.Foreground(lipgloss.Color(theme.Dim)),
		Confirm:     baseStyle.Foreground(lipgloss.Color(theme.Error)).Bold(true),
		InputBorder: baseStyle.Border(lipgloss.RoundedBorder()),

		// Component-specific colors
		BorderFocused: lipgloss.Color(theme.Accent),
		BorderBlurred: lipgloss.Color(theme.Dim),
		BorderDimmed:  lipgloss.Color(theme.Base),
		BorderCursor:  lipgloss.Color(theme.Peach),

		ColorAccent:  lipgloss.Color(theme.Accent),
		ColorDim:     lipgloss.Color(theme.Dim),
		ColorMuted:   lipgloss.Color(theme.Muted),
		ColorError:   lipgloss.Color(theme.Error),
		ColorSuccess: lipgloss.Color(theme.Success),
		ColorBase:    lipgloss.Color(theme.Base),
		CursorColor:  lipgloss.Color(theme.Cursor),
	}
}

// DefaultStyles returns the default styling configuration
// Deprecated: Use NewStyles with a Theme instead
func DefaultStyles() *Styles {
	return NewStyles(DefaultTheme())
}
