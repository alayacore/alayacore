package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/stream"
)

func TestSpaceKeyTogglesFold(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusDisplay()

	// Add a window with content that can be folded
	terminal.out.WindowBuffer().AppendOrUpdate("test1", stream.TagTextAssistant, "Hello world")

	// Set cursor to first window
	terminal.display.SetWindowCursor(0)

	// Verify window is not folded initially
	windows := terminal.out.WindowBuffer().Windows
	if len(windows) == 0 {
		t.Fatal("No windows created")
	}
	if windows[0].Folded {
		t.Error("Window should not be folded initially")
	}

	// Press Space key to fold
	msg := tea.KeyPressMsg(tea.Key{Code: ' '})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command (just toggles fold)
	if cmd != nil {
		t.Errorf("Space key should not emit command, got %v", cmd)
	}

	// Window should now be folded
	if !windows[0].Folded {
		t.Error("Window should be folded after pressing Space")
	}

	// Press Space again to unfold
	msg = tea.KeyPressMsg(tea.Key{Code: ' '})
	terminal.Update(msg)

	// Window should be unfolded again
	if windows[0].Folded {
		t.Error("Window should be unfolded after pressing Space again")
	}
}

func TestSpaceKeyDoesNothingInInputWindow(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusInput()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate("test1", stream.TagTextAssistant, "Hello world")

	// Press Space key while in input window
	msg := tea.KeyPressMsg(tea.Key{Code: ' '})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Space key in input window should not emit command, got %v", cmd)
	}

	// The space should be passed to input handler (we don't verify input value here
	// as that tests input component behavior, not the space key routing)
}

func TestSpaceKeyDoesNothingWithNoWindow(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusDisplay()

	// No windows in buffer
	terminal.display.SetWindowCursor(-1)

	// Press Space key
	msg := tea.KeyPressMsg(tea.Key{Code: ' '})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Space key with no window should not emit command, got %v", cmd)
	}

	// No windows should be created
	windows := terminal.out.WindowBuffer().Windows
	if len(windows) != 0 {
		t.Errorf("Expected no windows, got %d", len(windows))
	}
}
