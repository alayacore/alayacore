package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCreatesDefaultThemes(t *testing.T) {
	testDir := t.TempDir()
	themesDir := filepath.Join(testDir, "themes")

	if _, err := os.Stat(themesDir); !os.IsNotExist(err) {
		t.Fatalf("Themes directory should not exist initially")
	}

	tm := NewManager(themesDir)

	if _, err := os.Stat(themesDir); os.IsNotExist(err) {
		t.Errorf("Themes folder was not created")
	}

	darkPath := filepath.Join(themesDir, "theme-dark.conf")
	lightPath := filepath.Join(themesDir, "theme-light.conf")

	if _, err := os.Stat(darkPath); os.IsNotExist(err) {
		t.Errorf("theme-dark.conf was not created")
	}
	if _, err := os.Stat(lightPath); os.IsNotExist(err) {
		t.Errorf("theme-light.conf was not created")
	}

	themes := tm.GetThemes()
	if len(themes) != 2 {
		t.Errorf("Expected 2 themes, got %d", len(themes))
	}

	th := tm.LoadTheme("theme-dark")
	if th == nil {
		t.Fatalf("Failed to load theme-dark")
	} else if th.Primary != "#89d4fa" {
		t.Errorf("Expected theme-dark primary color #89d4fa, got %s", th.Primary)
	}
}

func TestManagerUsesExistingFolder(t *testing.T) {
	testDir := t.TempDir()
	themesDir := filepath.Join(testDir, "themes")

	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("Failed to create themes directory: %v", err)
	}

	customTheme := `# Custom theme
primary: #ff0000
`
	customPath := filepath.Join(themesDir, "custom.conf")
	if err := os.WriteFile(customPath, []byte(customTheme), 0644); err != nil {
		t.Fatalf("Failed to create custom theme: %v", err)
	}

	tm := NewManager(themesDir)

	darkPath := filepath.Join(themesDir, "theme-dark.conf")
	lightPath := filepath.Join(themesDir, "theme-light.conf")

	if _, err := os.Stat(darkPath); !os.IsNotExist(err) {
		t.Errorf("theme-dark.conf should not be created when folder exists")
	}
	if _, err := os.Stat(lightPath); !os.IsNotExist(err) {
		t.Errorf("theme-light.conf should not be created when folder exists")
	}

	themes := tm.GetThemes()
	if len(themes) != 1 {
		t.Errorf("Expected 1 theme, got %d", len(themes))
	}

	if len(themes) > 0 && themes[0].Name != "custom" {
		t.Errorf("Expected custom theme, got %s", themes[0].Name)
	}
}
