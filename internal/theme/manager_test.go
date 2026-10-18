package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCreatesDefaultThemes(t *testing.T) {
	themesDir := filepath.Join(t.TempDir(), "themes")

	if _, err := os.Stat(themesDir); !os.IsNotExist(err) {
		t.Fatalf("themes directory should not exist initially")
	}

	tm := NewManager(themesDir)

	if errs := tm.GetLoadErrors(); len(errs) != 0 {
		t.Fatalf("preparing a fresh folder reported errors: %v", errs)
	}
	if _, err := os.Stat(themesDir); os.IsNotExist(err) {
		t.Errorf("themes folder was not created")
	}

	darkPath := filepath.Join(themesDir, "theme-dark.conf")
	if _, err := os.Stat(darkPath); os.IsNotExist(err) {
		t.Errorf("theme-dark.conf was not created")
	}
	if _, err := os.Stat(filepath.Join(themesDir, "theme-light.conf")); os.IsNotExist(err) {
		t.Errorf("theme-light.conf was not created")
	}

	// The written default is a theme file like any other: it has to parse, with
	// the colors the built-in theme is defined by.
	th, errs, err := LoadTheme(darkPath)
	if err != nil {
		t.Fatalf("load the written default: %v", err)
	}
	if len(errs) > 0 {
		t.Errorf("the written default does not parse cleanly: %v", errs)
	}
	if th.Primary != "#89d4fa" {
		t.Errorf("primary = %q, want the dark theme's own color", th.Primary)
	}
}

// An existing folder is left alone: whatever the user put there is theirs, and a
// missing built-in theme is not a reason to write over the folder.
func TestManagerUsesExistingFolder(t *testing.T) {
	themesDir := t.TempDir()

	customPath := filepath.Join(themesDir, "custom.conf")
	if err := os.WriteFile(customPath, []byte("primary: #ff0000\n"), 0644); err != nil {
		t.Fatalf("create custom theme: %v", err)
	}

	tm := NewManager(themesDir)

	if errs := tm.GetLoadErrors(); len(errs) != 0 {
		t.Fatalf("preparing an existing folder reported errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(themesDir, "theme-dark.conf")); !os.IsNotExist(err) {
		t.Errorf("theme-dark.conf should not be created when the folder exists")
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Errorf("the user's own theme file is gone: %v", err)
	}
}

// An empty path disables the work rather than creating something: the non-TUI
// modes pass "" because themes are not in play.
func TestManagerEmptyFolderDoesNothing(t *testing.T) {
	tm := NewManager("")

	if errs := tm.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("errors = %v, want none for a disabled folder", errs)
	}
}
