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
//     tables give different cell counts for some single clusters — measured
//     on 4193 codepoints of the two, 237 of them printable; a keycap
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
//  3. Breaking cannot disagree with measuring either. The breakers live here
//     too (hardwrapCells, keepCells/dropCells/cutCells); nothing in the adapter
//     calls x/ansi's any more, and its word wrapper went with the Style block
//     width that was the only way to reach it. That is a fix, not tidiness:
//     x/ansi's breakers walk BYTES and form a grapheme cluster only when the
//     lead byte is non-ASCII, so a cluster that starts with an ASCII character
//     is billed one cell plus whatever its tail measures. A keycap ("1" +
//     U+FE0F + U+20E3) is 1 cell to ansi.Hardwrap and 2 to this table, to
//     ansi.StringWidth and to the terminal — so the wrapper handed back rows
//     WIDER than the budget it was given, and a row the layout charged one
//     terminal row to took two. Everything below it landed a row low, which is
//     what put transcript text on the live edge's row and kept it there until a
//     full repaint. width_test.go's budget and greediness tests are the check;
//     screen_repaint_invariant_test.go holds the frame-level consequence.
//
//  4. Nothing here materializes a list of clusters. Every question this file
//     and the input chain ask of a string — how wide, what is the widest
//     cluster, where does the prefix that fits end, where does the suffix that
//     fits begin, which cluster is this position in — is a fold, so it is
//     answered by walking (walkCells) and stopping when the answer is known.
//     The list form was here once, and it made each of those questions cost
//     O(len(s)) in time and ~120 B per cluster in garbage: a folded window's
//     one-row summary was 100μs and half a megabyte on a 2 KB message, and a
//     keystroke at the end of a long prompt line was ~1 ms and ~4 MB. Both are
//     measured in docs/internal/virtual-rendering-performance.md, and both are
//     pinned by allocation-count tests (TestCutsCostTheCutNotTheString,
//     TestLineQueriesCostOneEncoding) so the shape cannot come back quietly.
//     A cut returns a copy of what it kept rather than a substring of its
//     input: the folded summary is cached on the window, and a 30-cell prefix
//     must not hold the whole message alive behind it.
//
// With RUNEWIDTH_EASTASIAN set, x/ansi's copy of the options bills East-Asian-
// Ambiguous glyphs (│ ─ … — · • ↓ ∞) two cells. Nothing here reads that copy,
// so the variable cannot move a row; double-width-ambiguous stays an
// unsupported terminal configuration (constants.go, glyph policy; docs/tui.md)
// because it is a question about what the host draws, which no table answers.

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/displaywidth"
)

// widthModel is the one set of width options the adapter measures with.
// EastAsianWidth is pinned false (see 2 above). ControlSequences stays false:
// escape handling is done by ansi.Strip first, which recognizes the whole
// ECMA-48 grammar. displaywidth's own handling covers 7-bit introducers and
// needs a second option for the 8-bit C1 forms, and it disagrees with the
// stripper there (measured on "a" + C1 + "[m b": 6 cells against 4) — pasted
// content can carry C1, so one implementation of escape removal, in the
// place that also cuts, keeps measure and cut from parting company again.
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

// breakerModel is widthModel with ECMA-48 sequences recognized as clusters of
// their own. cellWidth strips escapes with ansi.Strip and measures the rest;
// the breakers below have to KEEP them (a cut row is still styled), so they
// walk the same string with each escape folded into one zero-width cluster
// instead. Both routes reach displaywidth's table with EastAsianWidth pinned
// false, which is the whole point: the number a breaker charges is the number
// cellWidth reports and the number the terminal draws.
//
// ControlSequences8Bit is off, and turning it on is a measured mistake rather
// than a matter of taste. cellWidth removes escapes with ansi.Strip, which
// recognizes a raw C1 byte (0x80-0x9F) as an introducer; hasEscape, the guard
// that decides whether to strip, recognizes only the UTF-8 form (C2 80..9F), so
// a raw C1 byte is never stripped and cellWidth bills it as the visible
// characters it then looks like. With the 8-bit option on, this walker instead
// folds the raw sequence into one zero-width cluster — and the two disagree,
// measured on "a" + 0x9B + "[m b": cellWidth 6, walker 4, so hardwrapCells
// returned a 5-cell row for a 3-cell budget. With it off the walker agrees with
// cellWidth on all three forms a C1 can arrive in (raw byte 6/6, UTF-8 encoded
// 5/5, and 7-bit CSI 2/2), which is the property this file exists to hold.
// TestWalkCellsAgreesWithCellWidth covers all three.
var breakerModel = &displaywidth.Options{
	EastAsianWidth:   false,
	ControlSequences: true,
}

// walkCells calls fn once per grapheme cluster of s, in order: the cluster's
// text and the cells it draws. An escape sequence arrives as one cluster of
// zero cells, so a caller copies it verbatim and the budget is untouched.
//
// fn returns false to stop the walk. That is what lets a caller whose answer
// is a prefix — takeCells, and the input chain's "which cluster is the caret
// in" — cost its answer instead of the string: the walk ends at the budget
// rather than at the end of s. A caller that wants the whole string returns
// true from every call.
//
// Two speeds, one meaning. Plain ASCII with no escape in it is walked byte by
// byte — each byte its own cluster, one cell, a control zero — which is the
// answer the table gives without paying for the walk. That is the common case
// (transcript prose), and it is what keeps a full re-wrap of a long document at
// the speed it had before the breakers moved onto this file's table —
// BenchmarkFullWrap and BenchmarkWrapContent are the two that would show it.
// Everything else goes through the table.
//
// This is the only cluster primitive the adapter has. Nothing materializes a
// []cluster: every caller wants a sum, a max, a prefix, a suffix or one
// position, and all five are folds. Building the list first made each of them
// O(len(s)) in time and ~120 B per cluster in garbage, which is how a folded
// window's one-row summary came to cost 100μs and half a megabyte on a 2 KB
// message (docs/internal/virtual-rendering-performance.md).
func walkCells(s string, fn func(text string, cells int) bool) {
	if s == "" {
		return
	}
	if plainASCIIFast(s) {
		for i := 0; i < len(s); i++ {
			cells := 1
			if s[i] <= 0x1f {
				cells = 0
			}
			if !fn(s[i:i+1], cells) {
				return
			}
		}
		return
	}
	it := breakerModel.StringGraphemes(s)
	for it.Next() {
		if !fn(it.Value(), it.Width()) {
			return
		}
	}
}

// linesFit reports whether every '\n'-separated line of s already measures at
// most width cells. It is hardwrapCells' early-out, and the reason a call with
// nothing to break costs one measuring pass instead of a rebuilt string — the
// common case, since wrapVisualLines hands it one original line at a time and
// most of them fit.
func linesFit(s string, width int) bool {
	for line := range strings.SplitSeq(s, "\n") {
		if cellWidth(line) > width {
			return false
		}
	}
	return true
}

// plainASCIIFast reports whether s can be walked byte by byte: no byte above
// 0x7E, and no escape introducer. An escape is ASCII too, so the second
// condition is what keeps a styled row out of that path — its sequence bytes
// would be billed as cells.
func plainASCIIFast(s string) bool {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b > 0x7e || b == 0x1b || b == 0x7f {
			return false
		}
	}
	return true
}

// hardwrapCells breaks s at cluster boundaries so that every line measures at
// most width cells — the cells cellWidth reports, which are the cells the
// terminal draws. Escapes are carried through unbroken and charge nothing; a
// newline in s ends the line without being charged.
//
// One row can still exceed the budget: a single cluster wider than width
// cannot be broken, so it gets a line to itself (a CJK glyph in a 1-cell
// column). It is emitted rather than dropped, and it is the only exception —
// widestCellCluster is how a caller asks whether one is in play.
func hardwrapCells(s string, width int) string {
	if width < 1 || s == "" {
		return s
	}
	if linesFit(s, width) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/width + 8)
	cur := 0
	walkCells(s, func(text string, cells int) bool {
		if cells == 0 {
			// An escape (kept, uncharged) or a control. Only a newline ends
			// the line.
			if text == "\n" {
				b.WriteByte('\n')
				cur = 0
				return true
			}
			b.WriteString(text)
			return true
		}
		// Break only when the line already carries something. An unbreakable
		// cluster — a CJK glyph in a 1-cell column — then gets a line to
		// itself instead of an empty line ahead of it, which is what keeps
		// the row count equal to the cluster count.
		if cur > 0 && cur+cells > width {
			b.WriteByte('\n')
			cur = 0
		}
		b.WriteString(text)
		cur += cells
		return true
	})
	return b.String()
}

// keepCells returns the leading clusters of s whose total is at most n cells,
// never splitting a cluster. Escape sequences are kept wherever they appear,
// including past the cut: dropping an SGR reset there would let the cut row's
// color bleed into whatever the terminal draws next. n <= 0 yields the escapes
// alone.
func keepCells(s string, n int) string {
	if s == "" {
		return ""
	}
	if cellWidth(s) <= n {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	used := 0
	done := false
	walkCells(s, func(text string, cells int) bool {
		if cells == 0 && escapeCluster(text) {
			b.WriteString(text)
			return true
		}
		// A character that draws nothing (a tab) belongs to the prefix only
		// while the budget is not yet spent, which is the rule dropCells
		// mirrors — so the two halves meet at one boundary and add back up.
		if done || (cells == 0 && used >= n) || used+cells > n {
			done = true
			return true // keep walking: the escapes behind the cut are kept
		}
		b.WriteString(text)
		used += cells
		return true
	})
	return b.String()
}

// dropCells returns s without the leading clusters that fill n cells — the
// complement of keepCells, so keepCells(s, n) and dropCells(s, n) partition s
// at one cluster boundary. Escapes on both sides of the cut are kept: the ones
// before it carry the pen state the surviving text was styled with.
func dropCells(s string, n int) string {
	if s == "" || n <= 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	dropped := 0
	walkCells(s, func(text string, cells int) bool {
		if cells == 0 && escapeCluster(text) {
			b.WriteString(text)
			return true
		}
		// Once the budget is spent — exactly, or by a cluster that
		// straddles it — everything that follows is kept. Marking the budget
		// spent rather than adding a flag keeps the rule one expression, and
		// the straddling cluster is kept whole: a cut never splits one.
		if dropped >= n || dropped+cells > n {
			dropped = n
			b.WriteString(text)
			return true
		}
		dropped += cells
		return true
	})
	return b.String()
}

// escapeCluster reports whether a zero-cell cluster is an ECMA-48 sequence
// rather than an ordinary character that draws nothing. Only a sequence travels
// with both halves of a cut — it is the pen state, and losing the reset past a
// cut bleeds color into the next row. A tab or a control character belongs to
// whichever side of the boundary it sits on, so the two halves still add up to
// the string they came from.
//
// Not hasEscape, which also answers true for DEL. DEL is a control character,
// not an introducer: keeping it in both halves would duplicate it.
func escapeCluster(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == 0x1b {
		return true
	}
	// A C1 control arrives as its two-byte UTF-8 form, C2 80..9F.
	return text[0] == 0xc2 && len(text) > 1 && text[1] >= 0x80 && text[1] <= 0x9f
}

// cutCells returns the run of s that occupies cells [left, right), measured
// with this file's table. It is the replacement for ansi.Cut: same shape
// (left inclusive, right exclusive, escapes preserved), same numbers as
// cellWidth. right <= left yields "".
func cutCells(s string, left, right int) string {
	if s == "" || right <= left {
		return ""
	}
	if left <= 0 {
		return keepCells(s, right)
	}
	return dropCells(keepCells(s, right), left)
}

// runeBoundary moves a byte offset in s back to the start of the rune it may
// have landed inside, so that s[:runeBoundary(s, i)] is whole characters and
// s[runeBoundary(s, i):] is too. i is clamped to [0, len(s)]; len(s) is
// already a boundary.
//
// This exists because the renderers slice a line at offsets computed from the
// pieces the line was BUILT from, and a truncation between building and
// slicing moves every piece after the cut. An offset that lands inside a
// multi-byte rune puts half a UTF-8 sequence on the wire, which a terminal
// draws as nothing or as a replacement glyph — a hole in the middle of a row —
// and which the width table then bills as one cell per stray byte, so the row
// also stops fitting the budget the layout reserved for it.
func runeBoundary(s string, i int) int {
	i = min(max(i, 0), len(s))
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// takeCells returns the leading clusters of s whose total width is at most
// cells, dropping from the end rather than splitting a cluster or
// overrunning the budget. cells <= 0 yields "".
//
// The walk stops at the budget, so this costs the cut and not the string: a
// 30-cell head of a 128 KB message is 30 cells of work and one small
// allocation. The result is a copy of that prefix rather than s[:n] for the
// same reason — the folded summary that asks for it is cached on the window,
// and a 30-cell substring would keep the whole message alive behind it.
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
	// Fast path: the whole string already fits. One measurement, no walk.
	if w := cellWidth(s); w <= cells {
		return s
	}
	if hasEscape(s) {
		return keepCells(s, cells)
	}
	used, end := 0, 0
	walkCells(s, func(text string, w int) bool {
		if used+w > cells {
			return false
		}
		used += w
		end += len(text)
		return true
	})
	return strings.Clone(s[:end])
}

// tailCells returns the trailing clusters of s whose total width is at most
// cells, dropping whole clusters from the front so the result stays
// right-anchored. cells <= 0 yields "". See takeCells on plain text, and on
// why the answer is a copy.
//
// A suffix cannot be found from its own end — segmentation runs forward — so
// this is one pass over s that stops at the first cluster boundary whose
// remainder fits the budget. The pass allocates nothing; only the tail it
// returns is allocated.
//
// The budget is a ceiling on both routes, which is what makes the pair
// trustworthy: a cluster that straddles it is dropped, not kept, exactly as
// takeCells drops it. (Routing the styled case through dropCells got this
// wrong, because dropCells' own contract is the opposite — it and keepCells
// partition a string, so it keeps the straddler. A styled row of 2-cell
// clusters asked for a 3-cell tail and got 4, which is the row-one-too-low
// class of bug this file exists to make unrepresentable.) Escapes still travel
// with the tail on both sides of the cut: dropping the SGR reset that preceded
// it would repaint everything after the row.
func tailCells(s string, cells int) string {
	if cells <= 0 || s == "" {
		return ""
	}
	w := cellWidth(s)
	if w <= cells {
		return s
	}
	if hasEscape(s) {
		remaining := w
		var b strings.Builder
		b.Grow(len(s))
		walkCells(s, func(text string, cw int) bool {
			switch {
			case cw == 0 && escapeCluster(text):
				b.WriteString(text) // pen state, kept on both sides of the cut
			case remaining > cells:
				remaining -= cw // still too much above the cut: drop this cluster
			default:
				b.WriteString(text)
			}
			return true
		})
		return b.String()
	}
	remaining, start := w, 0
	walkCells(s, func(text string, cw int) bool {
		if remaining <= cells {
			return false
		}
		remaining -= cw
		start += len(text)
		return true
	})
	return strings.Clone(s[start:])
}

// widestCellCluster returns the width of the widest single grapheme cluster
// in plain s — the narrowest cell budget s can be broken into without
// dropping content. A cluster wider than the space available cannot be shown at all.
func widestCellCluster(s string) int {
	best := 0
	walkCells(s, func(_ string, cells int) bool {
		if cells > best {
			best = cells
		}
		return true
	})
	return best
}
