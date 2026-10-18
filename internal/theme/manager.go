package theme

import (
	"fmt"
	"os"
	"path/filepath"
)

// Manager prepares a themes folder: it creates one holding the built-in themes
// when the folder is missing, and reports what it could not do.
//
// Reading themes happens elsewhere — the agent lists this same folder for the
// theme_list broadcast (internal/agent/session_output.go), an adapter resolves a
// name against that list, and LoadTheme parses one file. What is left here is the
// part that writes, and the reason it reports: a folder that cannot be created or
// listed is a theme the user is never offered, and silence about it is
// indistinguishable from "no themes configured".
type Manager struct {
	themesFolder string
	loadErrors   []string // paths that cannot be read or written
}

// NewManager prepares themesFolder and returns the manager holding whatever went
// wrong. An empty folder does nothing: there is nothing to prepare, and the
// caller has said themes are not in play.
func NewManager(themesFolder string) *Manager {
	tm := &Manager{themesFolder: themesFolder}
	tm.prepare()
	return tm
}

// prepare creates the folder with the built-in themes when it is missing, then
// lists it either way — a folder that exists but cannot be read is exactly the
// failure worth naming.
func (tm *Manager) prepare() {
	if tm.themesFolder == "" {
		return
	}

	if _, err := os.Stat(tm.themesFolder); os.IsNotExist(err) {
		if err := os.MkdirAll(tm.themesFolder, 0755); err != nil {
			tm.recordError(tm.themesFolder, err)
			return
		}
		tm.createDefaultThemes()
	}

	if _, err := os.ReadDir(tm.themesFolder); err != nil {
		tm.recordError(tm.themesFolder, err)
	}
}

// createDefaultThemes writes the built-in themes from embedded content.
func (tm *Manager) createDefaultThemes() {
	darkPath := filepath.Join(tm.themesFolder, "theme-dark.conf")
	if err := os.WriteFile(darkPath, []byte(darkThemeContent), 0600); err != nil {
		tm.recordError(darkPath, err)
	}

	lightPath := filepath.Join(tm.themesFolder, "theme-light.conf")
	if err := os.WriteFile(lightPath, []byte(lightThemeContent), 0600); err != nil {
		tm.recordError(lightPath, err)
	}
}

// recordError collects a folder or file problem for the startup screen: an
// adapter shows these, and a theme that is silently missing is a failure the user
// cannot otherwise see.
func (tm *Manager) recordError(path string, err error) {
	tm.loadErrors = append(tm.loadErrors, fmt.Sprintf("%s: %v", filepath.Base(path), err))
}

// GetLoadErrors returns the problems found while preparing the folder and clears
// the buffer: a caller that shows them is the only reader there is.
func (tm *Manager) GetLoadErrors() []string {
	errs := tm.loadErrors
	tm.loadErrors = nil
	return errs
}
