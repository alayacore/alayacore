package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newTestTerminal returns a minimal Terminal with the input focused,
// sized 80x24, ready for View() assertions.
func newTestTerminal() Terminal {
	out := NewTerminalOutput(DefaultStyles())
	styles := DefaultStyles()
	m := Terminal{
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
	m.display = m.display.WithWidth(80).WithHeight(20)
	m.input = m.input.WithWidth(80)
	return m
}

// TestTerminalViewRealCursor verifies View positions the real terminal cursor
// at the prompt input's logical cursor when the input has focus, and hides it
// when an overlay is open or the display pane has focus.
func TestTerminalViewRealCursor(t *testing.T) {
	m := newTestTerminal()

	// Empty input: cursor at the first content cell of the input line.
	// Layout: display 20 lines + input box top border at y=20, content at y=21.
	// x = left border (1) + left padding (1) + cell (0) = 2.
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("expected real cursor when input is focused")
	}
	if v.Cursor.X != 2 || v.Cursor.Y != 21 {
		t.Fatalf("empty input: got cursor (%d,%d), want (2,21)", v.Cursor.X, v.Cursor.Y)
	}
	if v.Cursor.Shape != tea.CursorBlock {
		t.Errorf("expected block cursor shape, got %v", v.Cursor.Shape)
	}
	if v.Cursor.Blink {
		t.Error("expected steady (non-blinking) cursor")
	}

	// Typed text moves the cursor right: x = 2 + 2 cells = 4.
	m.input = m.input.WithValue("hi").CursorEnd()
	v = m.View()
	if v.Cursor.X != 4 || v.Cursor.Y != 21 {
		t.Fatalf("with text: got cursor (%d,%d), want (4,21)", v.Cursor.X, v.Cursor.Y)
	}

	// Attachments push the input text down: display height shrinks to
	// 24-5-1 = 18, content line = 18 + 1 (border) + 2 (media+separator) = 21.
	m = m.addAttachment("/tmp/a.txt")
	v = m.View()
	if v.Cursor.X != 4 || v.Cursor.Y != 21 {
		t.Fatalf("with attachment: got cursor (%d,%d), want (4,21)", v.Cursor.X, v.Cursor.Y)
	}
}

// TestTerminalViewRealCursorHiddenStates verifies the real cursor is hidden
// when the input is not the active focus target.
func TestTerminalViewRealCursorHiddenStates(t *testing.T) {
	// Display pane focused: input blurred → no cursor.
	m := newTestTerminal()
	m = m.focusDisplay()
	if v := m.View(); v.Cursor != nil {
		t.Fatal("expected no cursor when display pane is focused")
	}

	// Overlay open: input blurred → no cursor.
	m = newTestTerminal()
	m = m.openModelSelector()
	if v := m.View(); v.Cursor != nil {
		t.Fatal("expected no cursor when an overlay is open")
	}
}
