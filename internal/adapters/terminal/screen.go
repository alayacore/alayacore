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
	"strings"
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

	// Frame-state tracking for the overlay-free render path:
	// lastFullScreen records the previous frame's fill mode (a change must
	// re-render); lastHadOverlay records whether the previous frame drew
	// CUP-positioned overlay rows, so a frame that no longer draws them can
	// clear any residue (e.g. overlay rows overlapping the short status bar).
	lastFullScreen bool
	lastHadOverlay bool
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

// Render writes the frame to the terminal. It is a no-op when the content
// and cursor are unchanged since the last render.
//
// Two output paths:
//
//   - Full-screen frames (fullScreen=true): the content soft-wraps to
//     exactly the screen height and every row is padded to the full width,
//     so it can overwrite any previous frame without clearing first —
//     `\x1b[H` + content. This eliminates the clear-then-redraw flicker of
//     ED2 during streaming. If the previous frame drew overlay rows (CUP
//     sequences at arbitrary rows — e.g. an overlay row overlapping the
//     short status bar that the new frame cannot cover), the residue is
//     cleared with a one-frame ED2 fallback.
//
//   - Non-full-screen frames (loading, errors): keep `ED2` + home + content,
//     clearing whatever the previous frame left below.
//
// The content's '\n' characters are emitted as "\r\n": the program runs the
// terminal in raw mode (x/term MakeRaw clears OPOST/ONLCR), so a bare '\n'
// would only move the cursor down WITHOUT returning it to column 0 — every
// line after the first would start at the column where the previous line
// ended, spiraling the frame. The conversion is output-only; the view
// content itself keeps plain '\n' (copy fidelity is unaffected — terminal
// selection copies the rendered screen, not the emitted bytes).
//
//nolint:gocyclo // clear-mode decision + cursor encoding branches
func (s *Screen) Render(content string, cur *Cursor, fullScreen bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sameContent := content == s.lastContent
	sameCursor := cursorsEqual(s.lastCursor, cur)
	if sameContent && sameCursor && s.lastFullScreen == fullScreen {
		return nil
	}

	hadOverlay := containsCUP(content)
	// Clear before writing when: the frame does not fill the screen
	// (loading/errors), the previous frame drew overlay rows that this one
	// no longer draws (residue beyond full-width rows), or the fill mode
	// changed (e.g. loading -> normal) — a fresh clear guarantees no
	// residue from the previous mode even if the new frame's row count is
	// temporarily short (viewport sizing edge cases).
	clearFirst := !fullScreen || (s.lastHadOverlay && !hadOverlay) || s.lastFullScreen != fullScreen

	var buf []byte
	if clearFirst {
		buf = append(buf, ansi.EraseDisplay(2)...)
	}
	buf = append(buf, ansi.CursorHomePosition...)
	if strings.ContainsRune(content, '\n') {
		buf = append(buf, strings.ReplaceAll(content, "\n", "\r\n")...)
	} else {
		buf = append(buf, content...)
	}
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
	s.lastFullScreen = fullScreen
	s.lastHadOverlay = hadOverlay
	if cur != nil {
		cc := *cur
		s.lastCursor = &cc
	} else {
		s.lastCursor = nil
	}
	return nil
}

// containsCUP reports whether content draws rows with absolute cursor
// positioning (`ESC [ <digits> ; <digits> H`) — the overlay row idiom.
func containsCUP(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j >= len(s) || s[j] != ';' {
			continue
		}
		j++
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == 'H' {
			return true
		}
		i = j
	}
	return false
}

// Reset clears the frame caches so the next Render is a full repaint even
// when the content is unchanged since the last frame (used after a terminal
// suspend/resume, where the screen was handed to another process).
func (s *Screen) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastContent = ""
	s.lastCursor = nil
	s.lastFullScreen = false
	s.lastHadOverlay = false
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
