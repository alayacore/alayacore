package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
)

func TestThemeSelectorCancelRestoresOriginalTheme(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []ThemeEntry{
		{Name: "theme-dark"},
		{Name: "theme-light"},
		{Name: "theme-custom"},
	}

	ts = ts.Open(themes, "theme-dark")

	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Expected original theme 'theme-dark', got '%s'", ts.GetOriginalThemeName())
	}

	// Tab to switch focus from filter to list, then navigate down.
	ts, _ = ts.Update(KeyPressMsg(Key{Code: KeyTab}))
	ts, _ = ts.Update(KeyPressMsg(Key{Code: 'j'}))

	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}

	ts, _ = ts.Update(KeyPressMsg(Key{Code: KeyEsc}))

	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after ESC")
	}
	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Original theme should still be 'theme-dark' after cancel, got '%s'", ts.GetOriginalThemeName())
	}
}

func TestThemeSelectorEnterSavesTheme(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []ThemeEntry{
		{Name: "theme-dark"},
		{Name: "theme-light"},
	}

	ts = ts.Open(themes, "theme-dark")

	// Tab to switch focus from filter to list, then navigate down.
	ts, _ = ts.Update(KeyPressMsg(Key{Code: KeyTab}))
	ts, _ = ts.Update(KeyPressMsg(Key{Code: 'j'}))

	ts, _ = ts.Update(KeyPressMsg(Key{Code: KeyEnter}))

	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after Enter")
	}
	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}
}
