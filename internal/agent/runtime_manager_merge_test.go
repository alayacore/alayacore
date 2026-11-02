package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRuntimeFile returns the file's current content, parsed.
func readRuntimeFile(t *testing.T, path string) runtimeConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	cfg, errs := parseRuntimeConfig(string(data), "runtime.conf")
	if len(errs) > 0 {
		t.Fatalf("runtime.conf no longer parses: %v\ncontent: %q", errs, data)
	}
	return cfg
}

// runtime.conf is per user, not per instance: two terminals share one file. A
// manager holds the copy it loaded at startup, and writing that whole copy back
// is what let one instance undo the other's change. The two settings must stay
// independent.
func TestRuntimeManagerKeepsAnotherInstancesChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")

	// The instance that will be told to switch model.
	first := newRuntimeManager(path)
	// A second instance, started later, that switches the theme.
	second := newRuntimeManager(path)
	if err := second.setActiveTheme("theme-light"); err != nil {
		t.Fatal(err)
	}

	// first's snapshot predates the theme switch; its model switch must not
	// revert it.
	if err := first.setActiveModel("Model B"); err != nil {
		t.Fatal(err)
	}

	cfg := readRuntimeFile(t, path)
	if cfg.ActiveModel != "Model B" {
		t.Errorf("active_model = %q, want %q", cfg.ActiveModel, "Model B")
	}
	if cfg.ActiveTheme != "theme-light" {
		t.Errorf("active_theme = %q, want the other instance's %q to survive", cfg.ActiveTheme, "theme-light")
	}

	// The writing process adopts what it merged, so its own next write cannot
	// undo the theme a third time.
	if got := first.getActiveTheme(); got != "theme-light" {
		t.Errorf("first's active theme = %q, want the merged %q", got, "theme-light")
	}
}

// The same in the other direction, and across repeated switches: the field a
// process did not change is never written from its stale copy.
func TestRuntimeManagerMergesRepeatedSwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")

	first := newRuntimeManager(path)
	second := newRuntimeManager(path)

	if err := first.setActiveModel("Model A"); err != nil {
		t.Fatal(err)
	}
	if err := second.setActiveTheme("theme-dark"); err != nil {
		t.Fatal(err)
	}
	if err := first.setActiveTheme("theme-light"); err != nil {
		t.Fatal(err)
	}
	if err := second.setActiveModel("Model C"); err != nil {
		t.Fatal(err)
	}

	cfg := readRuntimeFile(t, path)
	if cfg.ActiveModel != "Model C" {
		t.Errorf("active_model = %q, want %q", cfg.ActiveModel, "Model C")
	}
	if cfg.ActiveTheme != "theme-light" {
		t.Errorf("active_theme = %q, want %q", cfg.ActiveTheme, "theme-light")
	}
}

// A file that no longer parses (a hand edit gone wrong, or the tail of an
// interrupted write by an older version) must not stop a switch: the merge keeps
// what it can read and the write replaces the file with well-formed content.
func TestRuntimeManagerReplacesUnparseableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")
	rm := newRuntimeManager(path)

	if err := os.WriteFile(path, []byte("active_theme theme-light\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := rm.setActiveModel("Model A"); err != nil {
		t.Fatalf("a corrupt file blocked the write: %v", err)
	}

	cfg := readRuntimeFile(t, path)
	if cfg.ActiveModel != "Model A" {
		t.Errorf("active_model = %q, want %q", cfg.ActiveModel, "Model A")
	}
}

// A path that cannot be written is reported, not silently accepted: the session
// tells the user the switch will not survive a restart.
func TestRuntimeManagerReportsWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")
	rm := newRuntimeManager(path)

	// A directory where the file was: writing it must fail for root too, so the
	// test cannot pass vacuously.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	err := rm.setActiveTheme("theme-light")
	if err == nil {
		t.Fatal("switching a theme over a directory path succeeded")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %v, want it to say the target is a directory", err)
	}
}
