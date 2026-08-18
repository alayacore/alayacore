// Package theme defines the color palette (Theme) and loading logic.
// It is shared between adapters (terminal TUI, future GUI) so all
// visual frontends use the same theme data and loading mechanism.
package theme

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/alayacore/alayacore/internal/config"
)

//go:embed dark.conf
var darkThemeContent string

//go:embed light.conf
var lightThemeContent string

// Theme holds all color values for the UI.
// Each field maps to a key in the .conf theme files.
type Theme struct {
	// Core palette
	Primary   string `config:"primary" json:"primary"`     // Accent color — selected items, focused borders, highlights
	Dim       string `config:"dim" json:"dim"`             // Dimmed color — unfocused borders, muted text
	Muted     string `config:"muted" json:"muted"`         // Muted color — placeholders, secondary labels
	Text      string `config:"text" json:"text"`           // Primary text color
	Warning   string `config:"warning" json:"warning"`     // Warning color — alerts, caution, confirmations
	Error     string `config:"error" json:"error"`         // Error color — errors, failures
	Selection string `config:"selection" json:"selection"` // Selection highlight — selected list item, search match
	Cursor    string `config:"cursor" json:"cursor"`       // Cursor color — text input cursor

	// Diff colors
	Added   string `config:"added" json:"added"`     // Added lines in diff
	Removed string `config:"removed" json:"removed"` // Removed lines in diff

	// Tool color
	Tool string `config:"tool" json:"tool"` // Tool name — tool call labels in conversation

	// Fold-state arrow glyphs (single codepoint, configurable per theme).
	FoldArrow   string `config:"fold_arrow" json:"fold_arrow"`     // collapsed-window arrow
	UnfoldArrow string `config:"unfold_arrow" json:"unfold_arrow"` // expanded-window arrow
}

var (
	defaultThemeOnce sync.Once
	defaultTheme     *Theme
)

// DefaultTheme returns the default theme (Catppuccin Mocha dark).
// Parsed once from the embedded dark.conf and cached. Returns a copy
// so callers (e.g. LoadTheme) can safely modify the result.
func DefaultTheme() *Theme {
	defaultThemeOnce.Do(func() {
		var t Theme
		if errs := config.ParseKeyValue(darkThemeContent, &t); len(errs) > 0 {
			// Embedded default theme should always be valid. If it fails,
			// the binary is corrupted — surface errors to stderr.
			fmt.Fprintf(os.Stderr, "Error: default theme parse errors: %v\n", errs)
		}
		defaultTheme = &t
	})
	cpy := *defaultTheme
	return &cpy
}

// parseTheme parses theme content into a Theme, starting from the
// defaults and overriding with config values. Returns any parse errors
// (unknown fields, type mismatches) — the theme is still usable with
// default values for the unrecognized fields. Errors are bare (no file
// prefix); callers that know the source file should prefix them.
func parseTheme(content string) (*Theme, []string) {
	var errs []string
	theme := DefaultTheme()
	if parseErrs := config.ParseKeyValue(content, theme); len(parseErrs) > 0 {
		for _, e := range parseErrs {
			errs = append(errs, e.String())
		}
	}
	return theme, errs
}

// LoadTheme loads a theme from a configuration file.
// Returns the loaded theme, any parse errors, or an error
// if the file cannot be read. Parse errors are prefixed with the theme
// file name and are for unknown fields or type mismatches — the theme
// is still usable with default values for the unrecognized fields.
func LoadTheme(path string) (*Theme, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open theme file: %w", err)
	}
	th, errs := parseTheme(string(data))
	for i := range errs {
		errs[i] = fmt.Sprintf("%s: %s", filepath.Base(path), errs[i])
	}
	return th, errs, nil
}
