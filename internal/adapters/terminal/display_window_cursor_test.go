package terminal

import (
	"testing"
)

func TestValidateWindowCursor_ClampsOutOfRangeCursor(t *testing.T) {
	// Create a display with a window buffer
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add some windows
	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")
	wb.AppendOrUpdate(tagAssistantText, "window-3", "Content 3")

	// Set cursor to the middle window
	display = display.WithWindowCursor(1)
	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", display.GetWindowCursor())
	}

	// Set cursor beyond the range
	display.windowCursor = 10

	// Validate should clamp it
	display = display.ValidateWindowCursor()

	if display.GetWindowCursor() != 2 {
		t.Errorf("Expected cursor clamped to 2 (last window), got %d", display.GetWindowCursor())
	}
}

func TestValidateWindowCursor_HandlesNegativeCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add some windows
	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")

	// Set cursor to invalid negative value
	display.windowCursor = -5

	// Validate should clamp it to -1
	display = display.ValidateWindowCursor()

	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor clamped to -1, got %d", display.GetWindowCursor())
	}
}

func TestValidateWindowCursor_HandlesEmptyBuffer(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// No windows added, cursor should stay at -1
	display.windowCursor = 5

	display = display.ValidateWindowCursor()

	// With no windows, cursor should be -1
	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor -1 for empty buffer, got %d", display.GetWindowCursor())
	}
}

func TestValidateWindowCursor_KeepsValidCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add windows
	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")
	wb.AppendOrUpdate(tagAssistantText, "window-3", "Content 3")

	// Set cursor to valid position
	display = display.WithWindowCursor(1)

	// Validate should keep it
	display = display.ValidateWindowCursor()

	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor to stay at 1, got %d", display.GetWindowCursor())
	}
}
