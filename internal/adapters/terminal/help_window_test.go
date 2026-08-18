package terminal

import (
	"testing"
)

func TestHelpWindowOpenClose(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	if hw.IsOpen() {
		t.Error("Help window should not be open initially")
	}

	hw = hw.Open()
	if !hw.IsOpen() {
		t.Error("Help window should be open after Open()")
	}

	hw = hw.Close()
	if hw.IsOpen() {
		t.Error("Help window should not be open after Close()")
	}
}

func TestHelpWindowNavigation(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	// Initially search box is focused; Tab to list to navigate
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))

	// First selectable item should be index 1 (index 0 is a section header)
	if hw.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 (first non-header), got %d", hw.SelectedIdx)
	}

	// Move down
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'j'}))
	if hw.SelectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2 after j, got %d", hw.SelectedIdx)
	}

	// Move down again
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'j'}))
	if hw.SelectedIdx != 3 {
		t.Errorf("Expected selectedIdx to be 3 after second j, got %d", hw.SelectedIdx)
	}

	// Move up
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'k'}))
	if hw.SelectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2 after k, got %d", hw.SelectedIdx)
	}
}

func TestHelpWindowSkipsSectionHeaders(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Manually set items with a specific structure for testing
	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Section 1"},
		{ID: 2, Key: "a", Description: "Action A"},
		{ID: 3, IsSection: true, Description: "Section 2"},
		{ID: 4, Key: "b", Description: "Action B"},
	}
	hw = hw.Open()

	// Tab to list
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))

	// Should start at index 1 (first non-header)
	if hw.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1, got %d", hw.SelectedIdx)
	}

	// Move down should skip header at index 2 and land on index 3
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'j'}))
	if hw.SelectedIdx != 3 {
		t.Errorf("Expected selectedIdx to be 3 (skipping header), got %d", hw.SelectedIdx)
	}

	// Move up should skip header at index 2 and land on index 1
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'k'}))
	if hw.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 (skipping header up), got %d", hw.SelectedIdx)
	}
}

func TestHelpWindowNavigationBoundary(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Section"},
		{ID: 2, Key: "a", Description: "Action A"},
		{ID: 3, Key: "b", Description: "Action B"},
	}
	hw = hw.Open()

	// Tab to list
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))

	// Start at index 1
	if hw.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1, got %d", hw.SelectedIdx)
	}

	// Move up at top - should stay at 1
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'k'}))
	if hw.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to stay at 1 at top, got %d", hw.SelectedIdx)
	}

	// Move down to last item
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'j'}))
	if hw.SelectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2, got %d", hw.SelectedIdx)
	}

	// Move down at bottom - should stay at 2
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'j'}))
	if hw.SelectedIdx != 2 {
		t.Errorf("Expected selectedIdx to stay at 2 at bottom, got %d", hw.SelectedIdx)
	}
}

func TestHelpWindowCloseKeys(t *testing.T) {
	styles := DefaultStyles()

	// Test 'q' key (Tab to list first, then q)
	hw := NewHelpWindow(styles)
	hw = hw.Open()
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'q'}))
	if hw.IsOpen() {
		t.Error("Help window should be closed after pressing q (while list is focused)")
	}

	// Test 'esc' key (works regardless of focus)
	hw = NewHelpWindow(styles)
	hw = hw.Open()
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyEsc}))
	if hw.IsOpen() {
		t.Error("Help window should be closed after pressing esc")
	}
}

func TestHelpWindowViewWhenClosed(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// View should return empty when closed
	view := hw.View()
	if view.Content != "" {
		t.Errorf("Expected empty view when closed, got %q", view.Content)
	}

	// RenderOverlay should return baseContent unchanged
	base := "base content"
	overlay := hw.RenderOverlay(base, 80, 24)
	if overlay != base {
		t.Errorf("Expected base content unchanged when closed")
	}
}

func TestHelpWindowViewWhenOpen(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	view := hw.View()
	if view.Content == "" {
		t.Error("Expected non-empty view when open")
	}

	// Should contain section headers
	if !containsStr(view.Content, "Commands") {
		t.Error("View should contain 'Commands' section")
	}

	// Should contain command entries
	if !containsStr(view.Content, ":continue") {
		t.Error("View should contain ':continue' command")
	}

	// Tab to list, then RenderOverlay should contain navigation help text
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))
	overlay := hw.RenderOverlay("base", 80, 24)
	if !containsStr(overlay, "j/k: navigate") {
		t.Error("RenderOverlay should contain navigation help text")
	}
}

func TestHelpWindowWithSize(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	hw = hw.WithSize(80, 30)
	if hw.Width != 80 {
		t.Errorf("Expected width 80, got %d", hw.Width)
	}
	// SetSize clamps height to min(height-LayoutGap, SelectorMaxHeight)
	if hw.Height != 30-4 {
		t.Errorf("Expected height %d, got %d", 30-4, hw.Height)
	}
}

func TestHelpWindowBuildHelpItems(t *testing.T) {
	items := buildHelpItems()

	if len(items) == 0 {
		t.Fatal("Expected non-empty help items")
	}

	// Count sections and items
	sections := 0
	commands := 0
	for _, item := range items {
		if item.IsSection {
			sections++
		} else {
			commands++
		}
	}

	if sections < 2 {
		t.Errorf("Expected at least 2 sections, got %d", sections)
	}
	if commands < 10 {
		t.Errorf("Expected at least 10 command entries, got %d", commands)
	}

	// First item should be a section header
	if !items[0].IsSection {
		t.Error("Expected first item to be a section header")
	}

	// Verify Ctrl+H is in the list
	found := false
	for _, item := range items {
		if item.Key == "Ctrl+H" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected Ctrl+H to be in help items")
	}
}

func TestHelpWindowEnterOnCommand(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Set up items with a :command at index 1
	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Commands"},
		{ID: 2, Key: ":quit", Description: "Exit application", Type: HelpItemCommand},
		{ID: 3, Key: ":save", Description: "Save session", Type: HelpItemCommand},
	}
	hw = hw.Open()
	// Tab to list first (search is initially focused)
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))
	// Should start at index 1 (first non-header)
	if hw.SelectedIdx != 1 {
		t.Fatalf("Expected selectedIdx to be 1, got %d", hw.SelectedIdx)
	}

	// Press Enter on :quit
	hw, cmd := hw.Update(KeyPressMsg(Key{Code: KeyEnter}))

	// Window should be closed
	if hw.IsOpen() {
		t.Error("Help window should be closed after Enter on command")
	}

	// Cmd should carry HelpCmdMsg
	if cmd == nil {
		t.Fatal("Expected cmd to be non-nil after Enter on command")
	}
	if resultMsg := cmd(); resultMsg != nil {
		if hc, ok := resultMsg.(HelpCmdMsg); ok {
			if hc.Command != ":quit" {
				t.Errorf("Expected pending command ':quit', got %q", hc.Command)
			}
		} else {
			t.Errorf("Expected HelpCmdMsg, got %T", resultMsg)
		}
	}
}

func TestHelpWindowEnterOnCommandStripsArgs(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Set up items with argument syntax in the key
	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Commands"},
		{ID: 2, Key: ":continue", Description: "Retry last prompt", Type: HelpItemCommand},
		{ID: 3, Key: ":theme_set <name>", Description: "Switch theme by name", Type: HelpItemCommand},
		{ID: 4, Key: ":tool_confirm <id>", Description: "Confirm pending tool execution", Type: HelpItemCommand},
	}
	hw = hw.Open()

	// Helper to extract command from cmd (must Tab to list first)
	getCmd := func(hw HelpWindow) string {
		// Open returns with search focused; Tab to list
		hw2, _ := hw.Update(KeyPressMsg(Key{Code: KeyTab}))
		hw2, cmd := hw2.Update(KeyPressMsg(Key{Code: KeyEnter}))
		_ = hw2
		if cmd == nil {
			return ""
		}
		if resultMsg := cmd(); resultMsg != nil {
			if hc, ok := resultMsg.(HelpCmdMsg); ok {
				return hc.Command
			}
		}
		return ""
	}

	// Press Enter on :continue — should produce ":continue"
	if cmd := getCmd(hw); cmd != ":continue" {
		t.Errorf("Expected pending command ':continue', got %q", cmd)
	}

	// Re-open and test :theme_set <name> — should produce ":theme_set"
	hw = hw.Open()
	hw = hw.moveDown()
	if cmd := getCmd(hw); cmd != ":theme_set" {
		t.Errorf("Expected pending command ':theme_set', got %q", cmd)
	}

	// Re-open and test :tool_confirm <id> — should produce ":tool_confirm"
	hw = hw.Open()
	hw = hw.moveDown()
	hw = hw.moveDown()
	if cmd := getCmd(hw); cmd != ":tool_confirm" {
		t.Errorf("Expected pending command ':tool_confirm', got %q", cmd)
	}
}

func TestHelpWindowEnterOnKeyBinding(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Set up items with a key binding (not a :command)
	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Global Shortcuts"},
		{ID: 2, Key: "Ctrl+H", Description: "Open help window"},
	}
	hw = hw.updateFilteredItems()
	hw = hw.Open()

	// Press Enter on Ctrl+H (not a :command)
	hw, cmd := hw.Update(KeyPressMsg(Key{Code: KeyEnter}))

	// Window should stay open - Enter on non-command does nothing
	if !hw.IsOpen() {
		t.Error("Help window should stay open after Enter on key binding")
	}

	// No command
	if cmd != nil {
		t.Errorf("Expected no command, got %v", cmd)
	}
}

func TestHelpWindowFilter(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	// Initially all items should be shown
	totalItems := len(hw.items)
	if hw.filteredLen() != totalItems {
		t.Errorf("Expected %d filtered items initially, got %d", totalItems, hw.filteredLen())
	}

	// Type "quit" into filter
	hw.FilterInputFocused = true
	hw.FilterInput = hw.FilterInput.Focus()
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'q'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'u'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'i'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 't'}))

	// Should have filtered items (section header + :quit)
	if hw.filteredLen() == 0 {
		t.Error("Expected filtered items after typing 'quit'")
	}

	// Should contain :quit
	found := false
	for _, item := range hw.filteredItems {
		if item.Key == ":quit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected ':quit' in filtered items after typing 'quit'")
	}

	// Clear filter with ctrl+c
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'c', Mod: ModCtrl}))
	if hw.filteredLen() != totalItems {
		t.Errorf("Expected all items after clear, got %d", hw.filteredLen())
	}
}

func TestHelpWindowFilterSectionHeaders(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	// Filter for "Ctrl" - should only show Global Shortcuts section (has Ctrl+ entries)
	hw.FilterInputFocused = true
	hw.FilterInput = hw.FilterInput.Focus()
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'C'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 't'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'r'}))
	hw, _ = hw.Update(KeyPressMsg(Key{Code: 'l'}))

	// Should include Global Shortcuts section header
	hasGlobalShortcuts := false
	hasDisplayMode := false
	for _, item := range hw.filteredItems {
		if item.IsSection && item.Description == "Global Shortcuts" {
			hasGlobalShortcuts = true
		}
		if item.IsSection && item.Description == "Display Mode" {
			hasDisplayMode = true
		}
	}
	if !hasGlobalShortcuts {
		t.Error("Expected Global Shortcuts section header in filtered items")
	}
	// Display Mode has "Ctrl+D/U" so it should also be present
	if !hasDisplayMode {
		t.Error("Expected Display Mode section header in filtered items (has Ctrl+D/U)")
	}
}

func TestHelpWindowTabToggle(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	// Initially search box is focused
	if !hw.FilterInputFocused {
		t.Error("Expected search box focused initially")
	}

	// Tab to list
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))
	if hw.FilterInputFocused {
		t.Error("Expected list focused after Tab")
	}

	// Tab back to search
	hw, _ = hw.Update(KeyPressMsg(Key{Code: KeyTab}))
	if !hw.FilterInputFocused {
		t.Error("Expected search focused after second Tab")
	}
}

func TestHelpWindowFilterEmptyResult(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw = hw.Open()

	// Directly set filter value to test filtering logic
	hw.FilterInput = hw.FilterInput.WithValue("zzz")
	hw.lastFilterValue = "" // Force update
	hw = hw.updateFilteredItems()

	if hw.filteredLen() != 0 {
		t.Errorf("Expected 0 filtered items for 'zzz', got %d", hw.filteredLen())
	}

	// View should still render without error
	view := hw.View()
	if view.Content == "" {
		t.Error("Expected non-empty view even with no matches")
	}
}

func TestHelpWindowHeaderReappearsOnScrollBack(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Use a small set of items to control the scenario precisely:
	// 1 header + 9 items = 10 entries, with SelectorListRows = 8
	hw.items = []HelpItem{
		{ID: 1, IsSection: true, Description: "Commands"},
		{ID: 2, Key: ":a", Description: "A"},
		{ID: 3, Key: ":b", Description: "B"},
		{ID: 4, Key: ":c", Description: "C"},
		{ID: 5, Key: ":d", Description: "D"},
		{ID: 6, Key: ":e", Description: "E"},
		{ID: 7, Key: ":f", Description: "F"},
		{ID: 8, Key: ":g", Description: "G"},
		{ID: 9, Key: ":h", Description: "H"},
		{ID: 10, Key: ":i", Description: "I"},
	}
	hw = hw.Open()

	// Verify header is visible at start (scrollIdx = 0)
	if hw.ScrollIdx != 0 {
		t.Fatalf("Expected scrollIdx=0 at start, got %d", hw.ScrollIdx)
	}
	if !containsStr(hw.View().Content, "Commands") {
		t.Fatal("Header should be visible at start")
	}

	// Move down past the visible area to push header out of view
	for i := 0; i < SelectorListRows; i++ {
		hw = hw.moveDown()
	}

	// Header should be out of view now
	if hw.ScrollIdx == 0 {
		t.Fatal("Expected scrollIdx > 0 after scrolling down")
	}
	if containsStr(hw.View().Content, "── Commands") {
		t.Fatal("Header should be out of view after scrolling down")
	}

	// Now move all the way back up
	for i := 0; i < SelectorListRows; i++ {
		hw = hw.moveUp()
	}

	// Header should reappear
	if hw.ScrollIdx != 0 {
		t.Errorf("Expected scrollIdx=0 after scrolling back up, got %d", hw.ScrollIdx)
	}
	if !containsStr(hw.View().Content, "── Commands") {
		t.Error("Header should be visible again after scrolling back up")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
