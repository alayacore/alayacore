package terminal

// A terminal grid model for the render tests: it turns the bytes Screen
// writes into the screen a terminal would show, so a test can ask the only
// question a user can ask — what is actually on the glass?
//
// residue_simulation_test.go's applyFrame answers a narrower question (does
// old content survive?) and assumes one column per rune. This model is the one
// the frame-geometry invariants need: it tracks the deferred-wrap state a real
// terminal is in after filling a row, it bills a grapheme cluster the cells the
// terminal bills it, and it reports whether the screen scrolled.
//
// Its width table is displaywidth's with escapes folded into zero-width
// clusters — the same numbers cellWidth produces. That is not circular, but it
// is not self-proving either, so it is checked against a real terminal: tmux
// reports the cursor column on request (ESC [ 6 n), and a sweep of the glyphs
// this UI draws — a keycap ("1" + U+FE0F + U+20E3), a ZWJ family, a flag pair,
// CJK, box drawing, braille, the Ambiguous marks — came back at the column this
// table bills. TestScreenMatchesATerminalUnderTmux is the checked-in form of
// the comparison: it replays a recorded frame into tmux and diffs the pane
// against this model, so the two can only agree if the numbers here are the
// ones a terminal draws. The model's job is to be the terminal; whether the
// adapter agrees with the terminal is what the tests using it ask.
//
// The wrap and erase rules were likewise measured against tmux rather than
// guessed, because the obvious reading of the standard is not what a terminal
// does:
//
//   - Writing into the last column leaves the cursor there with a wrap
//     pending; the next printable cluster wraps first.
//   - In that pending state the cursor is logically one past the last column,
//     so EL (ESC [ K) erases nothing. Feeding a full-width row followed by
//     ESC [ K to tmux leaves the row intact — which matters here, because the
//     diff renderer ends every repainted row with an EL.
//   - A CUP clears the pending state, so an EL after it does erase.
//   - Writing a cluster that does not fit the columns left blanks them and
//     wraps; it is not split.

import (
	"strings"
	"testing"

	"github.com/clipperhouse/displaywidth"
)

// gridCell is one screen cell. It carries the whole grapheme cluster, the way
// a terminal stores a keycap or a flag in its first cell; cells is 0 on the
// second half of a wide cluster, so a row's text can skip those without
// counting them twice.
type gridCell struct {
	text  string
	cells int
}

// grid is a terminal's screen: a fixed cell matrix plus the cursor state a
// real terminal carries with it.
type grid struct {
	width, height int
	rows          [][]gridCell
	cx, cy        int

	// pendingWrap is the deferred-wrap flag: the cursor sits in the last
	// column and the next printable cluster moves to the next row first.
	pendingWrap bool

	// scrolls counts the rows the screen lost off the top. A frame that fits
	// the screen never scrolls, so this is how a test learns that the frame
	// was taller than the terminal.
	scrolls int
}

// newGrid returns a blank screen of the given size.
func newGrid(width, height int) *grid {
	g := &grid{width: width, height: height, rows: make([][]gridCell, height)}
	for i := range g.rows {
		g.rows[i] = make([]gridCell, width)
		for j := range g.rows[i] {
			g.rows[i][j] = gridCell{" ", 1}
		}
	}
	return g
}

// gridModel measures the clusters the grid writes. Escapes are clusters of
// their own here (zero cells), which is what lets the byte scanner below hand
// a whole cluster — a keycap, a ZWJ sequence — to the grid in one step.
//
// This is a second copy of width.go's breaker options, and the duplication is
// the point: the grid is the oracle the renderer is judged against, so it must
// not move when the code under test moves. A shared value would let a change
// to the adapter's table quietly redefine what "correct" means. The copy is
// checked against a real terminal instead — TestScreenMatchesATerminalUnderTmux
// replays a recorded frame stream into tmux and diffs the pane against this
// model, so the two can only agree if the numbers here are the ones a terminal
// actually draws.
//
// ControlSequences8Bit is off because that is what the terminal does, measured:
// tmux 3.7c draws a lone byte in 0x80..9F as one U+FFFD and the bytes after it as
// text, so it never enters a control sequence on one (sanitize_test.go has the
// cursor columns and the pane bytes). With the option on this model folded such a
// byte and what followed it into one zero-width cluster and disagreed with the
// pane by two cells. It was on for as long as the replayed stream contained no
// ill-formed bytes, which is the whole of it until now — a setting nothing
// exercised is a guess, and the stream carries such a row today.
var gridModel = &displaywidth.Options{
	EastAsianWidth:   false,
	ControlSequences: true,
}

// firstCluster returns the first grapheme cluster of s and the cells it
// occupies.
func firstCluster(s string) (string, int) {
	it := gridModel.StringGraphemes(s)
	if !it.Next() {
		return "", 0
	}
	return it.Value(), it.Width()
}

// TestGridModelBillsIllFormedBytesAsTheTerminalDrew pins the oracle to the
// terminal on exactly the inputs a width library is likeliest to get wrong, and is
// the only thing that makes ControlSequences8Bit's value a checked fact rather than
// a guess. The adapter does not emit ill-formed bytes — sanitizeUTF8 repairs
// content on its way into a Window — so no replayed frame reaches the raw half of
// this test, and without it the setting would be unexercised again. The
// expectations are the cursor columns tmux 3.7c reported for the same bytes
// (terminalCases, in sanitize_test.go, taken with the harness described there).
//
// With ControlSequences8Bit on, "a" + 0x9B + "[m b" bills 4 cells where the
// terminal drew 6: the model folded that byte and the two after it into one
// zero-width cluster, taking 0x9B for the C1 introducer a terminal in UTF-8 mode
// does not read it as.
func TestGridModelBillsIllFormedBytesAsTheTerminalDrew(t *testing.T) {
	// The one measured input whose RAW bytes the oracle bills differently from the
	// terminal, and why it is allowed to be the only one. displaywidth takes 0xF5
	// for the lead of a 4-byte encoding and consumes what follows it, so "a" + 0xF5
	// + "b" bills 1 cell where the terminal drew 3; no option changes that, it is
	// the library's handling of a byte that cannot appear in UTF-8 at all, and
	// sanitizeUTF8 is what keeps such a byte out of every string the adapter bills
	// or emits. TestSanitizeUTF8IsWhatTheComponentsDisagreeAbout records the same
	// defect in cellWidth, so it is a known library behavior written down in two
	// places rather than a difference of opinion discovered twice.
	rawExceptions := map[string]string{
		"a byte past U+10FFFF": "displaywidth consumes what follows a 0xF5 lead",
	}
	// The exceptions are allowed to exist and not to grow. A lone C1 is the input
	// that distinguishes ControlSequences8Bit on from off, so if it ever lands in
	// the table nothing pins the setting any more.
	if why, ok := rawExceptions["a lone C1 control"]; ok {
		t.Fatalf("a lone C1 is excepted (%s), so ControlSequences8Bit is a guess again", why)
	}

	billed := 0
	for _, tc := range terminalCases {
		// What production does: repair, then bill. Every measured input has to come
		// out at the width the terminal drew, because the padding and the wrap are
		// arithmetic on that width.
		repaired := newGrid(40, 6)
		repaired.write([]byte(sanitizeUTF8(tc.in)))
		if repaired.cx != tc.cells {
			t.Errorf("%s: the grid bills the repaired form of %q at %d cells, the terminal drew %d",
				tc.name, tc.in, repaired.cx, tc.cells)
			continue
		}

		if why, ok := rawExceptions[tc.name]; ok {
			t.Logf("%s: excepted from the raw comparison — %s", tc.name, why)
			continue
		}
		raw := newGrid(40, 6)
		raw.write([]byte(tc.in))
		if raw.cx != tc.cells {
			t.Errorf("%s: the grid model bills %d cells for the raw %q, the terminal drew %d",
				tc.name, raw.cx, tc.in, tc.cells)
			continue
		}
		billed++
	}
	if billed != len(terminalCases)-len(rawExceptions) {
		t.Errorf("%d of %d inputs were compared raw, want %d", billed, len(terminalCases), len(terminalCases)-len(rawExceptions))
	}
}

// write runs a byte stream through the grid.
//
// one dispatch over the byte classes a frame can contain
func (g *grid) write(data []byte) {
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x1b:
			i = g.escape(data, i+1)
		case b == '\r':
			g.cx, g.pendingWrap = 0, false
			i++
		case b == '\n':
			g.pendingWrap = false
			g.down()
			i++
		case b == '\b':
			if g.cx > 0 {
				g.cx--
			}
			g.pendingWrap = false
			i++
		case b < 0x20:
			i++ // a control this renderer never emits
		default:
			text, cells := firstCluster(string(data[i:]))
			if text == "" {
				i++
				continue
			}
			g.put(text, cells)
			i += len(text)
		}
	}
}

// escape consumes one escape sequence starting at i (the byte after ESC) and
// returns the index past it.
func (g *grid) escape(data []byte, i int) int {
	if i >= len(data) {
		return len(data)
	}
	if data[i] != '[' {
		// OSC (ESC ]) runs to BEL or ST; every other two-byte form is
		// consumed whole. Neither moves the cursor here.
		if data[i] != ']' {
			return i + 1
		}
		for i < len(data) && data[i] != 0x07 {
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		if i < len(data) {
			i++
		}
		return i
	}
	i++
	start := i
	// Parameter bytes (0x30-0x3F), intermediates (0x20-0x2F) and the
	// private-marker '?' all belong to the sequence, not to the screen: a
	// scanner that stops at the space in "ESC [ 1 SP q" leaves the 'q' to be
	// printed, which is what a cursor-style sequence looks like as text.
	for i < len(data) && (data[i] == ';' || data[i] == ':' || data[i] == '?' ||
		(data[i] >= '0' && data[i] <= '9') ||
		(data[i] >= 0x20 && data[i] <= 0x2f)) {
		i++
	}
	if i >= len(data) {
		return len(data)
	}
	final := data[i]
	g.csi(string(data[start:i]), final)
	return i + 1
}

// csi applies one CSI sequence. Only the commands this renderer emits do
// anything; the rest are consumed and ignored, as a terminal ignores a
// sequence it does not implement.
func (g *grid) csi(params string, final byte) {
	p := csiNumbers(params)
	at := func(i, def int) int {
		if i < len(p) && p[i] > 0 {
			return p[i]
		}
		return def
	}
	switch final {
	case 'H', 'f':
		g.cy, g.cx = at(0, 1)-1, at(1, 1)-1
		g.clamp()
		g.pendingWrap = false
	case 'A':
		g.cy -= at(0, 1)
		g.clamp()
		g.pendingWrap = false
	case 'B':
		g.cy += at(0, 1)
		g.clamp()
		g.pendingWrap = false
	case 'C':
		g.cx += at(0, 1)
		g.clamp()
		g.pendingWrap = false
	case 'D':
		g.cx -= at(0, 1)
		g.clamp()
		g.pendingWrap = false
	case 'J':
		g.eraseDisplay(at(0, 0))
	case 'K':
		g.eraseLine(at(0, 0))
	}
}

// eraseDisplay applies ED. Mode 2 clears the visible screen; in the alternate
// buffer that is all there is, so mode 3 is the same act here.
func (g *grid) eraseDisplay(mode int) {
	switch mode {
	case 0:
		g.eraseRowFrom(g.cy, g.cx)
		for r := g.cy + 1; r < g.height; r++ {
			g.blankRow(r)
		}
	case 1:
		for r := 0; r < g.cy; r++ {
			g.blankRow(r)
		}
		for j := 0; j <= g.cx && j < g.width; j++ {
			g.rows[g.cy][j] = gridCell{" ", 1}
		}
	case 2, 3:
		for r := 0; r < g.height; r++ {
			g.blankRow(r)
		}
	}
	g.pendingWrap = false
}

// eraseLine applies EL. A cursor one past the last column (the deferred-wrap
// state) has nothing to its right, so the call is a no-op — the rule measured
// against tmux, and the reason a repainted full-width row keeps its last cell.
func (g *grid) eraseLine(mode int) {
	if g.cx >= g.width {
		return
	}
	switch mode {
	case 0:
		g.eraseRowFrom(g.cy, g.cx)
	case 1:
		for j := 0; j <= g.cx; j++ {
			g.rows[g.cy][j] = gridCell{" ", 1}
		}
	case 2:
		g.blankRow(g.cy)
	}
}

func (g *grid) eraseRowFrom(y, x int) {
	if y < 0 || y >= g.height {
		return
	}
	for j := max(0, x); j < g.width; j++ {
		g.rows[y][j] = gridCell{" ", 1}
	}
}

func (g *grid) blankRow(y int) {
	if y < 0 || y >= g.height {
		return
	}
	for j := range g.rows[y] {
		g.rows[y][j] = gridCell{" ", 1}
	}
}

// put writes one grapheme cluster occupying cells cells.
func (g *grid) put(text string, cells int) {
	if cells <= 0 {
		return
	}
	if g.pendingWrap {
		g.cx, g.pendingWrap = 0, false
		g.down()
	}
	if g.cx+cells > g.width {
		// A wide cluster with no room for its second cell: the cell it sits
		// on is blanked and the cluster goes to the next row.
		g.eraseRowFrom(g.cy, g.cx)
		g.cx = 0
		g.down()
	}
	g.rows[g.cy][g.cx] = gridCell{text, cells}
	for k := 1; k < cells; k++ {
		g.rows[g.cy][g.cx+k] = gridCell{"", 0}
	}
	g.cx += cells
	if g.cx >= g.width {
		g.cx, g.pendingWrap = g.width, true
	}
}

// down moves one row down, scrolling when the screen is full.
func (g *grid) down() {
	g.cy++
	if g.cy < g.height {
		return
	}
	g.cy = g.height - 1
	g.scrolls++
	copy(g.rows[0:], g.rows[1:])
	g.rows[g.height-1] = make([]gridCell, g.width)
	for j := range g.rows[g.height-1] {
		g.rows[g.height-1][j] = gridCell{" ", 1}
	}
}

func (g *grid) clamp() {
	g.cx = min(max(g.cx, 0), g.width-1)
	g.cy = min(max(g.cy, 0), g.height-1)
}

// text returns the screen as lines with trailing blanks removed — the shape
// `tmux capture-pane -p` prints, so a captured pane and a modeled screen can
// be compared line for line.
func (g *grid) text() []string {
	out := make([]string, len(g.rows))
	for i, row := range g.rows {
		var b strings.Builder
		for _, c := range row {
			if c.cells == 0 {
				continue
			}
			b.WriteString(c.text)
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// csiNumbers reads a CSI parameter string into ints, treating a missing or
// empty parameter as 0 (the caller supplies the default).
func csiNumbers(params string) []int {
	if params == "" {
		return nil
	}
	parts := strings.Split(params, ";")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		v := 0
		for _, c := range part {
			if c >= '0' && c <= '9' {
				v = v*10 + int(c-'0')
			}
		}
		out = append(out, v)
	}
	return out
}

// gridDiff describes where two screens disagree, one entry per row.
func gridDiff(a, b *grid) []string {
	var out []string
	ta, tb := a.text(), b.text()
	for i := 0; i < len(ta) || i < len(tb); i++ {
		var x, y string
		if i < len(ta) {
			x = ta[i]
		}
		if i < len(tb) {
			y = tb[i]
		}
		if x != y {
			out = append(out, "  row "+itoaPad(i)+":\n    painted:  "+quote(x)+"\n    repainted: "+quote(y))
		}
	}
	return out
}

func quote(s string) string { return `"` + s + `"` }

// itoaPad renders a row index in a fixed two-cell field so a diff reads as a
// column.
func itoaPad(n int) string {
	if n < 10 {
		return " " + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
