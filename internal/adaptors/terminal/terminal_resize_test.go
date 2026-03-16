package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/stream"
)

// TestTerminalResizeCursorValidation tests that the window cursor is properly
// validated when the terminal is resized.
func TestTerminalResizeCursorValidation(t *testing.T) {
	// Create a terminal with initial size
	output := NewTerminalOutput()
	input := stream.NewChanInput(10)
	terminal := NewTerminal(nil, output, input, nil, 80, 24)

	// Add some windows to the buffer
	output.windowBuffer.AppendOrUpdate("window-1", stream.TagTextAssistant, "Content 1")
	output.windowBuffer.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content 2")
	output.windowBuffer.AppendOrUpdate("window-3", stream.TagTextAssistant, "Content 3")

	// Set cursor to the middle window
	terminal.display.SetWindowCursor(1)
	if terminal.display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", terminal.display.GetWindowCursor())
	}

	// Simulate a resize event
	resizeMsg := tea.WindowSizeMsg{
		Width:  120, // Wider terminal
		Height: 40,  // Taller terminal
	}

	model, _ := terminal.Update(resizeMsg)
	terminal, ok := model.(*Terminal)
	if !ok {
		t.Fatalf("Expected *Terminal, got %T", model)
	}

	// Cursor should still be valid
	cursor := terminal.display.GetWindowCursor()
	if cursor < 0 || cursor >= 3 {
		t.Errorf("Cursor %d is out of valid range [0, 2] after resize", cursor)
	}
}

// TestTerminalResizeClampsCursor tests that cursor is clamped when windows
// change height during resize.
func TestTerminalResizeClampsCursor(t *testing.T) {
	output := NewTerminalOutput()
	input := stream.NewChanInput(10)
	terminal := NewTerminal(nil, output, input, nil, 80, 24)

	// Add windows
	output.windowBuffer.AppendOrUpdate("window-1", stream.TagTextAssistant, "Short")
	output.windowBuffer.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content")

	// Manually set cursor to an invalid value (simulating a bug scenario)
	terminal.display.windowCursor = 10

	// Simulate resize
	resizeMsg := tea.WindowSizeMsg{
		Width:  100,
		Height: 30,
	}

	model, _ := terminal.Update(resizeMsg)
	terminal, ok := model.(*Terminal)
	if !ok {
		t.Fatalf("Expected *Terminal, got %T", model)
	}

	// Cursor should be clamped to valid range
	cursor := terminal.display.GetWindowCursor()
	if cursor < -1 || cursor >= 2 {
		t.Errorf("Cursor %d should be clamped to [-1, 1] after resize", cursor)
	}

	// Should be clamped to last window (index 1)
	if cursor != 1 {
		t.Errorf("Expected cursor clamped to 1 (last window), got %d", cursor)
	}
}

// TestTerminalResizeUpdatesDisplayContent tests that display content is
// immediately re-rendered when the terminal is resized (fixes blank display bug).
func TestTerminalResizeUpdatesDisplayContent(t *testing.T) {
	// Create a terminal with initial size
	output := NewTerminalOutput()
	input := stream.NewChanInput(10)
	terminal := NewTerminal(nil, output, input, nil, 80, 24)

	// Add content that will wrap differently at different widths
	longContent := "This is a long line of text that will wrap differently depending on the terminal width"
	output.windowBuffer.AppendOrUpdate("window-1", stream.TagTextAssistant, longContent)

	// Get the initial view
	terminal.display.updateContent()
	initialView := terminal.display.View()
	initialContent := initialView.Content

	// Simulate a resize to a narrower width
	resizeMsg := tea.WindowSizeMsg{
		Width:  40, // Narrower terminal
		Height: 24,
	}

	model, _ := terminal.Update(resizeMsg)
	terminal, ok := model.(*Terminal)
	if !ok {
		t.Fatalf("Expected *Terminal, got %T", model)
	}

	// Get the view after resize
	resizedView := terminal.display.View()
	resizedContent := resizedView.Content

	// The content should have been re-rendered with the new width
	// Verify that the content changed (it should be different due to narrower borders)
	if resizedContent == initialContent {
		t.Errorf("Expected content to change after resize")
	}

	// Verify the borders are narrower (check for shorter horizontal line)
	// The top border should be 40 chars wide instead of 80
	if !strings.Contains(resizedContent, "──────────────────────────────────────") {
		t.Errorf("Expected narrower borders in resized content")
	}

	// Verify the window buffer width was updated
	if output.windowBuffer.Width() != 40 {
		t.Errorf("Expected window buffer width to be 40, got %d", output.windowBuffer.Width())
	}
}
