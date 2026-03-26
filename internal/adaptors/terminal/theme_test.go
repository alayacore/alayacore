package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultTheme(t *testing.T) {
	theme := DefaultTheme()
	if theme.Background != "#1e1e2e" {
		t.Errorf("Expected Background #1e1e2e, got %s", theme.Background)
	}
	if theme.Primary != "#89d4fa" {
		t.Errorf("Expected Primary #89d4fa, got %s", theme.Primary)
	}
}

func TestLoadTheme(t *testing.T) {
	// Create a temporary theme file
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "test-theme.conf")
	content := `# Test theme
background: #000000
primary: #ffffff
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
	if theme.Background != "#000000" {
		t.Errorf("Expected Background #000000, got %s", theme.Background)
	}
	if theme.Primary != "#ffffff" {
		t.Errorf("Expected Primary #ffffff, got %s", theme.Primary)
	}
	if theme.Error != "#ff0000" {
		t.Errorf("Expected Error #ff0000, got %s", theme.Error)
	}

	// Check that other values retain defaults
	if theme.Warning != "#f9e2af" {
		t.Errorf("Expected Warning #f9e2af (default), got %s", theme.Warning)
	}
}

func TestLoadThemeWithUnknownFields(t *testing.T) {
	// Verify that unknown fields are simply ignored
	tmpDir := t.TempDir()
	themePath := filepath.Join(tmpDir, "unknown-fields.conf")
	content := `# Theme with unknown field names (ignored)
unknown_field: #111111
another_unknown: #222222
background: #333333
`
	if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test theme file: %v", err)
	}

	theme, err := LoadTheme(themePath)
	if err != nil {
		t.Fatalf("LoadTheme failed: %v", err)
	}

	// Unknown fields are ignored, known fields work
	if theme.Background != "#333333" {
		t.Errorf("Expected Background #333333, got %s", theme.Background)
	}
	// Muted should be default since it's not specified
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
	if theme.Background != "#1e1e2e" {
		t.Errorf("Expected default theme, got Background %s", theme.Background)
	}

	// Test with empty path (should use default)
	theme = LoadThemeFromPaths("")
	if theme.Background != "#1e1e2e" {
		t.Errorf("Expected default theme, got Background %s", theme.Background)
	}
}

func TestNewStylesWithTheme(t *testing.T) {
	theme := &Theme{
		Background: "#1e1e2e",
		Surface:    "#585b70",
		Primary:    "#custom1",
		Dim:        "#custom2",
		Muted:      "#custom3",
		Text:       "#custom4",
		Warning:    "#custom5",
		Error:      "#custom6",
		Success:    "#custom7",
		Selection:  "#custom8",
		Cursor:     "#custom9",
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
