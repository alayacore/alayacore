package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInputFieldInsertion(t *testing.T) {
	f := NewInputField()
	f.WithWidth(20)

	// Simulate real app flow: call Update with tea.KeyPressMsg
	// passed as tea.Msg interface, the way updateFromMsg does it.
	var msg tea.Msg = tea.KeyPressMsg{Text: "h", Code: 'h'}
	f, _ = f.Update(msg)
	if f.Value() != "h" {
		t.Errorf("expected value 'h', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}

	msg = tea.KeyPressMsg{Text: "e", Code: 'e'}
	f, _ = f.Update(msg)
	if f.Value() != "he" {
		t.Errorf("expected value 'he', got %q", f.Value())
	}
	if f.pos != 2 {
		t.Errorf("expected pos=2, got %d", f.pos)
	}

	// Type "l", "l", "o"
	msg = tea.KeyPressMsg{Text: "l", Code: 'l'}
	f, _ = f.Update(msg)
	f, _ = f.Update(msg)
	msg = tea.KeyPressMsg{Text: "o", Code: 'o'}
	f, _ = f.Update(msg)

	if f.Value() != "hello" {
		t.Errorf("expected value 'hello', got %q", f.Value())
	}
	if f.pos != 5 {
		t.Errorf("expected pos=5, got %d", f.pos)
	}
}

func TestInputFieldBackspace(t *testing.T) {
	f := NewInputField()
	f.WithWidth(20)

	f, _ = f.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	f, _ = f.Update(tea.KeyPressMsg{Text: "b", Code: 'b'})

	if f.Value() != "ab" || f.pos != 2 {
		t.Fatalf("expected 'ab' pos=2, got %q pos=%d", f.Value(), f.pos)
	}

	f, _ = f.Update(tea.KeyPressMsg{Text: "backspace", Code: tea.KeyBackspace})
	if f.Value() != "a" {
		t.Errorf("expected value 'a', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}
}

func TestInputFieldCJKInsertion(t *testing.T) {
	f := NewInputField()
	f.WithWidth(20)

	var msg tea.Msg = tea.KeyPressMsg{Text: "你", Code: '你'}
	f, _ = f.Update(msg)
	if f.Value() != "你" {
		t.Errorf("expected value '你', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}

	msg = tea.KeyPressMsg{Text: "好", Code: '好'}
	f, _ = f.Update(msg)
	if f.Value() != "你好" {
		t.Errorf("expected value '你好', got %q", f.Value())
	}
	if f.pos != 2 {
		t.Errorf("expected pos=2, got %d", f.pos)
	}
}

// TestInputFieldCursorCell verifies CursorCell returns the correct cell
// offset for empty values, ASCII, CJK wide characters, multiline values,
// and horizontally scrolled lines.
func TestInputFieldCursorCell(t *testing.T) {
	// Empty value: cursor at cell 0.
	f := NewInputField()
	if cell := f.CursorCell(); cell != 0 {
		t.Fatalf("empty: got cell %d, want 0", cell)
	}

	// ASCII text, cursor at end.
	f = NewInputField()
	f = f.WithValue("hello").CursorEnd()
	if cell := f.CursorCell(); cell != 5 {
		t.Fatalf("hello end: got cell %d, want 5", cell)
	}

	// CJK wide characters occupy 2 cells each.
	f = NewInputField()
	f = f.WithValue("你好").CursorEnd()
	if cell := f.CursorCell(); cell != 4 {
		t.Fatalf("你好 end: got cell %d, want 4", cell)
	}

	// Cursor in the middle of mixed ASCII/CJK content (before the wide rune).
	f = NewInputField()
	f = f.WithValue("a你b")
	f = f.WithCursorPos(1) // between 'a' (cell 1) and '你' (cells 1-2)
	if cell := f.CursorCell(); cell != 1 {
		t.Fatalf("a你b pos1: got cell %d, want 1", cell)
	}
	// After the wide rune: cell = 1 + 2 = 3.
	f = f.WithCursorPos(2)
	if cell := f.CursorCell(); cell != 3 {
		t.Fatalf("a你b pos2: got cell %d, want 3", cell)
	}

	// Multiline: cursor on the second line — the displayed line is the
	// current line, so the cell offset is relative to that line only.
	f = NewInputField()
	f = f.WithValue("first\nsecond").CursorEnd()
	if cell := f.CursorCell(); cell != 6 {
		t.Fatalf("multiline end: got cell %d, want 6", cell)
	}

	// Horizontal scroll: long line in a narrow viewport, cursor at end.
	// CursorEnd runs ensureCursorVisible, so offset > 0 and the cell is
	// measured relative to the visible window.
	f = NewInputField()
	f = f.WithWidth(5)
	f = f.WithValue("abcdefghij").CursorEnd()
	if f.offset == 0 {
		t.Fatal("setup failed: expected horizontal scroll offset > 0")
	}
	wantCell := 10 - f.offset
	if cell := f.CursorCell(); cell != wantCell {
		t.Fatalf("scrolled end: got cell %d, want %d", cell, wantCell)
	}

	// Horizontal scroll with a wide char before the cursor.
	f = NewInputField()
	f = f.WithWidth(4)
	f = f.WithValue("abcdefgh").CursorEnd()
	f = f.WithCursorPos(2) // after 'a','b' — ensureCursorVisible adjusts
	f = f.ensureCursorVisible()
	wantCell = runesWidth([]rune("ab")) - f.offset
	if cell := f.CursorCell(); cell != wantCell {
		t.Fatalf("scrolled mid: got cell %d, want %d", cell, wantCell)
	}
}

// TestInputFieldViewCursorPosition verifies that buildVisibleText returns the
// correct visible portion of the line. This test caught a bug where
// buildVisibleText was returning cursorIdx=0 because the second loop
// corrupted startIdx (cursorIdx is gone now, but the visible-text logic
// remains).
func TestInputFieldViewCursorPosition(t *testing.T) {
	f := NewInputField()
	f.WithWidth(20)
	f.Focus()

	// Type "hello" through Update calls
	keys := []string{"h", "e", "l", "l", "o"}
	var msg tea.Msg
	for _, k := range keys {
		msg = tea.KeyPressMsg{Text: k, Code: rune(k[0])}
		f, _ = f.Update(msg)
	}

	if f.Value() != "hello" || f.pos != 5 {
		t.Fatalf("setup failed: value=%q pos=%d", f.Value(), f.pos)
	}

	vis := f.buildVisibleText()
	if string(vis) != "hello" {
		t.Errorf("buildVisibleText visible=%q, want 'hello'", string(vis))
	}

	// View() should render the text without any painted cursor cell.
	view := f.View()
	if strings.Contains(view, "\x1b[48") {
		t.Error("input view must not paint a cursor cell (background color)")
	}

	// Blurred rendering must also be cursor-free.
	if strings.Contains(f.Blur().View(), "\x1b[48") {
		t.Error("blurred view must not paint a cursor cell")
	}
}
