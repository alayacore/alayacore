package terminal

import (
	"encoding/json"
	"testing"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

func TestStatusBarShowsLastMaxSteps(t *testing.T) {
	// Create output writer and simulate task completion
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress with max steps = 50, current step = 2
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    50,
		CurrentStep: 2,
	}
	data := marshalSystemInfoForTerminalTest(t, systemInfoInProgress)
	out.handleSystemTag(string(data))

	// Simulate task completion
	systemInfoCompleted := agentpkg.SystemInfo{
		InProgress:  false,
		MaxSteps:    50,
		CurrentStep: 0,
	}
	data = marshalSystemInfoForTerminalTest(t, systemInfoCompleted)
	out.handleSystemTag(string(data))

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:           out,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		windowWidth:   80,
		windowHeight:  24,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Update status
	terminal.updateStatus()

	// Check that status shows last step info (2/50, not 50/50)
	expectedSubstring := "Steps: 2/50"
	if !containsSubstring(terminal.statusText, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, terminal.statusText)
	}
}

func TestStatusBarShowsCurrentStepsDuringProgress(t *testing.T) {
	// Create output writer and simulate task in progress
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    20,
		CurrentStep: 7,
	}
	data := marshalSystemInfoForTerminalTest(t, systemInfoInProgress)
	out.handleSystemTag(string(data))

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:           out,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		windowWidth:   80,
		windowHeight:  24,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Update status
	terminal.updateStatus()

	// Check that status shows current step progress
	expectedSubstring := "Steps: 7/20"
	if !containsSubstring(terminal.statusText, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, terminal.statusText)
	}
}

// Helper function to marshal SystemInfo to JSON
func marshalSystemInfoForTerminalTest(t *testing.T, info agentpkg.SystemInfo) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Failed to marshal SystemInfo: %v", err)
	}
	return data
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
