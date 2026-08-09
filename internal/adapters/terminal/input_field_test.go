package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// TestInputFieldCursorCell verifies CursorCell returns the correct line and
// cell offset for empty values, ASCII, CJK wide characters, multiline values,
// and horizontally scrolled lines.
func TestInputFieldCursorCell(t *testing.T) {
	// Empty value: cursor at (0, 0).
	f := NewInputField()
	if line, cell := f.CursorCell(); line != 0 || cell != 0 {
		t.Fatalf("empty: got (%d,%d), want (0,0)", line, cell)
	}

	// ASCII text, cursor at end.
	f = NewInputField()
	f = f.WithValue("hello").CursorEnd()
	if line, cell := f.CursorCell(); line != 0 || cell != 5 {
		t.Fatalf("hello end: got (%d,%d), want (0,5)", line, cell)
	}

	// CJK wide characters occupy 2 cells each.
	f = NewInputField()
	f = f.WithValue("你好").CursorEnd()
	if line, cell := f.CursorCell(); line != 0 || cell != 4 {
		t.Fatalf("你好 end: got (%d,%d), want (0,4)", line, cell)
	}

	// Cursor in the middle of mixed ASCII/CJK content (before the wide rune).
	f = NewInputField()
	f = f.WithValue("a你b")
	f = f.WithCursorPos(1) // between 'a' (cell 1) and '你' (cells 1-2)
	if line, cell := f.CursorCell(); line != 0 || cell != 1 {
		t.Fatalf("a你b pos1: got (%d,%d), want (0,1)", line, cell)
	}
	// After the wide rune: cell = 1 + 2 = 3.
	f = f.WithCursorPos(2)
	if line, cell := f.CursorCell(); line != 0 || cell != 3 {
		t.Fatalf("a你b pos2: got (%d,%d), want (0,3)", line, cell)
	}

	// Multiline: cursor on the second line.
	f = NewInputField()
	f = f.WithValue("first\nsecond").CursorEnd()
	if line, cell := f.CursorCell(); line != 1 || cell != 6 {
		t.Fatalf("multiline end: got (%d,%d), want (1,6)", line, cell)
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
	line, cell := f.CursorCell()
	if line != 0 || cell != wantCell {
		t.Fatalf("scrolled end: got (%d,%d), want (0,%d)", line, cell, wantCell)
	}

	// Horizontal scroll with a wide char before the cursor.
	f = NewInputField()
	f = f.WithWidth(4)
	f = f.WithValue("abcdefgh").CursorEnd()
	f = f.WithCursorPos(2) // after 'a','b' — but cursor visible? ensureCursorVisible adjusts
	f = f.ensureCursorVisible()
	line, cell = f.CursorCell()
	wantCell = runesWidth([]rune("ab")) - f.offset
	if cell != wantCell {
		t.Fatalf("scrolled mid: got (%d,%d), want (0,%d)", line, cell, wantCell)
	}
}

// TestInputFieldFakeCursorToggle verifies WithFakeCursor toggles the painted
// block cursor: on by default (overlays), off for the main prompt input.
func TestInputFieldFakeCursorToggle(t *testing.T) {
	f := NewInputField()
	f = f.WithStyles(
		inputFieldStyle{Prompt: lipgloss.NewStyle()},
		inputFieldStyle{},
		lipgloss.Color("#00ff00"),
	)
	f = f.WithValue("hi").CursorEnd()

	withFake := f.View()
	if !strings.Contains(withFake, "\x1b[48") {
		t.Error("default view should paint the fake cursor (background color)")
	}

	withoutFake := f.WithFakeCursor(false).View()
	if strings.Contains(withoutFake, "\x1b[48") {
		t.Error("view with fake cursor disabled should not paint a background cursor")
	}
	if withFake == withoutFake {
		t.Error("fake cursor on/off should produce different views")
	}

	// Blurred rendering never paints the fake cursor, with or without the flag.
	if strings.Contains(f.Blur().View(), "\x1b[48") {
		t.Error("blurred view should not paint a cursor")
	}
}

// TestInputFieldViewCursorPosition verifies that View renders the cursor
// at the correct position. This test caught a bug where buildVisibleText
// was returning cursorIdx=0 because the second loop corrupted startIdx.
func TestInputFieldViewCursorPosition(t *testing.T) {
	f := NewInputField()
	f.WithWidth(20)
	f.Focus() // needed to initialize cursorRender

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

	// buildVisibleText should return cursorIdx=5 (not 0!)
	vis, cursorIdx := f.buildVisibleText()
	if cursorIdx != 5 {
		t.Errorf("buildVisibleText cursorIdx=%d, want 5", cursorIdx)
	}
	if string(vis) != "hello" {
		t.Errorf("buildVisibleText visible=%q, want 'hello'", string(vis))
	}

	// View() should render correctly
	_ = f.View()
}
