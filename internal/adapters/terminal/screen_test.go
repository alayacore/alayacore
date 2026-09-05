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
	err := s.Render("line1\nline2", cur, false)
	if err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "\x1b[2J\x1b[H") {
		t.Errorf("non-full-screen render should start with ED2+home, got %q", out[:min(len(out), 20)])
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

// TestScreenRenderFullScreenNoClear locks the flicker-free overlay render
// path: consecutive full-screen frames overwrite without ED2, but the
// first frame (fill-mode change) clears once so no previous content can
// leak through.
func TestScreenRenderFullScreenNoClear(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}

	// First full-screen frame: mode change from unknown → clears once.
	if err := s.Render("row1\r\nrow2\r\n", nil, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\x1b[2J\x1b[H") {
		t.Errorf("first full-screen render should clear (mode change), got %q", out)
	}

	// Subsequent full-screen frames: no clearing (flicker-free) and only
	// the CHANGED row is repainted (row diff), not the whole frame.
	buf.Reset()
	if err := s.Render("row1\r\nrow2\r\nCHANGED", nil, true); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("steady full-screen render must not clear the screen, got %q", out)
	}
	if !strings.HasPrefix(out, "\x1b[?25l") {
		t.Errorf("steady frame must hide the cursor before painting, got %q", out)
	}
	if !strings.Contains(out, "\x1b[3;1HCHANGED") {
		t.Errorf("steady frame should repaint only the changed row, got %q", out)
	}
	if strings.Contains(out, "row1") || strings.Contains(out, "row2") {
		t.Errorf("unchanged rows must not be rewritten, got %q", out)
	}
}

// TestScreenRenderOverlayResidueCleanup locks the one-frame ED2 fallback:
// a frame that previously drew CUP overlay rows but no longer does must
// clear the residue.
func TestScreenRenderOverlayResidueCleanup(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}

	// Overlay frame: base + CUP-positioned rows.
	overlayFrame := "base row\n\x1b[10;20Hoverlay row"
	if err := s.Render(overlayFrame, nil, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[10;20H") {
		t.Fatalf("overlay frame should contain CUP rows, got %q", buf.String())
	}
	first := buf.String()
	if !strings.HasPrefix(first, "\x1b[2J") {
		t.Fatalf("first full-screen frame should clear once (mode change), got %q", first)
	}

	// Second overlay frame: same mode, no clearing, and only the NEW base
	// row is repainted — the unchanged overlay CUP row is not rewritten.
	buf.Reset()
	if err := s.Render(overlayFrame+"\nmore", nil, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b[2J") {
		t.Fatalf("steady full-screen frame should not clear, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "\x1b[10;20H") {
		t.Fatalf("unchanged overlay row must not be rewritten (row diff), got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\x1b[11;1Hmore") {
		t.Fatalf("new base row should be repainted in place, got %q", buf.String())
	}

	// Next frame has no overlay rows — must clear once (ED2) to erase residue.
	buf.Reset()
	if err := s.Render("base row\n", nil, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[2J") {
		t.Errorf("frame after overlay must clear once for residue, got %q", buf.String())
	}

	// Subsequent frames without overlays go back to clear-free rendering.
	buf.Reset()
	if err := s.Render("base row\n", nil, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b[2J") {
		t.Errorf("steady full-screen frames must not clear, got %q", buf.String())
	}
}

// TestScreenTabRepaintsOnlyChangedRows locks the Tab-focus flicker fix at
// the Screen level: when only the overlay's focus-dependent rows change,
// the steady frame repaints exactly those rows (row diff) — never the
// whole frame — and the cursor is hidden before any painting (so an
// incrementally painting terminal never shows it traveling with the text).
func TestScreenTabRepaintsOnlyChangedRows(t *testing.T) {
	m := newTestTerminal()
	m = m.openModelSelector()
	tab := KeyPressMsg(Key{Code: KeyTab})

	v1 := m.View()
	buf := &bytes.Buffer{}
	s := &Screen{out: buf}
	if err := s.Render(v1.Content, v1.Cursor, true); err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	mm, _ := m.Update(tab)
	m = mm.(Terminal)
	v2 := m.View()
	if err := s.Render(v2.Content, v2.Cursor, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("steady Tab frame must not clear the screen, got %q", out)
	}
	if !strings.HasPrefix(out, "\x1b[?25l") {
		t.Errorf("cursor must be hidden before painting, got %q", out)
	}
	// Unchanged rows must not be rewritten: the overlay title row and the
	// base content stay untouched on Tab.
	if strings.Contains(out, "Model Selector") {
		t.Errorf("unchanged overlay title row must not be rewritten, got %q", out)
	}
	// Only the focus-dependent rows change: the help bar swaps to the
	// list-focus text.
	if !strings.Contains(out, "tab: search |") {
		t.Errorf("changed help row should be repainted, got %q", out)
	}
}

// TestScreenRenderNoNewlinePassthrough verifies content without newlines is
// written verbatim (no CRLF conversion needed).
func TestScreenRenderNoNewlinePassthrough(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Render("plain content", nil, true); err != nil {
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
	if err := s.Render("content", nil, false); err != nil {
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
	if err := s.Render("same", cur, true); err != nil {
		t.Fatal(err)
	}
	first := buf.String()
	if err := s.Render("same", cur, true); err != nil {
		t.Fatal(err)
	}
	if buf.String() != first {
		t.Errorf("identical render should be skipped, got %q", buf.String()[len(first):])
	}
	// Cursor position change must re-render.
	cur2 := NewCursor(1, 0)
	if err := s.Render("same", cur2, true); err != nil {
		t.Fatal(err)
	}
	if buf.String() == first {
		t.Error("cursor position change should re-render")
	}
	// FullScreen mode change must re-render.
	if err := s.Render("same", cur2, false); err != nil {
		t.Fatal(err)
	}
	if buf.String() == first {
		t.Error("FullScreen change should re-render")
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

	if err := s.Render("x", cur, true); err != nil {
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
	if err := s.Render("same", nil, true); err != nil {
		t.Fatal(err)
	}
	first := buf.String()
	s.Resize(100, 50)
	if err := s.Render("same", nil, true); err != nil {
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
