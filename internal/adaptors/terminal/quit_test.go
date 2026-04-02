package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/stream"
)

func TestQuitCommandRequiresConfirm(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal(":q should not emit command immediately, should show confirm dialog")
	}

	// Quit confirmation dialog should be shown
	if terminal.confirmDialog != confirmQuit {
		t.Errorf(":q should set confirmDialog to confirmQuit, got %v", terminal.confirmDialog)
	}

	// confirmFromCommand should be set to true
	if !terminal.confirmFromCommand {
		t.Error(":q should set confirmFromCommand to true")
	}
}

func TestQuitCommandCanceledClearsInput(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	terminal.Update(msg)

	// Verify dialog is shown
	if terminal.confirmDialog != confirmQuit {
		t.Fatalf("Expected confirmQuit dialog, got %v", terminal.confirmDialog)
	}

	// Press 'n' to cancel
	msg = tea.KeyPressMsg(tea.Key{Code: 'n'})
	terminal.Update(msg)

	// Input should be cleared after canceling the dialog
	if terminal.input.Value() != "" {
		t.Errorf("Input should be cleared after canceling :q confirmation, got %q", terminal.input.Value())
	}

	// Dialog should be closed
	if terminal.confirmDialog != confirmNone {
		t.Errorf("Dialog should be closed after canceling, got %v", terminal.confirmDialog)
	}
}

func TestQuitCommandEscapeCancels(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	terminal.Update(msg)

	// Verify dialog is shown
	if terminal.confirmDialog != confirmQuit {
		t.Fatalf("Expected confirmQuit dialog, got %v", terminal.confirmDialog)
	}

	// Press Escape to cancel
	msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	terminal.Update(msg)

	// Input should be cleared after canceling the dialog
	if terminal.input.Value() != "" {
		t.Errorf("Input should be cleared after canceling :q confirmation with Escape, got %q", terminal.input.Value())
	}

	// Dialog should be closed
	if terminal.confirmDialog != confirmNone {
		t.Errorf("Dialog should be closed after canceling, got %v", terminal.confirmDialog)
	}
}

func TestQuitCommandConfirmed(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	terminal.Update(msg)

	// Verify dialog is shown
	if terminal.confirmDialog != confirmQuit {
		t.Fatalf("Expected confirmQuit dialog, got %v", terminal.confirmDialog)
	}

	// Press 'y' to confirm
	msg = tea.KeyPressMsg(tea.Key{Code: 'y'})
	model, cmd := terminal.Update(msg)

	// Should return quit command
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd == nil {
		t.Fatal("Pressing 'y' should return quit command")
	}

	// Should be quitting
	if !terminal.quitting {
		t.Error("Terminal should be in quitting state after confirming :q")
	}
}

func TestFullQuitCommandRequiresConfirm(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":quit")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal(":quit should not emit command immediately, should show confirm dialog")
	}

	// Quit confirmation dialog should be shown
	if terminal.confirmDialog != confirmQuit {
		t.Errorf(":quit should set confirmDialog to confirmQuit, got %v", terminal.confirmDialog)
	}

	// confirmFromCommand should be set to true
	if !terminal.confirmFromCommand {
		t.Error(":quit should set confirmFromCommand to true")
	}
}
