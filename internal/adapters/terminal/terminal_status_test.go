package terminal

import (
	"fmt"
	"testing"
)

func TestStatusBarShowsCurrentStepsDuringProgress(t *testing.T) {
	// Create output writer and simulate task in progress
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":7,"max_steps":20,"context":0,"context_limit":0}}`)

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:              out,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		editor:           NewEditor(),
		modelSelector:    NewModelSelector(styles),
		themeSelector:    NewThemeSelector(styles),
		helpWindow:       NewHelpWindow(styles),
		confirmOverlay:   NewConfirmDialog(styles),
		mcpInitOverlay:   NewConfirmDialog(styles),
		attachmentWindow: NewAttachmentWindow(styles),
		focusedWindow:    focusInput,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	// Update status
	*terminal = terminal.updateStatus()

	// Check that status shows current step progress
	expectedSubstring := "7/20"
	plain := stripANSI(terminal.statusText)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

func TestStatusBarShowsLastStepsAfterCompletion(t *testing.T) {
	// Create output writer and simulate a task that ran 5 of 10 steps
	out := NewTerminalOutput(DefaultStyles())

	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0}}`)
	// Completion broadcast carries current_step zeroed.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:              out,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		editor:           NewEditor(),
		modelSelector:    NewModelSelector(styles),
		themeSelector:    NewThemeSelector(styles),
		helpWindow:       NewHelpWindow(styles),
		confirmOverlay:   NewConfirmDialog(styles),
		mcpInitOverlay:   NewConfirmDialog(styles),
		attachmentWindow: NewAttachmentWindow(styles),
		focusedWindow:    focusInput,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	// Update status
	*terminal = terminal.updateStatus()

	// Check that the status shows the last run's summary
	expectedSubstring := "last 5/10"
	plain := stripANSI(terminal.statusText)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

func TestStatusBarShowsLastStepsUnlimited(t *testing.T) {
	// Simulate a task with unlimited max steps (default --max-steps=0)
	out := NewTerminalOutput(DefaultStyles())

	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":3,"max_steps":0,"context":0,"context_limit":0}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":0,"context":0,"context_limit":0}}`)

	styles := DefaultStyles()
	terminal := &Terminal{
		out:              out,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		editor:           NewEditor(),
		modelSelector:    NewModelSelector(styles),
		themeSelector:    NewThemeSelector(styles),
		helpWindow:       NewHelpWindow(styles),
		confirmOverlay:   NewConfirmDialog(styles),
		mcpInitOverlay:   NewConfirmDialog(styles),
		attachmentWindow: NewAttachmentWindow(styles),
		focusedWindow:    focusInput,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	*terminal = terminal.updateStatus()

	expectedSubstring := "last 3/INF"
	plain := stripANSI(terminal.statusText)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{9999, "10.0K"},
		{10000, "10K"},
		{15500, "15.5K"},
		{100000, "100K"},
		{999499, "999.5K"},
		{1000000, "1M"},
		{1500000, "1.5M"},
		{10000000, "10M"},
		{128000, "128K"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := formatTokenCount(tt.input)
			if got != tt.expected {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
