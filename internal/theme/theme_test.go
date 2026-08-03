package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTheme(t *testing.T) {
	// Pure-content variant: overrides defaults, reports parse errors.
	content := `# Test theme
primary: #ffffff
error: #ff0000
unknown_field: ignored
`
	th, errs := parseTheme(content)

	if th.Primary != "#ffffff" {
		t.Errorf("Expected Primary #ffffff, got %s", th.Primary)
	}
	if th.Error != "#ff0000" {
		t.Errorf("Expected Error #ff0000, got %s", th.Error)
	}
	if th.Warning != "#f77923" {
		t.Errorf("Expected Warning #f77923 (default), got %s", th.Warning)
	}

	if len(errs) != 1 {
		t.Fatalf("expected 1 parse error for unknown field, got %d: %v", len(errs), errs)
	}
	// Pure-content parser: errors are bare, no file prefix.
	if strings.Contains(errs[0], "theme.conf:") {
		t.Errorf("parseTheme errors must be bare (no file prefix), got: %s", errs[0])
	}
	if !strings.Contains(errs[0], "unknown_field") {
		t.Errorf("expected error mentioning unknown_field, got: %s", errs[0])
	}
}

func TestDefaultTheme(t *testing.T) {
	th := DefaultTheme()
	if th.Primary != "#89d4fa" {
		t.Errorf("Expected Primary #89d4fa, got %s", th.Primary)
	}
}

func TestLoadTheme(t *testing.T) {
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "test-theme.conf")
	content := `# Test theme
primary: #ffffff
error: #ff0000
`
	if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test theme file: %v", err)
	}

	th, _, err := LoadTheme(themePath)
	if err != nil {
		t.Fatalf("LoadTheme failed: %v", err)
	}

	if th.Primary != "#ffffff" {
		t.Errorf("Expected Primary #ffffff, got %s", th.Primary)
	}
	if th.Error != "#ff0000" {
		t.Errorf("Expected Error #ff0000, got %s", th.Error)
	}

	if th.Warning != "#f77923" {
		t.Errorf("Expected Warning #f77923 (default), got %s", th.Warning)
	}

	if th.Tool != "#f9e2af" {
		t.Errorf("Expected Tool #f9e2af (default), got %s", th.Tool)
	}
}

func TestLoadThemeWithUnknownFields(t *testing.T) {
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "unknown-fields.conf")
	content := `# Theme with unknown field names (ignored)
unknown_field: #111111
another_unknown: #222222
primary: #333333
`
	if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test theme file: %v", err)
	}

	th, errs, err := LoadTheme(themePath)
	if err != nil {
		t.Fatalf("LoadTheme failed: %v", err)
	}
	if len(errs) == 0 {
		t.Error("expected parse errors for unknown fields, got none")
	}
	// Errors must be prefixed with the actual theme file name so the
	// offending file can be identified (parseTheme returns bare errors).
	for _, e := range errs {
		if !strings.HasPrefix(e, "unknown-fields.conf: ") {
			t.Errorf("expected error prefixed with theme file name, got: %s", e)
		}
	}

	if th.Primary != "#333333" {
		t.Errorf("Expected Primary #333333, got %s", th.Primary)
	}
	if th.Muted != "#6c7086" {
		t.Errorf("Expected Muted #6c7086 (default), got %s", th.Muted)
	}
}

func TestLoadThemeInvalidPath(t *testing.T) {
	_, _, err := LoadTheme("/nonexistent/path/theme.conf")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestLoadThemeFieldAliases(t *testing.T) {
	t.Run("field names", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test.conf")
		content := `# Test theme
primary: #222222
selection: #999999
added: #bbbbbb
removed: #cccccc
`
		if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create theme file: %v", err)
		}

		th, _, err := LoadTheme(themePath)
		if err != nil {
			t.Fatalf("Failed to load theme: %v", err)
		}

		if th.Primary != "#222222" {
			t.Errorf("Expected primary #222222, got %s", th.Primary)
		}
		if th.Selection != "#999999" {
			t.Errorf("Expected selection #999999, got %s", th.Selection)
		}
		if th.Added != "#bbbbbb" {
			t.Errorf("Expected added #bbbbbb, got %s", th.Added)
		}
		if th.Removed != "#cccccc" {
			t.Errorf("Expected removed #cccccc, got %s", th.Removed)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test-defaults.conf")

		content := `# Minimal theme
dim: #000000
`
		if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create theme file: %v", err)
		}

		th, _, err := LoadTheme(themePath)
		if err != nil {
			t.Fatalf("Failed to load theme: %v", err)
		}

		if th.Dim != "#000000" {
			t.Errorf("Expected dim #000000, got %s", th.Dim)
		}

		defaults := DefaultTheme()
		if th.Primary != defaults.Primary {
			t.Errorf("Expected default primary, got %s", th.Primary)
		}
	})
}
