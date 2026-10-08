package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A themes folder that cannot be listed is reported. It used to be silent, and
// silence is indistinguishable from "no themes configured": the selector shows
// an empty list either way.
//
// The folder path is a file, not a chmod-0 directory: that fails the same way
// for root as for anyone else, so the test cannot pass vacuously when the suite
// runs privileged.
func TestManagerReportsUnreadableThemesFolder(t *testing.T) {
	notADirectory := filepath.Join(t.TempDir(), "themes")
	if err := os.WriteFile(notADirectory, []byte("I am a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := NewManager(notADirectory)

	if themes := tm.GetThemes(); len(themes) != 0 {
		t.Errorf("themes = %v, want none from an unreadable folder", themes)
	}
	if !hasError(tm.GetLoadErrors(), "themes") {
		t.Errorf("load errors = %v, want the folder named", tm.GetLoadErrors())
	}
}

// A theme file that cannot be read is reported and the default theme is
// returned. The theme stays usable; what changes is that the user is told the
// file they wrote is not being used.
//
// The unreadable entry is a symlink to a file that is not there: it is listed
// like any other .conf, and reading it fails for every uid.
func TestManagerReportsUnreadableThemeFile(t *testing.T) {
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "good.conf"), []byte("primary: #ff0000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(folder, "broken.conf")
	if err := os.Symlink(filepath.Join(folder, "gone.conf"), broken); err != nil {
		t.Fatal(err)
	}

	tm := NewManager(folder)

	if got := tm.GetThemes(); len(got) != 2 {
		t.Fatalf("themes = %v, want both entries listed", got)
	}
	if tm.LoadTheme("broken") == nil {
		t.Fatal("LoadTheme(broken) returned nil, want a usable default")
	}
	if !hasError(tm.GetLoadErrors(), "broken.conf") {
		t.Errorf("load errors = %v, want the unreadable theme named", tm.GetLoadErrors())
	}

	// The good theme must still load: one bad file costs itself.
	good := tm.LoadTheme("good")
	if good == nil {
		t.Fatal("LoadTheme(good) returned nil")
	}
	if good.Primary != "#ff0000" {
		t.Errorf("primary = %q, want the good theme's own color", good.Primary)
	}
	if errs := tm.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("loading a readable theme reported errors: %v", errs)
	}
}

func hasError(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}
