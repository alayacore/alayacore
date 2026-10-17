package terminal

import (
	"testing"
)

// TestTerminalViewScreenCursorMultiline verifies the real cursor stays on the
// input box's single content line even when the value spans multiple lines:
// InputField renders only the current line, so the cursor's screen y must not
// include the value-line index.
func TestTerminalViewScreenCursorMultiline(t *testing.T) {
	m := newTestTerminal()
	m.promptBox = m.promptBox.WithValue("line1\nline2").CaretEnd() // cursor on line 2

	v := m.View()
	if v.ScreenCursor == nil {
		t.Fatal("expected real cursor when input is focused")
	}
	// Input box content is always a single line: rows 0-18 are the display,
	// row 19 the live edge, row 20 the box's top rule — so the content line
	// is y=21 whatever the value holds.
	if v.ScreenCursor.Y != 21 {
		t.Fatalf("multiline cursor: got y=%d, want 21 (input box content line)", v.ScreenCursor.Y)
	}
	// x = cells of "line2" (5) — open boxes have no left border/padding.
	if v.ScreenCursor.X != 5 {
		t.Fatalf("multiline cursor: got x=%d, want %d", v.ScreenCursor.X, 5)
	}

	// ScreenCursor on the first line of a multiline value.
	m.promptBox = m.promptBox.WithValue("line1\nline2")
	m.promptBox.field = m.promptBox.field.WithCaretPos(0) // same-package access to the inner field
	v = m.View()
	if v.ScreenCursor.X != 0 || v.ScreenCursor.Y != 21 {
		t.Fatalf("multiline first line: got cursor (%d,%d), want (0,21)", v.ScreenCursor.X, v.ScreenCursor.Y)
	}
}
