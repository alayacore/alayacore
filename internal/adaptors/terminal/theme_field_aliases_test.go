package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeFieldNames(t *testing.T) {
	// Test that field names work correctly
	t.Run("field names", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test.conf")

		// Create theme with field names
		content := `# Test theme
primary: #222222
dim: #333333
muted: #444444
text: #555555
warning: #666666
error: #777777
success: #888888
selection: #999999
cursor: #aaaaaa
added: #bbbbbb
removed: #cccccc
`
		if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create theme file: %v", err)
		}

		theme, err := LoadTheme(themePath)
		if err != nil {
			t.Fatalf("Failed to load theme: %v", err)
		}

		// Verify fields are set
		if theme.Primary != "#222222" {
			t.Errorf("Expected primary #222222, got %s", theme.Primary)
		}
		if theme.Selection != "#999999" {
			t.Errorf("Expected selection #999999, got %s", theme.Selection)
		}
		if theme.Added != "#bbbbbb" {
			t.Errorf("Expected added #bbbbbb, got %s", theme.Added)
		}
		if theme.Removed != "#cccccc" {
			t.Errorf("Expected removed #cccccc, got %s", theme.Removed)
		}
	})

	// Test defaults are applied for missing fields
	t.Run("defaults", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test-defaults.conf")

		// Create minimal theme
		content := `# Minimal theme
dim: #000000
`
		if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create theme file: %v", err)
		}

		theme, err := LoadTheme(themePath)
		if err != nil {
			t.Fatalf("Failed to load theme: %v", err)
		}

		// Verify specified field is used
		if theme.Dim != "#000000" {
			t.Errorf("Expected dim #000000, got %s", theme.Dim)
		}

		// Verify defaults are applied for missing fields
		defaults := DefaultTheme()
		if theme.Primary != defaults.Primary {
			t.Errorf("Expected default primary, got %s", theme.Primary)
		}
	})
}
