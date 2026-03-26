package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeFieldAliases(t *testing.T) {
	// Test that new field names work
	t.Run("new field names", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test-new.conf")

		// Create theme with new field names
		content := `# Test theme with new field names
background: #000000
surface: #111111
primary: #222222
selection: #999999
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

		// Verify new fields are set
		if theme.Background != "#000000" {
			t.Errorf("Expected background #000000, got %s", theme.Background)
		}
		if theme.Surface != "#111111" {
			t.Errorf("Expected surface #111111, got %s", theme.Surface)
		}
		if theme.Primary != "#222222" {
			t.Errorf("Expected primary #222222, got %s", theme.Primary)
		}

		// Verify legacy aliases are also populated
		if theme.Base != "#000000" {
			t.Errorf("Expected legacy base #000000, got %s", theme.Base)
		}
		if theme.Surface1 != "#111111" {
			t.Errorf("Expected legacy surface1 #111111, got %s", theme.Surface1)
		}
		if theme.Accent != "#222222" {
			t.Errorf("Expected legacy accent #222222, got %s", theme.Accent)
		}
		if theme.Peach != "#999999" {
			t.Errorf("Expected legacy peach #999999, got %s", theme.Peach)
		}
	})

	// Test that old field names still work
	t.Run("legacy field names", func(t *testing.T) {
		testDir := t.TempDir()
		themePath := filepath.Join(testDir, "test-legacy.conf")

		// Create theme with legacy field names
		content := `# Test theme with legacy field names
base: #000000
surface1: #111111
accent: #222222
peach: #999999
diff_add: #bbbbbb
diff_remove: #cccccc
`
		if err := os.WriteFile(themePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create theme file: %v", err)
		}

		theme, err := LoadTheme(themePath)
		if err != nil {
			t.Fatalf("Failed to load theme: %v", err)
		}

		// Verify legacy fields are set
		if theme.Base != "#000000" {
			t.Errorf("Expected base #000000, got %s", theme.Base)
		}
		if theme.Surface1 != "#111111" {
			t.Errorf("Expected surface1 #111111, got %s", theme.Surface1)
		}
		if theme.Accent != "#222222" {
			t.Errorf("Expected accent #222222, got %s", theme.Accent)
		}

		// Verify new field aliases are also populated
		if theme.Background != "#000000" {
			t.Errorf("Expected new background #000000, got %s", theme.Background)
		}
		if theme.Surface != "#111111" {
			t.Errorf("Expected new surface #111111, got %s", theme.Surface)
		}
		if theme.Primary != "#222222" {
			t.Errorf("Expected new primary #222222, got %s", theme.Primary)
		}
		if theme.Selection != "#999999" {
			t.Errorf("Expected new selection #999999, got %s", theme.Selection)
		}
	})
}
