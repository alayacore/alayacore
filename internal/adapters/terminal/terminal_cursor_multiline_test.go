package terminal

import (
	"testing"
)

// TestTerminalViewRealCursorMultiline verifies the real cursor stays on the
// input box's single content line even when the value spans multiple lines:
// InputField renders only the current line, so the cursor's screen y must not
// include the value-line index.
func TestTerminalViewRealCursorMultiline(t *testing.T) {
	m := newTestTerminal()
	m.input = m.input.WithValue("line1\nline2").CursorEnd() // cursor on line 2

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("expected real cursor when input is focused")
	}
	// Input box content is always a single line: y = displayH(20) + border(1).
	if v.Cursor.Y != 21 {
		t.Fatalf("multiline cursor: got y=%d, want 21 (input box content line)", v.Cursor.Y)
	}
	// x = left border/padding (2) + cells of "line2" (5).
	if v.Cursor.X != 2+5 {
		t.Fatalf("multiline cursor: got x=%d, want %d", v.Cursor.X, 2+5)
	}

	// Cursor on the first line of a multiline value.
	m.input = m.input.WithValue("line1\nline2")
	m.input.input = m.input.input.WithCursorPos(0) // same-package access to the inner field
	v = m.View()
	if v.Cursor.X != 2 || v.Cursor.Y != 21 {
		t.Fatalf("multiline first line: got cursor (%d,%d), want (2,21)", v.Cursor.X, v.Cursor.Y)
	}
}
