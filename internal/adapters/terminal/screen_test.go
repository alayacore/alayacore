package terminal

import (
	"bytes"
	"image/color"
	"strings"
	"testing"
)

// TestScreenRenderRaw verifies the raw render output byte sequence:
// ED2 + home + content + absolute CUP for the cursor.
func TestScreenRenderRaw(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}

	cur := NewCursor(3, 5)
	err := s.Render("line1\nline2", cur)
	if err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "\x1b[2J\x1b[H") {
		t.Errorf("render should start with ED2+home, got %q", out[:min(len(out), 20)])
	}
	if !strings.Contains(out, "line1\r\nline2") {
		t.Errorf("render should emit CRLF for newlines (raw mode has no ONLCR), got %q", out)
	}
	if strings.Contains(out, "line1\nline2") {
		t.Errorf("render must not emit bare LF newlines in raw mode, got %q", out)
	}
	if !strings.Contains(out, "\x1b[6;4H") {
		t.Errorf("render should position cursor with absolute CUP (row 6, col 4), got %q", out)
	}
	if !strings.Contains(out, "\x1b[?25h") {
		t.Errorf("render should show cursor, got %q", out)
	}
}

// TestScreenRenderNoNewlinePassthrough verifies content without newlines is
// written verbatim (no spurious CR).
func TestScreenRenderNoNewlinePassthrough(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Render("plain content", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "plain content") {
		t.Errorf("content should be written verbatim, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "\r") {
		t.Errorf("no-newline content should not gain CR, got %q", buf.String())
	}
}

// TestScreenRenderNoCursor verifies the cursor is hidden when nil.
func TestScreenRenderNoCursor(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Render("content", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[?25l") {
		t.Errorf("render without cursor should hide it, got %q", buf.String())
	}
}

// TestScreenRenderSkipIdentical verifies unchanged content+cursor skips the
// write entirely.
func TestScreenRenderSkipIdentical(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	cur := NewCursor(0, 0)
	if err := s.Render("same", cur); err != nil {
		t.Fatal(err)
	}
	first := buf.String()
	if err := s.Render("same", cur); err != nil {
		t.Fatal(err)
	}
	if buf.String() != first {
		t.Errorf("identical render should be skipped, got %q", buf.String()[len(first):])
	}
	// Cursor position change must re-render.
	cur2 := NewCursor(1, 0)
	if err := s.Render("same", cur2); err != nil {
		t.Fatal(err)
	}
	if buf.String() == first {
		t.Error("cursor position change should re-render")
	}
}

// TestScreenRenderCursorStyle verifies cursor shape/color encoding.
func TestScreenRenderCursorStyle(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}

	cur := NewCursor(0, 0)
	cur.Shape = CursorBlock
	cur.Blink = false
	cur.Color = color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}

	if err := s.Render("x", cur); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b]12;#123456\x07") {
		t.Errorf("render should set cursor color #123456, got %q", out)
	}
	if !strings.Contains(out, "\x1b[2 q") {
		t.Errorf("render should set steady block cursor style (2 q), got %q", out)
	}
}

// TestScreenStartStop verifies alt screen + paste/focus mode sequences.
func TestScreenStartStop(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[?1049h", // alt screen
		"\x1b[?2004h", // bracketed paste
		"\x1b[?1004h", // focus reporting
		"\x1b[?25l",   // hide cursor
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Start() missing %q, got %q", want, out)
		}
	}
	buf.Reset()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	for _, want := range []string{
		"\x1b[?2004l", // reset paste
		"\x1b[?1004l", // reset focus
		"\x1b[?25h",   // show cursor
		"\x1b[?1049l", // leave alt screen
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Stop() missing %q, got %q", want, out)
		}
	}
}

// TestScreenResizeForcesRedraw verifies Resize invalidates the frame.
func TestScreenResizeForcesRedraw(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Render("same", nil); err != nil {
		t.Fatal(err)
	}
	first := buf.String()
	s.Resize(100, 50)
	if err := s.Render("same", nil); err != nil {
		t.Fatal(err)
	}
	if buf.String() == first {
		t.Error("Resize should force a full redraw")
	}
}

// TestColorHex verifies the image/color → #rrggbb conversion.
func TestColorHex(t *testing.T) {
	hex, ok := colorHex(color.RGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff})
	if !ok || hex != "#abcdef" {
		t.Errorf("colorHex = %q, %v; want #abcdef, true", hex, ok)
	}
}
