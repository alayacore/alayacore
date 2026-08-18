package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestSpaceKeyTogglesFold(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()

	// Add a window with content that can be folded
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", "Hello world")

	// Set cursor to first window
	terminal.display = terminal.display.WithWindowCursor(0)

	// Verify window is not folded initially
	windows := terminal.out.WindowBuffer().AllWindows()
	if len(windows) == 0 {
		t.Fatal("No windows created")
	}
	if windows[0].Folded {
		t.Error("Window should not be folded initially")
	}

	// Press Space key to fold
	msg := KeyPressMsg(Key{Code: ' '})

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
	msg = KeyPressMsg(Key{Code: ' '})
	terminal.Update(msg)

	// Window should be unfolded again
	if windows[0].Folded {
		t.Error("Window should be unfolded after pressing Space again")
	}
}

func TestSpaceKeyDoesNothingInInputWindow(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusInput()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", "Hello world")

	// Press Space key while in input window
	msg := KeyPressMsg(Key{Code: ' '})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Space key in input window should not emit command, got %v", cmd)
	}

	// The space should be passed to input handler (we don't verify input value here
	// as that tests prompt input behavior, not the space key routing)
}

func TestSpaceKeyDoesNothingWithNoWindow(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()

	// No windows in buffer
	terminal.display = terminal.display.WithWindowCursor(-1)

	// Press Space key
	msg := KeyPressMsg(Key{Code: ' '})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Space key with no window should not emit command, got %v", cmd)
	}

	// No windows should be created
	windows := terminal.out.WindowBuffer().AllWindows()
	if len(windows) != 0 {
		t.Errorf("Expected no windows, got %d", len(windows))
	}
}
