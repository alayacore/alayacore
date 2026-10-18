package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A themes folder that cannot be listed is reported. It used to be silent, and
// silence is indistinguishable from "no themes configured": the selector shows an
// empty list either way.
//
// The folder path is a file, not a chmod-0 directory: that fails the same way for
// root as for anyone else, so the test cannot pass vacuously when the suite runs
// privileged.
func TestManagerReportsUnreadableThemesFolder(t *testing.T) {
	notADirectory := filepath.Join(t.TempDir(), "themes")
	if err := os.WriteFile(notADirectory, []byte("I am a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := NewManager(notADirectory)

	errs := tm.GetLoadErrors()
	if !hasError(errs, "themes") {
		t.Errorf("load errors = %v, want the folder named", errs)
	}
	if second := tm.GetLoadErrors(); len(second) != 0 {
		t.Errorf("load errors = %v on the second read, want the buffer cleared", second)
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
