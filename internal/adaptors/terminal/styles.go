package terminal

// Theme and styling for the terminal UI.
// This file defines the color palette (Theme) and derived styles (Styles).

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/alayacore/alayacore/internal/config"
)

// ============================================================================
// Theme - Color Palette
// ============================================================================

// Theme holds all color values for the terminal UI
type Theme struct {
	// Core palette
	Base     string `config:"base"`     // Background color - used for invisible borders
	Surface1 string `config:"surface1"` // Surface color - used for subtle backgrounds
	Accent   string `config:"accent"`   // Primary accent color (blue) - used for focused borders, prompts
	Dim      string `config:"dim"`      // Dimmed color - used for unfocused borders, blurred text
	Muted    string `config:"muted"`    // Muted color - used for placeholder text, system messages
	Text     string `config:"text"`     // Primary text color (white)
	Warning  string `config:"warning"`  // Warning/accent color (yellow)
	Error    string `config:"error"`    // Error color (red)
	Success  string `config:"success"`  // Success color (green)
	Peach    string `config:"peach"`    // Peach color - used for window cursor border highlight
	Cursor   string `config:"cursor"`   // Cursor color - used for text input cursor

	// Diff colors
	DiffAdd    string `config:"diff_add"`    // Diff added line color (green)
	DiffRemove string `config:"diff_remove"` // Diff removed line color (red)
}

// DefaultTheme returns the default theme (Catppuccin Mocha)
func DefaultTheme() *Theme {
	return &Theme{
		Base:       "#1e1e2e",
		Surface1:   "#585b70",
		Accent:     "#89d4fa",
		Dim:        "#45475a",
		Muted:      "#6c7086",
		Text:       "#cdd6f4",
		Warning:    "#f9e2af",
		Error:      "#f38ba8",
		Success:    "#a6e3a1",
		Peach:      "#fab387",
		Cursor:     "#cdd6f4", // Light gray/white for visibility on dark backgrounds
		DiffAdd:    "#a6e3a1", // Green for added lines
		DiffRemove: "#f38ba8", // Red for removed lines
	}
}

// LoadTheme loads a theme from a configuration file
// Returns the loaded theme or an error if the file cannot be read or parsed
func LoadTheme(path string) (*Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open theme file: %w", err)
	}

	theme := DefaultTheme()
	config.ParseKeyValue(string(data), theme)
	return theme, nil
}

// LoadThemeFromPaths tries to load a theme from multiple paths in priority order
// Returns the first successfully loaded theme, or the default theme if none found
func LoadThemeFromPaths(explicitPath string) *Theme {
	// Try explicit path first (highest priority)
	if explicitPath != "" {
		theme, err := LoadTheme(explicitPath)
		if err == nil {
			return theme
		}
		// If explicit path was given but failed, print warning but continue
		fmt.Fprintf(os.Stderr, "Warning: failed to load theme from %s: %v\n", explicitPath, err)
	}

	// Try default user theme path
	homeDir, err := os.UserHomeDir()
	if err == nil {
		defaultPath := filepath.Join(homeDir, ".alayacore", "theme.conf")
		if _, err := os.Stat(defaultPath); err == nil {
			theme, err := LoadTheme(defaultPath)
			if err == nil {
				return theme
			}
			fmt.Fprintf(os.Stderr, "Warning: failed to load theme from %s: %v\n", defaultPath, err)
		}
	}

	// Fallback to default theme
	return DefaultTheme()
}

// ============================================================================
// Styles - Derived Lipgloss Styles
// ============================================================================

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
