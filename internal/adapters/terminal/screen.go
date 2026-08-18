package terminal

// Terminal screen management: alt screen, cursor, and the raw passthrough
// renderer. This is module 4 of the self-built TUI stack (see
// REFACTOR.md §8.3).
//
// Render writes the view content VERBATIM after clearing the screen and
// homing the cursor: `ED2` + home + content + absolute CUP. This is the
// same raw passthrough logic that was proven in the forked bubbletea
// renderer (third_party/bubbletea/cursed_renderer.go, Raw branch): the
// terminal soft-wraps the content natively, so window fragments (continuous
// text padded to the terminal width) land exactly on the intended visual
// rows and selections copy without fake newlines.

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// Screen manages the terminal: alt screen mode, cursor, paste/focus
// reporting, and raw rendering.
type Screen struct {
	out      io.Writer
	sizeFile *os.File // used for term.GetSize; nil when no real TTY

	mu          sync.Mutex // serializes writes
	width       int
	height      int
	started     bool
	lastContent string
	lastCursor  *Cursor
}

// NewScreen creates a Screen writing to out.
func NewScreen(out *os.File) *Screen {
	return &Screen{out: out, sizeFile: out}
}

// Start enters the alt screen and enables bracketed paste and focus
// reporting. The cursor is hidden until the first render.
func (s *Screen) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	var buf []byte
	buf = append(buf, ansi.SetModeAltScreenSaveCursor...)
	buf = append(buf, ansi.SetModeBracketedPaste...)
	buf = append(buf, ansi.SetModeFocusEvent...)
	buf = append(buf, ansi.ResetModeTextCursorEnable...)
	if _, err := s.out.Write(buf); err != nil {
		return fmt.Errorf("terminal: enter alt screen: %w", err)
	}
	s.started = true
	return nil
}

// Stop restores the cursor and leaves the alt screen. It is safe to call
// multiple times (idempotent) — always defer it.
func (s *Screen) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil
	}
	var buf []byte
	buf = append(buf, ansi.ResetModeBracketedPaste...)
	buf = append(buf, ansi.ResetModeFocusEvent...)
	buf = append(buf, ansi.SetModeTextCursorEnable...)
	buf = append(buf, ansi.ResetModeAltScreenSaveCursor...)
	if _, err := s.out.Write(buf); err != nil {
		return fmt.Errorf("terminal: exit alt screen: %w", err)
	}
	s.started = false
	return nil
}

// Render clears the screen, homes the cursor, writes the content verbatim,
// and positions the real cursor at the given position (or hides it). It is
// a no-op when the content and cursor are unchanged since the last render.
func (s *Screen) Render(content string, cur *Cursor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sameContent := content == s.lastContent
	sameCursor := cursorsEqual(s.lastCursor, cur)
	if sameContent && sameCursor {
		return nil
	}

	var buf []byte
	buf = append(buf, ansi.EraseDisplay(2)...)
	buf = append(buf, ansi.CursorHomePosition...)
	buf = append(buf, content...)
	if cur != nil {
		// Absolute CUP: the terminal soft-wraps the content, so absolute
		// positioning is the only reliable way to land on a cell. The
		// soft-wrapped grid matches the visual rows (fragments are padded
		// to the full terminal width), so the position is exact.
		buf = append(buf, ansi.CursorPosition(cur.X+1, cur.Y+1)...)
		buf = append(buf, ansi.SetModeTextCursorEnable...)
		if cur.Color != nil {
			if hex, ok := colorHex(cur.Color); ok {
				buf = append(buf, ansi.SetCursorColor(hex)...)
			}
		}
		if style := encodeCursorStyle(cur.Shape, cur.Blink); style != 0 && style != 1 {
			buf = append(buf, ansi.SetCursorStyle(style)...)
		}
	} else {
		buf = append(buf, ansi.ResetModeTextCursorEnable...)
	}

	if _, err := s.out.Write(buf); err != nil {
		return fmt.Errorf("terminal: render: %w", err)
	}

	s.lastContent = content
	if cur != nil {
		cc := *cur
		s.lastCursor = &cc
	} else {
		s.lastCursor = nil
	}
	return nil
}

// Reset clears the frame caches so the next Render is a full repaint even
// when the content is unchanged since the last frame (used after a terminal
// suspend/resume, where the screen was handed to another process).
func (s *Screen) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastContent = ""
	s.lastCursor = nil
}

// Resize records a new terminal size. Rendering does not depend on the
// size (the terminal soft-wraps), but the program tracks it for layout.
func (s *Screen) Resize(width, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.width, s.height = width, height
	// Content may be stale at the new size — force a full redraw.
	s.lastContent = ""
}

// Size returns the terminal size in cells.
func (s *Screen) Size() (int, int) {
	if s.sizeFile == nil {
		return s.width, s.height
	}
	w, h, err := term.GetSize(int(s.sizeFile.Fd()))
	if err != nil {
		return s.width, s.height
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.width, s.height = w, h
	return w, h
}

// cursorsEqual reports whether two cursors render identically.
func cursorsEqual(a, b *Cursor) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.X == b.X && a.Y == b.Y && a.Shape == b.Shape && a.Blink == b.Blink
}

// colorHex converts an image/color.Color to "#rrggbb".
func colorHex(c color.Color) (string, bool) {
	if c == nil {
		return "", false
	}
	r, g, b, _ := c.RGBA()
	// RGBA() returns 16-bit values; the upper 8 bits are the color.
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8), true
}

// encodeCursorStyle returns the DECSCUSR parameter for the given cursor
// shape and blink state (same mapping as bubbletea's renderer).
func encodeCursorStyle(shape CursorShape, blink bool) int {
	style := (int(shape) * 2) + 1 //nolint:mnd
	if !blink {
		style++
	}
	return style
}
