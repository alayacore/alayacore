package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultTheme(t *testing.T) {
	theme := DefaultTheme()
	if theme.Base != "#1e1e2e" {
		t.Errorf("Expected Base #1e1e2e, got %s", theme.Base)
	}
	if theme.Accent != "#89d4fa" {
		t.Errorf("Expected Accent #89d4fa, got %s", theme.Accent)
	}
}

func TestLoadTheme(t *testing.T) {
	// Create a temporary theme file
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "test-theme.conf")
	content := `# Test theme
base: #000000
accent: #ffffff
error: #ff0000
`
	if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test theme file: %v", err)
	}

	theme, err := LoadTheme(themePath)
	if err != nil {
		t.Fatalf("LoadTheme failed: %v", err)
	}

	// Check that custom values were loaded
	if theme.Base != "#000000" {
		t.Errorf("Expected Base #000000, got %s", theme.Base)
	}
	if theme.Accent != "#ffffff" {
		t.Errorf("Expected Accent #ffffff, got %s", theme.Accent)
	}
	if theme.Error != "#ff0000" {
		t.Errorf("Expected Error #ff0000, got %s", theme.Error)
	}

	// Check that other values retain defaults
	if theme.Warning != "#f9e2af" {
		t.Errorf("Expected Warning #f9e2af (default), got %s", theme.Warning)
	}
}

func TestLoadThemeWithAliases(t *testing.T) {
	// Note: The new config parser does not support aliases.
	// Users should use canonical field names: base, muted, etc.
	// This test verifies that unknown fields are simply ignored.
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "alias-theme.conf")
	content := `# Theme with old alias names (now ignored)
window_border: #111111
text_muted: #222222
base: #333333
`
	if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test theme file: %v", err)
	}

	theme, err := LoadTheme(themePath)
	if err != nil {
		t.Fatalf("LoadTheme failed: %v", err)
	}

	// Unknown fields are ignored, known fields work
	if theme.Base != "#333333" {
		t.Errorf("Expected Base #333333, got %s", theme.Base)
	}
	// Muted should be default since text_muted is not a recognized field
	if theme.Muted != "#6c7086" {
		t.Errorf("Expected Muted #6c7086 (default), got %s", theme.Muted)
	}
}

func TestLoadThemeInvalidPath(t *testing.T) {
	_, err := LoadTheme("/nonexistent/path/theme.conf")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestLoadThemeFromPaths(t *testing.T) {
	// Set HOME to a temp directory to isolate from user's config
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Test with nonexistent explicit path (should fallback to default)
	theme := LoadThemeFromPaths("/nonexistent/theme.conf")
	if theme.Base != "#1e1e2e" {
		t.Errorf("Expected default theme, got Base %s", theme.Base)
	}

	// Test with empty path (should use default)
	theme = LoadThemeFromPaths("")
	if theme.Base != "#1e1e2e" {
		t.Errorf("Expected default theme, got Base %s", theme.Base)
	}
}

func TestNewStylesWithTheme(t *testing.T) {
	theme := &Theme{
		Base:     "#1e1e2e",
		Surface1: "#585b70",
		Accent:   "#custom1",
		Dim:      "#custom2",
		Muted:    "#custom3",
		Text:     "#custom4",
		Warning:  "#custom5",
		Error:    "#custom6",
		Success:  "#custom7",
		Peach:    "#custom8",
		Cursor:   "#custom9",
	}

	styles := NewStyles(theme)
	if styles == nil {
		t.Fatal("NewStyles returned nil")
	}

	// Verify that styles are created (we can't easily test the actual color values
	// without extracting them from lipgloss.Style, but we can verify it doesn't crash)
	_ = styles.Text.Render("test")
	_ = styles.Error.Render("test")

	// Verify color fields are accessible
	_ = styles.ColorAccent
	_ = styles.ColorDim
	_ = styles.ColorError
	_ = styles.ColorSuccess
	_ = styles.ColorBase
	_ = styles.CursorColor
}
