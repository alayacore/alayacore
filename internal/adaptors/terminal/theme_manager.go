package terminal

// ThemeManager manages theme loading from a themes folder.
// It loads theme files (*.conf) from a specified directory and provides
// theme switching functionality.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ThemeInfo represents a theme's metadata for display in the selector.
type ThemeInfo struct {
	Name string // Theme name (filename without .conf extension)
	Path string // Full path to the theme file
}

// ThemeManager handles theme loading and management.
type ThemeManager struct {
	themesFolder string
	themes       []ThemeInfo
}

// NewThemeManager creates a new theme manager.
// If themesFolder is empty, it defaults to ~/.alayacore/themes.
func NewThemeManager(themesFolder string) *ThemeManager {
	tm := &ThemeManager{
		themesFolder: themesFolder,
	}

	// Set default folder if not provided
	if tm.themesFolder == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			tm.themesFolder = filepath.Join(home, ".alayacore", "themes")
		}
	}

	// Load theme list
	tm.ReloadThemes()

	return tm
}

// ReloadThemes reloads the list of available themes from the themes folder.
func (tm *ThemeManager) ReloadThemes() {
	tm.themes = nil

	if tm.themesFolder == "" {
		return
	}

	// Read directory
	entries, err := os.ReadDir(tm.themesFolder)
	if err != nil {
		// Folder doesn't exist or can't be read - that's OK
		return
	}

	// Find all .conf files
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") {
			continue
		}

		// Strip .conf extension to get theme name
		themeName := strings.TrimSuffix(name, ".conf")

		tm.themes = append(tm.themes, ThemeInfo{
			Name: themeName,
			Path: filepath.Join(tm.themesFolder, name),
		})
	}

	// Sort themes alphabetically
	sort.Slice(tm.themes, func(i, j int) bool {
		return tm.themes[i].Name < tm.themes[j].Name
	})
}

// GetThemes returns the list of available themes.
func (tm *ThemeManager) GetThemes() []ThemeInfo {
	return tm.themes
}

// GetThemesFolder returns the themes folder path.
func (tm *ThemeManager) GetThemesFolder() string {
	return tm.themesFolder
}

// LoadTheme loads a theme by name.
// If the theme doesn't exist or name is empty, returns the default theme.
func (tm *ThemeManager) LoadTheme(name string) *Theme {
	if name == "" {
		return DefaultTheme()
	}

	// Find the theme
	for _, theme := range tm.themes {
		if theme.Name == name {
			loaded, err := LoadTheme(theme.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to load theme %s: %v\n", name, err)
				return DefaultTheme()
			}
			return loaded
		}
	}

	// Theme not found
	fmt.Fprintf(os.Stderr, "Warning: theme %s not found, using default\n", name)
	return DefaultTheme()
}

// ThemeExists checks if a theme with the given name exists.
func (tm *ThemeManager) ThemeExists(name string) bool {
	for _, theme := range tm.themes {
		if theme.Name == name {
			return true
		}
	}
	return false
}
