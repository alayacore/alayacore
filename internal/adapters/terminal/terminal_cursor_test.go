package terminal

import (
	"testing"
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
// at the prompt input's logical cursor when the input has focus.
func TestTerminalViewRealCursor(t *testing.T) {
	m := newTestTerminal()

	// Empty input: cursor at the first content cell of the input line.
	// Layout: display 20 lines + input box top rule at y=20, content at y=21.
	// x = cell (0) — open boxes have no left border or padding.
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("expected real cursor when input is focused")
	}
	if v.Cursor.X != 0 || v.Cursor.Y != 21 {
		t.Fatalf("empty input: got cursor (%d,%d), want (0,21)", v.Cursor.X, v.Cursor.Y)
	}
	if v.Cursor.Shape != CursorBlock {
		t.Errorf("expected block cursor shape, got %v", v.Cursor.Shape)
	}
	if v.Cursor.Blink {
		t.Error("expected steady (non-blinking) cursor")
	}

	// Typed text moves the cursor right: x = 0 + 2 cells = 2.
	m.input = m.input.WithValue("hi").CursorEnd()
	v = m.View()
	if v.Cursor.X != 2 || v.Cursor.Y != 21 {
		t.Fatalf("with text: got cursor (%d,%d), want (2,21)", v.Cursor.X, v.Cursor.Y)
	}

	// Attachments push the input text down: display height shrinks to
	// 24-5-1 = 18, content line = 18 + 1 (rule) + 2 (media+separator) = 21.
	m = m.addAttachment("/tmp/a.txt")
	v = m.View()
	if v.Cursor.X != 2 || v.Cursor.Y != 21 {
		t.Fatalf("with attachment: got cursor (%d,%d), want (2,21)", v.Cursor.X, v.Cursor.Y)
	}
}

// TestTerminalViewRealCursorHiddenStates verifies the real cursor is hidden
// when no text input has focus.
func TestTerminalViewRealCursorHiddenStates(t *testing.T) {
	// Display pane focused: input blurred → no cursor.
	m := newTestTerminal()
	m = m.focusDisplay()
	if v := m.View(); v.Cursor != nil {
		t.Fatal("expected no cursor when display pane is focused")
	}

	// Overlay open with list (not filter) focused → no cursor.
	m = newTestTerminal()
	m = m.openModelSelector()
	m.modelSelector.FilteredListCore = m.modelSelector.HandleTabKey() // focus moves to the list
	if v := m.View(); v.Cursor != nil {
		t.Fatal("expected no cursor when overlay list is focused")
	}
}

// TestTerminalViewOverlayCursor verifies the real cursor moves into the
// focused overlay filter box and follows the filter text.
func TestTerminalViewOverlayCursor(t *testing.T) {
	m := newTestTerminal()
	m = m.openModelSelector() // filter input focused by default

	// Expected: box origin (renderOverlay formula) + prompt (empty — no
	// "/" prefix anymore) + cell (0) horizontally; + title/rule (2)
	// vertically. Open boxes have no left border or padding.
	box := m.modelSelector.View().Content
	x0, y0 := overlayOrigin(box, 80, 24)
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("expected real cursor in overlay filter when filter is focused")
	}
	if v.Cursor.X != x0 || v.Cursor.Y != y0+2 {
		t.Fatalf("overlay cursor: got (%d,%d), want (%d,%d)", v.Cursor.X, v.Cursor.Y, x0, y0+2)
	}

	// Typing in the filter moves the cursor right by the text width.
	m.modelSelector.FilterInput = m.modelSelector.FilterInput.WithValue("gpt4").CursorEnd()
	v = m.View()
	if v.Cursor.X != x0+4 || v.Cursor.Y != y0+2 {
		t.Fatalf("overlay cursor with text: got (%d,%d), want (%d,%d)", v.Cursor.X, v.Cursor.Y, x0+4, y0+2)
	}
}

// TestTerminalViewThemeOverlayCursor smoke-tests the same wiring through the
// theme selector (each overlay delegates to FilteredListCore.CursorPosition).
func TestTerminalViewThemeOverlayCursor(t *testing.T) {
	m := newTestTerminal()
	// Open the theme selector directly: openThemeSelector requires a
	// themeManager, which newTestTerminal doesn't set up.
	m.themeSelector = m.themeSelector.Open(nil, "")
	m.input = m.input.Blur()

	box := m.themeSelector.View().Content
	x0, y0 := overlayOrigin(box, 80, 24)
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("expected real cursor in theme selector filter")
	}
	if v.Cursor.X != x0 || v.Cursor.Y != y0+2 {
		t.Fatalf("theme overlay cursor: got (%d,%d), want (%d,%d)", v.Cursor.X, v.Cursor.Y, x0, y0+2)
	}
}
