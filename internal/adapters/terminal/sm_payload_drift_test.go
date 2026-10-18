package terminal

import "testing"

// TestThemePayloadsMatchTheGuide pins the two SM payloads this package decodes
// itself — theme and theme_list, the pair that carries theme.Theme (see
// themePayload) — against the shapes adapter-guide/README.md documents.
//
// The other twelve payloads get that guarantee for free: protocol owns their
// types, so this adapter reads the same struct the session writes and a rename
// cannot land on one side only. These two have a copy on each side, so each end
// is measured against the guide instead — the agent's
// adapter_guide_tables_test.go checks its structs against that table, and this
// test feeds the table's shape through this decoder. A stale copy then fails a
// test rather than dropping a field on the floor, which is what a theme that
// quietly stops applying looks like.
func TestThemePayloadsMatchTheGuide(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// theme_list carries every theme, palettes included; the adapter caches it.
	w.handleSystemFrame(`{"type":"theme_list","data":{"themes":[` +
		`{"name":"theme-dark","theme":{"primary":"#89d4fa","dim":"#313244","muted":"#6c7086","warning":"#f77923","error":"#f38ba8","added":"#a6e3a1","removed":"#f38ba8"}},` +
		`{"name":"theme-light","theme":{"primary":"#1e66f5","dim":"#ccd0da","muted":"#9ca0b0","warning":"#df8e1d","error":"#d20f39","added":"#40a02b","removed":"#d20f39"}}]}}`)

	snap := w.SnapshotStatus()
	if len(snap.CachedThemes) != 2 {
		t.Fatalf("cached themes = %d, want 2", len(snap.CachedThemes))
	}
	light := snap.CachedThemes[1]
	if light.Name != "theme-light" {
		t.Errorf("second cached theme = %q, want theme-light", light.Name)
	}
	if light.Theme == nil || light.Theme.Primary != "#1e66f5" || light.Theme.Removed != "#d20f39" {
		t.Errorf("second cached palette = %+v, want primary #1e66f5 and removed #d20f39", light.Theme)
	}

	// theme names the active entry; the palette is resolved from that cache, so
	// the frame carries no colors of its own.
	w.handleSystemFrame(`{"type":"theme","data":{"name":"theme-light"}}`)

	snap = w.SnapshotStatus()
	if snap.ActiveTheme != "theme-light" {
		t.Errorf("active theme = %q, want theme-light", snap.ActiveTheme)
	}
	if snap.ActiveThemeData == nil || snap.ActiveThemeData.Primary != "#1e66f5" {
		t.Errorf("active palette = %+v, want the cached theme-light colors", snap.ActiveThemeData)
	}
}
