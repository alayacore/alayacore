package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	rw "github.com/mattn/go-runewidth"
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
	// CursorEnd runs ensureCursorVisible, so visStart > 0 and the cell is
	// measured relative to the visible window.
	f = NewInputField()
	f = f.WithWidth(5)
	f = f.WithValue("abcdefghij").CursorEnd()
	if f.visStart == 0 {
		t.Fatal("setup failed: expected horizontal scroll (visStart > 0)")
	}
	wantCell := 10 - runesWidth([]rune("abcdefghij")[:f.visStart])
	if cell := f.CursorCell(); cell != wantCell {
		t.Fatalf("scrolled end: got cell %d, want %d", cell, wantCell)
	}

	// Horizontal scroll with a wide char before the cursor.
	f = NewInputField()
	f = f.WithWidth(4)
	f = f.WithValue("abcdefgh").CursorEnd()
	f = f.WithCursorPos(2) // after 'a','b' — ensureCursorVisible adjusts
	f = f.ensureCursorVisible()
	wantCell = runesWidth([]rune("ab")) - runesWidth([]rune("abcdefgh")[:f.visStart])
	if cell := f.CursorCell(); cell != wantCell {
		t.Fatalf("scrolled mid: got cell %d, want %d", cell, wantCell)
	}
}

// TestInputFieldCursorCellAlignedWithVisibleText is a regression test for a
// bug where the real terminal cursor drifted onto the last character when a
// mixed CJK/ASCII line overflowed the input width.
//
// The scroll offset (in cells) could land in the middle of a wide CJK
// character: buildVisibleText rounded the start down to the character
// boundary (rendering one extra cell), while CursorCell subtracted the raw
// offset — placing the cursor one cell too early, on top of the last
// character. Typing then appeared to replace the last character (display
// error only).
func TestInputFieldCursorCellAlignedWithVisibleText(t *testing.T) {
	// Exact user flow: type CJK until the line overflows, type an ASCII char,
	// then type more CJK. The scroll offset becomes odd (mid-wide-char), which
	// used to desync CursorCell from the rendered text.
	f := NewInputField()
	f = f.WithWidth(20)

	for _, r := range "你好世界你好世界你好世" {
		f, _ = f.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	f, _ = f.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	f, _ = f.Update(tea.KeyPressMsg{Text: "你", Code: '你'})

	vis := f.buildVisibleText()
	if cell, want := f.CursorCell(), runesWidth(vis); cell != want {
		t.Fatalf("cursor at end of line: CursorCell=%d but visible text width=%d — cursor is drawn %d cell(s) too early (on the last char); value=%q visStart=%d visible=%q",
			cell, want, want-cell, f.Value(), f.visStart, string(vis))
	}

	// Sweep: for a range of widths and mixed ASCII/CJK sequences, the cursor
	// must always sit exactly after the last visible character when at the end
	// of the line, and never drift onto it. (WithWidth is a value-type
	// method — the result must be assigned back, or the width silently stays
	// at the default and the sweep never exercises narrow viewports.)
	chars := []rune("a你b好c世d界e你f好g世h界")
	for width := 1; width <= 12; width++ {
		for n := 1; n <= len(chars); n++ {
			g := NewInputField()
			g = g.WithWidth(width)
			for _, r := range chars[:n] {
				g, _ = g.Update(tea.KeyPressMsg{Text: string(r), Code: r})
			}
			vis := g.buildVisibleText()
			if cell, want := g.CursorCell(), runesWidth(vis); cell != want {
				t.Fatalf("width=%d chars=%q: CursorCell=%d but visible text width=%d — cursor drawn %d cell(s) too early (on the last char); visStart=%d visible=%q",
					width, string(chars[:n]), cell, want, want-cell, g.visStart, string(vis))
			}
		}
	}

	// Degenerate viewports (width 1 and 2) can push the scroll offset to the
	// end of the line; the cursor must still sit after the last visible
	// character instead of on it.
	for width := 1; width <= 2; width++ {
		g := NewInputField()
		g = g.WithWidth(width)
		for _, r := range "你好世界" {
			g, _ = g.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		}
		vis := g.buildVisibleText()
		if cell, want := g.CursorCell(), runesWidth(vis); cell != want {
			t.Fatalf("width=%d CJK: CursorCell=%d but visible text width=%d — cursor drawn %d cell(s) off; visStart=%d visible=%q",
				width, cell, want, want-cell, g.visStart, string(vis))
		}
	}
}

// TestInputFieldWideRuneAtRightEdgeVisible is a regression test for wide (CJK)
// runes being clipped by the right edge of the viewport. When the cursor sits
// right before a wide rune whose second cell falls outside the viewport, the
// viewport must scroll so the rune is fully visible.
func TestInputFieldWideRuneAtRightEdgeVisible(t *testing.T) {
	// "abcd你fg世": a0 b1 c2 d3 你4-5 f6 g7 世8-9, width=5.
	// CursorEnd -> offset=7; WithCursorPos(3) pulls the offset back to 3;
	// WithCursorPos(7) (before 世, cursorCell=8) fires a scroll whose raw
	// offset (8-5+2=5) falls mid-你. Rounding it DOWN used to yield offset=4
	// (rel=4), clipping 世 at the right edge; rounding UP to the next rune
	// boundary (6) keeps 世 fully visible.
	g := NewInputField()
	g = g.WithWidth(5)
	g = g.WithValue("abcd你fg世").CursorEnd()
	g = g.WithCursorPos(3)
	g = g.WithCursorPos(7) // before 世

	vis := g.buildVisibleText()
	cell := g.CursorCell()
	if cell+2 > runesWidth(vis) {
		t.Fatalf("wide rune at cursor clipped: CursorCell=%d, rune width=2, visible width=%d; visStart=%d visible=%q",
			cell, runesWidth(vis), g.visStart, string(vis))
	}
	// The rune starting at the cursor cell must be the wide rune itself.
	cells, idx := 0, -1
	for i, r := range vis {
		if cells == cell {
			idx = i
			break
		}
		cells += rw.RuneWidth(r)
	}
	if idx < 0 || vis[idx] != '世' {
		t.Fatalf("rune at cursor cell %d is not 世; visible=%q", cell, string(vis))
	}
}

// assertCursorRuneVisible checks the two cursor/rendering invariants against
// an InputField: the cursor never sits beyond the rendered visible text, and
// the rune at the cursor (when there is one and it could fit in the viewport)
// is never clipped by the right edge.
func assertCursorRuneVisible(t *testing.T, g InputField) {
	t.Helper()
	vis := g.buildVisibleText()
	cell := g.CursorCell()
	if cell > runesWidth(vis) {
		t.Fatalf("CursorCell=%d beyond rendered width=%d; visStart=%d visible=%q", cell, runesWidth(vis), g.visStart, string(vis))
	}
	val := []rune(g.Value())
	if g.pos >= len(val) {
		return // end of line
	}
	w := rw.RuneWidth(val[g.pos])
	if w <= g.width && cell+w > runesWidth(vis) {
		t.Fatalf("rune %q at cursor clipped: CursorCell=%d + w=%d > visible width=%d; visStart=%d visible=%q",
			string(val[g.pos]), cell, w, runesWidth(vis), g.visStart, string(vis))
	}
}

// TestInputFieldCursorNeverClipsWideRune sweeps every viewport width, prefix
// length, and cursor position over mixed ASCII/CJK content, asserting that the
// cursor never sits beyond the rendered text and a rune at the cursor is never
// clipped by the right edge. Cursor positions are reached both directly (via
// WithCursorPos) and through real movement sequences (CursorEnd -> move left
// to the start -> move right to the target), which exercises the path where a
// scroll offset is first pulled back and then advanced again.
func TestInputFieldCursorNeverClipsWideRune(t *testing.T) {
	chars := []rune("a你b好c世d界e你f好g世h界")
	for width := 1; width <= 12; width++ {
		for n := 1; n <= len(chars); n++ {
			val := string(chars[:n])

			// Direct cursor placement.
			for pos := 0; pos <= n; pos++ {
				g := NewInputField()
				g = g.WithWidth(width)
				g = g.WithValue(val).WithCursorPos(pos)
				assertCursorRuneVisible(t, g)
			}

			// Movement walk: CursorEnd -> left to start -> right to end,
			// asserting at every step.
			g := NewInputField()
			g = g.WithWidth(width)
			g = g.WithValue(val).CursorEnd()
			assertCursorRuneVisible(t, g)
			for p := n - 1; p >= 0; p-- {
				g, _ = g.handleKeyMsg(tea.KeyPressMsg{Text: "left", Code: 0})
				assertCursorRuneVisible(t, g)
			}
			for p := 1; p <= n; p++ {
				g, _ = g.handleKeyMsg(tea.KeyPressMsg{Text: "right", Code: 0})
				assertCursorRuneVisible(t, g)
			}
		}
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

// TestInputFieldWithValueResetsVisibleStart is a regression test: WithValue
// replaces the whole value, so the visible start must be recomputed from the
// new value. The old and new values can coincidentally share a lineStart
// (e.g. both single-line values start at 0), which the line-change detection
// alone cannot distinguish — the invalidation must be explicit.
func TestInputFieldWithValueResetsVisibleStart(t *testing.T) {
	// Scrolled single-line value...
	g := NewInputField()
	g = g.WithWidth(5)
	g = g.WithValue("abcdefghij").CursorEnd()
	if g.visStart == 0 {
		t.Fatal("setup failed: expected scrolled state (visStart > 0)")
	}
	stale := g.visStart

	// ...replaced by another single-line value (same lineStart 0).
	g = g.WithValue("ABCDEFGHIJK")
	vis := g.buildVisibleText()
	// The view must be recomputed from the new value: scrolling to the end
	// of "ABCDEFGHIJK" (11 cells, width 5) yields visStart = 8 ("IJK",
	// cursor at rel 3), independent of the old value's state.
	newLine := []rune("ABCDEFGHIJK")
	wantStart := firstRuneStartAtLeast(newLine, runesWidth(newLine)-5+2)
	if g.visStart != wantStart {
		t.Errorf("visStart=%d leaked from old value (was %d); want recomputed %d", g.visStart, stale, wantStart)
	}
	if string(vis) != "IJK" {
		t.Errorf("visible=%q, want %q (stale scroll position applied to new value)", string(vis), "IJK")
	}
	if cell := g.CursorCell(); cell != 3 {
		t.Errorf("CursorCell=%d, want 3", cell)
	}

	// Short replacement must also reset (no stale visStart).
	g = g.WithValue("界")
	if g.visStart != 0 {
		t.Errorf("visStart=%d, want 0 after short replacement", g.visStart)
	}
	if vis := g.buildVisibleText(); string(vis) != "界" {
		t.Errorf("visible=%q, want %q", string(vis), "界")
	}
}

// TestInputFieldUnifiedWidthSource verifies that every width calculation in
// the input chain (truncation, cursor, padding) comes from runesWidth, so a
// character whose width differs between width libraries (e.g. ❤️ with a
// variation selector: go-runewidth says 1, ansi says 2) can never make the
// view overflow or produce negative padding.
func TestInputFieldUnifiedWidthSource(t *testing.T) {
	g := NewInputField()
	g = g.WithWidth(5)
	g = g.WithValue("a❤️b").CursorEnd()

	vis := g.buildVisibleText()
	if w := runesWidth(vis); w > g.width {
		t.Fatalf("visible width=%d exceeds viewport width=%d; visible=%q", w, g.width, string(vis))
	}
	// Padding is computed from the same runesWidth: width - runesWidth(vis)
	// can never be negative (runesWidth(vis) <= width by construction).
	if w := runesWidth(vis); w < g.width {
		if got := g.View(); got == "" {
			t.Fatal("empty view")
		}
	}

	// Placeholder path (truncation + padding) must also stay consistent.
	g = g.WithValue("")
	g.Placeholder = "❤️你好"
	if got := g.View(); got == "" {
		t.Fatal("empty placeholder view")
	}
}
