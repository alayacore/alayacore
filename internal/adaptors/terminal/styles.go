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
	Primary   string `config:"primary"`   // Primary/accent color for highlights and focused borders
	Dim       string `config:"dim"`       // Dimmed color for unfocused borders and blurred text
	Muted     string `config:"muted"`     // Muted color for placeholder and secondary text
	Text      string `config:"text"`      // Primary text color
	Warning   string `config:"warning"`   // Warning color (yellow/orange)
	Error     string `config:"error"`     // Error color (red)
	Success   string `config:"success"`   // Success color (green)
	Selection string `config:"selection"` // Selection/cursor border highlight color
	Cursor    string `config:"cursor"`    // Text input cursor color

	// Diff colors
	Added   string `config:"added"`   // Added lines in diff (green)
	Removed string `config:"removed"` // Removed lines in diff (red)
}

// DefaultTheme returns the default theme (Catppuccin Mocha)
func DefaultTheme() *Theme {
	return &Theme{
		Primary:   "#89d4fa",
		Dim:       "#313244",
		Muted:     "#6c7086",
		Text:      "#cdd6f4",
		Warning:   "#f9e2af",
		Error:     "#f38ba8",
		Success:   "#a6e3a1",
		Selection: "#fab387",
		Cursor:    "#cdd6f4",
		Added:     "#a6e3a1",
		Removed:   "#f38ba8",
	}
}

// LoadTheme loads a theme from a configuration file
// Returns the loaded theme or an error if the file cannot be read or parsed
func LoadTheme(path string) (*Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open theme file: %w", err)
	}

	// Start with defaults, then override with config values
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
		// If explicit path was given but failed, buffer warning but continue
		AddWarning("Warning: failed to load theme from %s: %v", explicitPath, err)
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
			AddWarning("Warning: failed to load theme from %s: %v", defaultPath, err)
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

	// Display styles
	Input       lipgloss.Style
	Status      lipgloss.Style
	Confirm     lipgloss.Style
	InputBorder lipgloss.Style

	// Component-specific colors (exposed as color.Color for dynamic use)
	// Border colors
	BorderFocused color.Color
	BorderBlurred color.Color
	BorderCursor  color.Color

	// Text colors for dynamic use
	ColorAccent  color.Color
	ColorDim     color.Color
	ColorMuted   color.Color
	ColorError   color.Color
	ColorSuccess color.Color
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
		UserInput:   baseStyle.Foreground(lipgloss.Color(theme.Primary)).Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color(theme.Warning)),
		ToolContent: baseStyle.Foreground(lipgloss.Color(theme.Muted)),
		Reasoning:   baseStyle.Foreground(lipgloss.Color(theme.Muted)).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color(theme.Error)),
		System:      baseStyle.Foreground(lipgloss.Color(theme.Muted)),
		Prompt:      baseStyle.Foreground(lipgloss.Color(theme.Primary)).Bold(true),
		DiffRemove:  baseStyle.Foreground(lipgloss.Color(theme.Removed)),
		DiffAdd:     baseStyle.Foreground(lipgloss.Color(theme.Added)),

		// Display styles
		Input:       baseStyle,
		Status:      baseStyle.Foreground(lipgloss.Color(theme.Dim)),
		Confirm:     baseStyle.Foreground(lipgloss.Color(theme.Error)).Bold(true),
		InputBorder: baseStyle.Border(lipgloss.RoundedBorder()),

		// Component-specific colors
		BorderFocused: lipgloss.Color(theme.Primary),
		BorderBlurred: lipgloss.Color(theme.Dim),
		BorderCursor:  lipgloss.Color(theme.Selection),

		ColorAccent:  lipgloss.Color(theme.Primary),
		ColorDim:     lipgloss.Color(theme.Dim),
		ColorMuted:   lipgloss.Color(theme.Muted),
		ColorError:   lipgloss.Color(theme.Error),
		ColorSuccess: lipgloss.Color(theme.Success),
		CursorColor:  lipgloss.Color(theme.Cursor),
	}
}

// DefaultStyles returns the default styling configuration
// Deprecated: Use NewStyles with a Theme instead
func DefaultStyles() *Styles {
	return NewStyles(DefaultTheme())
}
