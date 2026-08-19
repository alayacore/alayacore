package theme

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Info represents a theme's metadata.
type Info struct {
	Name string // Theme name (filename without .conf extension)
	Path string // Full path to the theme file
}

// Manager handles theme loading and management.
// It scans a themes folder for .conf files, provides lookup by name,
// and creates default themes on first run.
type Manager struct {
	themesFolder string
	themes       []Info
	loadErrors   []string // parse errors collected during LoadTheme
}

// NewManager creates a new theme manager.
// themesFolder is the directory containing *.conf theme files.
// If it's empty, theme listing is disabled.
// If the directory doesn't exist, it's created with default themes.
func NewManager(themesFolder string) *Manager {
	tm := &Manager{themesFolder: themesFolder}
	tm.initializeThemesFolder()
	tm.ReloadThemes()
	return tm
}

// initializeThemesFolder creates the themes folder and populates it with default themes.
func (tm *Manager) initializeThemesFolder() {
	if tm.themesFolder == "" {
		return
	}
	if _, err := os.Stat(tm.themesFolder); os.IsNotExist(err) {
		if err := os.MkdirAll(tm.themesFolder, 0755); err != nil {
			return
		}
		tm.createDefaultThemes()
	}
}

// createDefaultThemes writes the built-in themes from embedded content.
func (tm *Manager) createDefaultThemes() {
	darkPath := filepath.Join(tm.themesFolder, "theme-dark.conf")
	_ = os.WriteFile(darkPath, []byte(darkThemeContent), 0600)

	lightPath := filepath.Join(tm.themesFolder, "theme-light.conf")
	_ = os.WriteFile(lightPath, []byte(lightThemeContent), 0600)
}

// ReloadThemes reloads the list of available themes from the themes folder.
func (tm *Manager) ReloadThemes() {
	tm.themes = nil
	if tm.themesFolder == "" {
		return
	}

	entries, err := os.ReadDir(tm.themesFolder)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		themeName := strings.TrimSuffix(name, ".conf")
		tm.themes = append(tm.themes, Info{
			Name: themeName,
			Path: filepath.Join(tm.themesFolder, name),
		})
	}

	sort.Slice(tm.themes, func(i, j int) bool {
		return tm.themes[i].Name < tm.themes[j].Name
	})
}

func (tm *Manager) GetThemes() []Info {
	if tm.themes == nil {
		return nil
	}
	result := make([]Info, len(tm.themes))
	copy(result, tm.themes)
	return result
}

// LoadTheme loads a theme by name.
// If the theme doesn't exist or name is empty, returns the default theme.
func (tm *Manager) LoadTheme(name string) *Theme {
	if name == "" {
		return DefaultTheme()
	}
	for _, t := range tm.themes {
		if t.Name == name {
			loaded, errs, err := LoadTheme(t.Path)
			if err != nil {
				return DefaultTheme()
			}
			if len(errs) > 0 {
				tm.loadErrors = append(tm.loadErrors, errs...)
			}
			return loaded
		}
	}
	return DefaultTheme()
}

// GetLoadErrors returns any parse errors collected during
// LoadTheme calls, and clears the internal buffer.
func (tm *Manager) GetLoadErrors() []string {
	w := tm.loadErrors
	tm.loadErrors = nil
	return w
}
