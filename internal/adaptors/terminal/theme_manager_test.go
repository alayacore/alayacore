package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeManagerCreatesDefaultThemes(t *testing.T) {
	// Create a temporary directory for testing
	testDir := t.TempDir()
	themesDir := filepath.Join(testDir, "themes")

	// Verify themes directory doesn't exist initially
	if _, err := os.Stat(themesDir); !os.IsNotExist(err) {
		t.Fatalf("Themes directory should not exist initially")
	}

	// Create theme manager - this should create the themes folder
	tm := NewThemeManager(themesDir)

	// Verify themes folder was created
	if _, err := os.Stat(themesDir); os.IsNotExist(err) {
		t.Errorf("Themes folder was not created")
	}

	// Verify default themes were created
	darkPath := filepath.Join(themesDir, "theme-dark.conf")
	lightPath := filepath.Join(themesDir, "theme-light.conf")

	if _, err := os.Stat(darkPath); os.IsNotExist(err) {
		t.Errorf("theme-dark.conf was not created")
	}

	if _, err := os.Stat(lightPath); os.IsNotExist(err) {
		t.Errorf("theme-light.conf was not created")
	}

	// Verify themes are loaded
	themes := tm.GetThemes()
	if len(themes) != 2 {
		t.Errorf("Expected 2 themes, got %d", len(themes))
	}

	// Verify we can load the themes
	theme := tm.LoadTheme("theme-dark")
	if theme == nil {
		t.Errorf("Failed to load theme-dark")
	}

	if theme.Primary != "#89d4fa" {
		t.Errorf("Expected theme-dark primary color #89d4fa, got %s", theme.Primary)
	}
}

func TestThemeManagerUsesExistingFolder(t *testing.T) {
	// Create a temporary directory with existing themes
	testDir := t.TempDir()
	themesDir := filepath.Join(testDir, "themes")

	// Pre-create the themes folder
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("Failed to create themes directory: %v", err)
	}

	// Create a custom theme
	customTheme := `# Custom theme
base: #000000
text: #ffffff
`
	customPath := filepath.Join(themesDir, "custom.conf")
	if err := os.WriteFile(customPath, []byte(customTheme), 0644); err != nil {
		t.Fatalf("Failed to create custom theme: %v", err)
	}

	// Create theme manager - should not overwrite existing themes
	tm := NewThemeManager(themesDir)

	// Verify default themes were NOT created (folder already existed)
	darkPath := filepath.Join(themesDir, "theme-dark.conf")
	lightPath := filepath.Join(themesDir, "theme-light.conf")

	if _, err := os.Stat(darkPath); !os.IsNotExist(err) {
		t.Errorf("theme-dark.conf should not be created when folder exists")
	}

	if _, err := os.Stat(lightPath); !os.IsNotExist(err) {
		t.Errorf("theme-light.conf should not be created when folder exists")
	}

	// Verify custom theme is loaded
	themes := tm.GetThemes()
	if len(themes) != 1 {
		t.Errorf("Expected 1 theme, got %d", len(themes))
	}

	if len(themes) > 0 && themes[0].Name != "custom" {
		t.Errorf("Expected custom theme, got %s", themes[0].Name)
	}
}
