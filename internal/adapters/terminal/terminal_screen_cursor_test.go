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
		out:               out,
		display:           NewDisplayModel(out.WindowBuffer(), styles),
		promptBox:         NewPromptInput(styles),
		editor:            NewEditor(),
		modelSelector:     NewModelSelector(styles),
		themeSelector:     NewThemeSelector(styles),
		helpOverlay:       NewHelpOverlay(styles),
		confirmOverlay:    NewConfirmDialog(styles),
		mcpInitOverlay:    NewConfirmDialog(styles),
		attachmentOverlay: NewAttachmentOverlay(styles),
		focusedWindow:     focusPrompt,
		screenWidth:       80,
		screenHeight:      24,
		styles:            styles,
		hasFocus:          true,
	}
	// The display region gets whatever the layout leaves after the input
	// box, the live-edge row and the status bar take their share — going
	// through updateDisplayHeight keeps the fixture from drifting from the
	// real row accounting (View anchors the input box and the live edge
	// with absolute CUPs, so a hand-set height here would overlap them).
	m = m.updateDisplayHeight()
	m.promptBox = m.promptBox.WithWidth(80)
	return m
}

// TestTerminalViewScreenCursor verifies View positions the real terminal cursor
// at the prompt input's logical cursor when the input has focus.
func TestTerminalViewScreenCursor(t *testing.T) {
	m := newTestTerminal()

	// Empty input: cursor at the first content cell of the input line.
	// Layout (24 rows): display 19 lines, the live edge on row 19, the input
	// box's top rule at y=20, its content row at y=21.
	// x = cell (0) — open boxes have no left border or padding.
	v := m.View()
	if v.ScreenCursor == nil {
		t.Fatal("expected real cursor when input is focused")
	}
	if v.ScreenCursor.X != 0 || v.ScreenCursor.Y != 21 {
		t.Fatalf("empty input: got cursor (%d,%d), want (0,21)", v.ScreenCursor.X, v.ScreenCursor.Y)
	}
	if v.ScreenCursor.Shape != ScreenCursorBlock {
		t.Errorf("expected block cursor shape, got %v", v.ScreenCursor.Shape)
	}
	if v.ScreenCursor.Blink {
		t.Error("expected steady (non-blinking) cursor")
	}

	// Typed text moves the cursor right: x = 0 + 2 cells = 2.
	m.promptBox = m.promptBox.WithValue("hi").CaretEnd()
	v = m.View()
	if v.ScreenCursor.X != 2 || v.ScreenCursor.Y != 21 {
		t.Fatalf("with text: got cursor (%d,%d), want (2,21)", v.ScreenCursor.X, v.ScreenCursor.Y)
	}

	// Attachments grow the box UPWARD, not the caret downward: Height()
	// gains the media row, so the bottom-anchored top rule moves up one and
	// AttachmentsOffset() moves the caret back down one — 19 + 1 (first
	// content row) + 1 (media row) = 21, the row it was before. The display
	// region simply gets one row less (24 - 4 - 1 = 19).
	m = m.addAttachment("/tmp/a.txt")
	v = m.View()
	if v.ScreenCursor.X != 2 || v.ScreenCursor.Y != 21 {
		t.Fatalf("with attachment: got cursor (%d,%d), want (2,21)", v.ScreenCursor.X, v.ScreenCursor.Y)
	}
}

// TestTerminalViewScreenCursorHiddenStates verifies the real cursor is hidden
// when no text input has focus.
func TestTerminalViewScreenCursorHiddenStates(t *testing.T) {
	// Display pane focused: input blurred → no cursor.
	m := newTestTerminal()
	m = m.focusDisplay()
	if v := m.View(); v.ScreenCursor != nil {
		t.Fatal("expected no cursor when display pane is focused")
	}

	// Overlay open with list (not filter) focused → no cursor.
	m = newTestTerminal()
	m = m.openModelSelector()
	m.modelSelector.FilteredListCore = m.modelSelector.HandleTabKey() // focus moves to the list
	if v := m.View(); v.ScreenCursor != nil {
		t.Fatal("expected no cursor when overlay list is focused")
	}
}

// TestTerminalViewOverlayCaret verifies the real cursor moves into the
// focused overlay filter box and follows the filter text.
func TestTerminalViewOverlayCaret(t *testing.T) {
	m := newTestTerminal()
	m = m.openModelSelector() // filter input focused by default

	// Expected: box origin (renderOverlay formula) + prompt (empty — no
	// "/" prefix anymore) + cell (0) horizontally; + title/rule (2)
	// vertically. Open boxes have no left border or padding.
	box := m.modelSelector.View().Content
	x0, y0 := overlayOrigin(box, 80, 24)
	v := m.View()
	if v.ScreenCursor == nil {
		t.Fatal("expected real cursor in overlay filter when filter is focused")
	}
	if v.ScreenCursor.X != x0 || v.ScreenCursor.Y != y0+2 {
		t.Fatalf("overlay cursor: got (%d,%d), want (%d,%d)", v.ScreenCursor.X, v.ScreenCursor.Y, x0, y0+2)
	}

	// Typing in the filter moves the cursor right by the text width.
	m.modelSelector.FilterInput = m.modelSelector.FilterInput.WithValue("gpt4").CaretEnd()
	v = m.View()
	if v.ScreenCursor.X != x0+4 || v.ScreenCursor.Y != y0+2 {
		t.Fatalf("overlay cursor with text: got (%d,%d), want (%d,%d)", v.ScreenCursor.X, v.ScreenCursor.Y, x0+4, y0+2)
	}
}

// TestTerminalViewThemeOverlayCaret smoke-tests the same wiring through the
// theme selector (each overlay delegates to FilteredListCore.CaretPosition).
func TestTerminalViewThemeOverlayCaret(t *testing.T) {
	m := newTestTerminal()
	// Open the theme selector directly: openThemeSelector requires a
	// themeManager, which newTestTerminal doesn't set up.
	m.themeSelector = m.themeSelector.Open(nil, "")
	m.promptBox = m.promptBox.WithActive(false)

	box := m.themeSelector.View().Content
	x0, y0 := overlayOrigin(box, 80, 24)
	v := m.View()
	if v.ScreenCursor == nil {
		t.Fatal("expected real cursor in theme selector filter")
	}
	if v.ScreenCursor.X != x0 || v.ScreenCursor.Y != y0+2 {
		t.Fatalf("theme overlay cursor: got (%d,%d), want (%d,%d)", v.ScreenCursor.X, v.ScreenCursor.Y, x0, y0+2)
	}
}
