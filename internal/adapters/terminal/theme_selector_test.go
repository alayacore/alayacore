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

// TestThemePreviewStaleTickIgnoredAfterCancel is a Terminal-level regression
// test for the preview debounce race: navigating in the theme selector
// schedules a 150ms preview tick, and canceling before it fires must not
// apply the preview after the overlay closed (the cancel path restores the
// original theme, then the stale tick would re-apply the preview).
func TestThemePreviewStaleTickIgnoredAfterCancel(t *testing.T) {
	original := theme.DefaultTheme()
	preview := *original
	preview.Warning = "#123456" // arbitrary field just to mutate a copy
	previewData := &preview

	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, original, NewThemeManager(""), "theme-dark")
	ow := terminal.out.(*outputWriter)
	ow.status.updateThemeList([]ThemeEntry{
		{Name: "theme-dark", Theme: original},
		{Name: "theme-preview", Theme: previewData},
	})
	ow.status.updateTheme("theme-dark", original)

	// Open the selector (Ctrl+P).
	m, cmd := terminal.Update(KeyPressMsg(Key{Code: 'p', Mod: ModCtrl}))
	if cmd != nil {
		t.Fatalf("open selector returned a Cmd: %T", cmd)
	}
	terminal = m.(Terminal)

	// Tab to the list, then navigate to the preview theme — this schedules
	// the debounce tick. (Tab itself also schedules a preview tick for the
	// initial selection; it is irrelevant here.)
	m, _ = terminal.Update(KeyPressMsg(Key{Code: KeyTab}))
	terminal = m.(Terminal)
	m, cmd = terminal.Update(KeyPressMsg(Key{Code: 'j'}))
	terminal = m.(Terminal)
	if cmd == nil {
		t.Fatal("expected a preview debounce Cmd after navigation")
	}
	pending := cmd

	// Cancel before the debounce fires.
	m, _ = terminal.Update(KeyPressMsg(Key{Code: KeyEsc}))
	terminal = m.(Terminal)

	// The stale tick fires late (fires the 150ms timer) — it must be
	// ignored: handleThemePreview only applies when the tick's ID still
	// matches, and the close bumped it.
	msg := pending() // Batch collapses to the single Tick when the inner Cmd is nil
	if batch, ok := msg.(BatchMsg); ok {
		msg = nil
		for _, c := range batch {
			if c == nil {
				continue
			}
			if r := c(); r != nil {
				msg = r
			}
		}
	}
	pm, ok := msg.(themePreviewMsg)
	if !ok {
		t.Fatalf("pending Cmd produced %T, want themePreviewMsg", msg)
	}
	m, _ = terminal.Update(pm)
	terminal = m.(Terminal)
	if terminal.previewAppliedTheme != nil {
		t.Errorf("stale preview applied after cancel: %v", terminal.previewAppliedTheme)
	}
}
