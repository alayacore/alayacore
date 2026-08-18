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
	"strconv"
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
	switch {
	case clearFirst:
		buf = append(buf, ansi.EraseDisplay(2)...)
		buf = append(buf, ansi.CursorHomePosition...)
		// Hide the real cursor BEFORE painting: with the cursor visible,
		// an incrementally-painting terminal would show it traveling with
		// the frame's text (down the base rows, around the overlay rows)
		// before the final position/show — a visible flicker on every
		// changed frame. The cursor is re-shown at its final position
		// below.
		buf = append(buf, ansi.ResetModeTextCursorEnable...)
		if strings.ContainsRune(content, '\n') {
			buf = append(buf, strings.ReplaceAll(content, "\n", "\r\n")...)
		} else {
			buf = append(buf, content...)
		}
		// Overlay frames draw CUP rows over the base content; those rows are
		// padded to the box width but may be shorter than the screen width.
		// For non-overlay full-screen frames, clear from the end of the
		// content (the status bar is the last, short row) to the bottom of the
		// screen — the status row's tail and any row below the frame (when a
		// transition temporarily leaves fewer rows) get wiped, so no residue
		// from a previous frame survives. The cursor is positioned AFTER this
		// erase (absolute CUP below).
		if fullScreen && !hadOverlay {
			buf = append(buf, ansi.EraseDisplay(0)...)
		}
	case s.lastContent != "":
		// Steady frame: repaint only the rows that actually changed (row
		// diff). A full-frame rewrite on every small change (a Tab focus
		// toggle, a border color, a status segment) makes incrementally
		// painting terminals visibly repaint the whole screen — the base
		// repaints without the overlay rows, then the overlay pops back
		// in. With the cursor hidden first and only changed rows written,
		// small changes repaint a few rows in place.
		buf = append(buf, ansi.ResetModeTextCursorEnable...)
		buf = append(buf, diffFrameRows(s.lastContent, content)...)
		// Non-overlay frames: the last row (status bar) is short; when the
		// frame shrank or the last row changed, clear from its end so no
		// tail residue survives.
		if !hadOverlay {
			if lr, ok := lastBaseRow(parseFrameRows(content)); ok {
				if needTailErase(s.lastContent, content) {
					buf = append(buf, ansi.CursorPosition(ansi.StringWidth(lr.text)+1, lr.row+1)...)
					buf = append(buf, ansi.EraseDisplay(0)...)
				}
			}
		}
	default:
		// First frame without a clear: hide the cursor, paint from home.
		buf = append(buf, ansi.ResetModeTextCursorEnable...)
		buf = append(buf, ansi.CursorHomePosition...)
		if strings.ContainsRune(content, '\n') {
			buf = append(buf, strings.ReplaceAll(content, "\n", "\r\n")...)
		} else {
			buf = append(buf, content...)
		}
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
	}
	// cur == nil: the cursor stays hidden (already hidden at frame start).

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

// frameRow is one row of a rendered frame at an absolute screen position:
// the base rows are sequential from (0,0) (base=true); overlay rows carry
// explicit CUP coordinates (base=false). text is the row content verbatim
// (SGR and EL codes intact).
type frameRow struct {
	row, col int
	base     bool // true = sequential base row, false = CUP-positioned overlay row
	text     string
}

// parseFrameRows splits raw frame content into positioned rows. Base rows
// are the '\n'-separated lines drawn sequentially; CUP sequences
// (ESC [ row ; col H) start a new row at an absolute position (overlay).
// All other escape sequences (SGR, EL, …) are part of the row text.
//
//nolint:gocyclo // byte-level CSI scanner (position jumps + text flushing)
func parseFrameRows(content string) []frameRow {
	var rows []frameRow
	row, col := 0, 0
	base := true
	sawCUP := false
	start := 0
	flush := func(end int) {
		if end > start {
			rows = append(rows, frameRow{row: row, col: col, base: base, text: content[start:end]})
		}
	}
	i := 0
	for i < len(content) {
		switch content[i] {
		case '\n':
			flush(i)
			start = i + 1
			row++
			col = 0
			if sawCUP {
				base = false
			}
			i++
		case '\r':
			col = 0
			i++
		case 0x1b:
			if i+1 < len(content) && content[i+1] == '[' {
				j := i + 2
				for j < len(content) && (content[j] == ';' ||
					(content[j] >= '0' && content[j] <= '9')) {
					j++
				}
				if j < len(content) && content[j] == 'H' {
					if r, c, ok := parseCUP(content[i+1 : j+1]); ok {
						// CUP (or home): flush the current row, jump.
						flush(i)
						start = j + 1
						row, col = r, c
						sawCUP = true
						base = false
						i = j + 1
						continue
					}
				}
			}
			i++
		default:
			i++
		}
	}
	flush(i)
	return rows
}

// parseCUP parses a CSI absolute-cursor-positioning sequence of the form
// "[" inner "H" (e.g. "[5;10H") and returns 0-indexed (row, col).
// The bare "[H" form returns (0, 0, true) (home position). A single
// argument ("[5H") defaults col to 1 (xterm convention), so it returns
// (row-1, 0, true). Anything else returns (0, 0, false).
func parseCUP(seq string) (row, col int, ok bool) {
	if len(seq) < 2 || seq[0] != '[' || seq[len(seq)-1] != 'H' {
		return 0, 0, false
	}
	inner := seq[1 : len(seq)-1]
	if inner == "" {
		return 0, 0, true
	}
	parts := strings.SplitN(inner, ";", 2)
	if parts[0] != "" {
		r, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, false
		}
		row = r - 1
	}
	col = 0 // default column
	if len(parts) == 2 && parts[1] != "" {
		c, err2 := strconv.Atoi(parts[1])
		if err2 != nil {
			return 0, 0, false
		}
		col = c - 1
	}
	return row, col, true
}

// lastBaseRow returns the last sequential base row (the base content's
// final row — the status bar in full-screen frames).
func lastBaseRow(rows []frameRow) (frameRow, bool) {
	var last frameRow
	found := false
	for _, r := range rows {
		if r.base {
			last = r
			found = true
		}
	}
	return last, found
}

// diffFrameRows emits CUP + row for every row whose text changed or is
// new, and clears (EL) overlay rows that disappeared. Base rows that
// disappeared are handled by the caller's tail erase.
//
// Overlap rule: overlay boxes span the FULL terminal width by design (they
// start at column 1), so overlay rows share row coordinates with the base
// rows. A base row that an overlay row covers in the new frame is NEVER
// rewritten — the overlay is the top layer, and rewriting the base
// underneath would wipe the overlay.
func diffFrameRows(oldContent, newContent string) []byte {
	oldRows := parseFrameRows(oldContent)
	newRows := parseFrameRows(newContent)

	key := func(r frameRow) [3]int { return [3]int{r.row, r.col, boolInt(r.base)} }
	oldMap := make(map[[3]int]string, len(oldRows))
	for _, r := range oldRows {
		oldMap[key(r)] = r.text
	}
	newMap := make(map[[3]int]bool, len(newRows))
	coveredByOverlay := make(map[int]bool)
	for _, r := range newRows {
		newMap[key(r)] = true
		if !r.base {
			coveredByOverlay[r.row] = true
		}
	}

	var buf []byte
	for _, r := range newRows {
		if r.base && coveredByOverlay[r.row] {
			// Overlay covers this row in the new frame — do not rewrite
			// the base underneath.
			continue
		}
		if oldText, ok := oldMap[key(r)]; !ok || oldText != r.text {
			buf = append(buf, ansi.CursorPosition(r.col+1, r.row+1)...)
			buf = append(buf, r.text...)
		}
	}
	// Rows the old frame drew that the new frame no longer draws: clear
	// absolute (overlay) rows in place; base rows are handled by the tail
	// erase (they are sequential from the top).
	for _, r := range oldRows {
		if !r.base && !newMap[key(r)] {
			buf = append(buf, ansi.CursorPosition(r.col+1, r.row+1)...)
			buf = append(buf, ansi.EraseLine(0)...)
		}
	}
	return buf
}

// boolInt converts a bool to an int (0/1) for use in map keys.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// needTailErase reports whether a steady non-overlay frame needs the
// trailing ED0: the new frame is shorter than the old one (rows below
// would survive), or the last base row changed (its short tail — the
// status bar is not padded — must be wiped).
func needTailErase(oldContent, newContent string) bool {
	oldLast, oldOK := lastBaseRow(parseFrameRows(oldContent))
	newLast, newOK := lastBaseRow(parseFrameRows(newContent))
	if !oldOK || !newOK {
		return newOK
	}
	if newLast.row < oldLast.row {
		return true
	}
	if newLast.row == oldLast.row && newLast.text != oldLast.text {
		return true
	}
	return false
}

// containsCUP reports whether content draws rows with absolute cursor
// positioning (`ESC [ <digits> ; <digits> H`) — the overlay row idiom.
func containsCUP(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j < len(s) && s[j] == 'H' {
			if _, _, ok := parseCUP(s[i+1 : j+1]); ok {
				return true
			}
			i = j
		}
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
