package terminal

// ============================================================================
// Cell arithmetic — one table, one file
// ============================================================================
//
// Every number that answers "how many terminal cells?" or "cut this string
// at N cells" comes out of this file, from one table: displaywidth's, with
// its options constructed here. Two things follow from that, and both are
// pinned by width_test.go.
//
//  1. Measuring and cutting cannot disagree. They used to: rows were sized
//     with ansi.StringWidth and cut with uniseg's cluster widths, and the two
//     tables give different cell counts for some single clusters — a keycap
//     ("1" + U+FE0F + U+20E3) is 1 cell to uniseg and 2 to displaywidth. A
//     4-cell budget was then filled with what measured 5 cells, and the row
//     overflowed by one: the collapsed summary that pushed the next row
//     right, the wrapped line that ate a frame column. One table for both
//     halves makes that class of bug unrepresentable.
//
//  2. The environment cannot retune the layout. charmbracelet/x/ansi reads
//     RUNEWIDTH_EASTASIAN in its own package init and, when it says true,
//     charges East-Asian-Ambiguous glyphs (│ ─ … — · • ↓ ∞) two cells
//     instead of one. That switch is unexported and already applied by the
//     time any code of ours runs (an init in this package, or an
//     os.Unsetenv in main, is too late — measured), so the only way to own
//     the number is to hold our own Options. We pin EastAsianWidth=false:
//     the app draws Ambiguous glyphs one cell wide, which is what every
//     mainstream terminal does by default. The residual exposure — a user
//     who configures the terminal itself to draw them two cells wide — is
//     the limitation recorded in constants.go's glyph policy, waiver 2, and
//     no width table can help with it.
//
// What is NOT routed here: ansi.Hardwrap, ansi.Wrap, ansi.Truncate and
// ansi.Cut. They are escape-aware line breakers that also use displaywidth,
// but through ansi's env-tuned copy of the options, so with
// RUNEWIDTH_EASTASIAN set they break lines earlier than this file measures.
// That direction is safe — an early break leaves a row under-filled, it can
// never overflow a budget this file computed — so they stay. Routing them
// here would mean reimplementing ECMA-48-aware word wrapping for no
// additional safety.

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/displaywidth"
)

// widthModel is the one set of width options the adapter measures with.
// EastAsianWidth is pinned false (see 2 above); ControlSequences stays false
// because escape handling is done by ansi.Strip, which understands the full
// ECMA-48 grammar including 8-bit C1 (displaywidth's own escape handling
// covers only 7-bit introducers, and differs on those: measured).
var widthModel = &displaywidth.Options{EastAsianWidth: false}

// cellWidth returns the number of terminal cells s occupies. ANSI/ECMA-48
// escape sequences are not drawn and count for nothing, tabs and newlines
// count for nothing (callers expand tabs before measuring — see
// expandTabs), and a grapheme cluster is measured whole.
func cellWidth(s string) int {
	if s == "" {
		return 0
	}
	// Unstyled text is the common case on the hot paths (window labels,
	// table cells, status segments), and stripping is a second full pass
	// over the string.
	if !hasEscape(s) {
		return widthModel.String(s)
	}
	return widthModel.String(ansi.Strip(s))
}

// hasEscape reports whether s may contain an escape sequence: a 7-bit
// introducer (ESC, DEL) or a C1 control encoded as UTF-8 (U+0080-U+009F).
// Cheap because it only looks for the introducers, never parses them.
func hasEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		switch b := s[i]; b {
		case 0x1b, 0x7f:
			return true
		default:
			// C1 controls in UTF-8 are the two-byte sequences C2 80..9F.
			if b == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f {
				return true
			}
		}
	}
	return false
}

// cluster is one grapheme cluster: its text, the cells it occupies, and its
// rune range within the string it came from.
type cluster struct {
	text               string
	cells              int
	runeStart, runeEnd int // rune indices, runeEnd exclusive
}

// clusters returns s's grapheme clusters in order, over plain text: escape
// sequences are not text and are not treated as units here (takeCells and
// tailCells route styled strings elsewhere). Cutting a string to a cell
// budget must never split a cluster — a base character and its
// combining mark, or an emoji and its variation selector, are drawn as one
// unit and half of one is garbage on screen — and the input chain moves the
// caret by whole clusters, which is why the rune range comes along too.
func clusters(s string) []cluster {
	if s == "" {
		return nil
	}
	it := widthModel.StringGraphemes(s)
	var out []cluster
	runes := 0
	for it.Next() {
		v := it.Value()
		n := utf8.RuneCountInString(v)
		out = append(out, cluster{text: v, cells: it.Width(), runeStart: runes, runeEnd: runes + n})
		runes += n
	}
	return out
}

// takeCells returns the leading clusters of s whose total width is at most
// cells, dropping from the end rather than splitting a cluster or
// overrunning the budget. cells <= 0 yields "".
//
// s is expected to be plain text — the adapter's window and table content,
// which is styled later by the render layer. A styled string is still cut
// correctly (grapheme clustering cannot tell an escape from text, so such a
// string is handed to the escape-aware cutter instead), but the clustering
// guarantee does not cover it.
func takeCells(s string, cells int) string {
	if cells <= 0 || s == "" {
		return ""
	}
	// Fast path: the whole string already fits. One measurement, no
	// clustering pass.
	w := cellWidth(s)
	if w <= cells {
		return s
	}
	if hasEscape(s) {
		return ansi.Cut(s, 0, cells)
	}
	var b strings.Builder
	used := 0
	for _, c := range clusters(s) {
		if used+c.cells > cells {
			break
		}
		b.WriteString(c.text)
		used += c.cells
	}
	return b.String()
}

// tailCells returns the trailing clusters of s whose total width is at most
// cells, dropping whole clusters from the front so the result stays
// right-anchored. cells <= 0 yields "". See takeCells on plain text.
func tailCells(s string, cells int) string {
	if cells <= 0 || s == "" {
		return ""
	}
	w := cellWidth(s)
	if w <= cells {
		return s
	}
	if hasEscape(s) {
		// TruncateLeft drops cells from the front; to keep the last
		// `cells`, drop everything before them.
		return ansi.TruncateLeft(s, w-cells, "")
	}
	cs := clusters(s)
	used := w
	start := 0
	for start < len(cs) && used > cells {
		used -= cs[start].cells
		start++
	}
	var b strings.Builder
	for _, c := range cs[start:] {
		b.WriteString(c.text)
	}
	return b.String()
}

// widestCellCluster returns the width of the widest single grapheme cluster
// in plain s — the narrowest cell budget s can be broken into without
// dropping content. A cluster wider than the space available cannot be shown at all.
func widestCellCluster(s string) int {
	best := 0
	for _, c := range clusters(s) {
		if c.cells > best {
			best = c.cells
		}
	}
	return best
}

// clusterCount returns the number of grapheme clusters in plain s. A row wider
// than its budget is only forgivable when it is one cluster (a single CJK
// glyph in a one-column window); more than one means something was
// over-filled.
func clusterCount(s string) int {
	return len(clusters(s))
}
