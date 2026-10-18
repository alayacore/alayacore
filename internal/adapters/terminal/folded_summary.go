package terminal

// Fitting a window's content into one folded row.
//
// A folded row is the label column, then as much of the content as fits, with a
// "…" where it was cut — and the row is re-drawn on every delta, so neither half
// may walk the whole message. summaryCounts is the price kept as content grows
// (and the reason a tab, a CR or a byte off the width table's ASCII route retires
// it: from there no delta can be priced exactly). headAndTailParts decides what
// the budget buys, and padLabel fixes the column the content starts in.

import (
	"strings"
)

// summaryCounts is the price of a text window's content, kept as that content
// grows so a folded row does not walk the whole message to draw two thirds of one
// line. note is the only thing that writes it and it writes all of it, which is
// the reason this is a value and not three fields on the renderer: loose fields
// can be updated in any combination, and a cell sum out of step with the byte
// count beside it prices a row against a message that is not the one being drawn.
//
// What the counts cannot cover is anything the byte-wise width route does not
// price — a byte over 0x7E, ESC, DEL — or anything prepareContent would rewrite:
// a tab or a carriage return. For every other byte one byte is one cluster, so a
// delta's cells add to the total exactly and no cluster can straddle two deltas.
// That is the whole reason this is sound where summing per-delta measurements of
// arbitrary text would not be: a combining mark arriving in the delta after its
// base would be priced as two clusters and drawn as one.
type summaryCounts struct {
	cells    int
	newlines int
	// bytes is how much of the content cells and newlines account for, and
	// summaryRetired from the first byte they cannot. Content only grows, so
	// counts that retired are retired for good.
	bytes int
}

// summaryRetired is what bytes holds once the counts are junk. No content length
// equals it, so covers answers false from then on with no second flag to keep in
// step with the first.
const summaryRetired = -1

// note folds one streaming delta in, or retires the counts. It is called from
// AppendFromFrame, the one path every byte of a text window's content arrives by —
// which is what makes bytes a length the content can be checked against rather
// than a number that happens to be near it.
func (c *summaryCounts) note(delta string) {
	if c.retired() {
		return
	}
	d, ok := plainSummaryCounts(delta)
	if !ok {
		c.retire()
		return
	}
	c.cells += d.cells
	c.newlines += d.newlines
	c.bytes += d.bytes
}

// retire drops the counts for good. Content only grows, so nothing brings them
// back, and a byte that retired them would retire them again.
func (c *summaryCounts) retire() { *c = summaryCounts{bytes: summaryRetired} }

// retired reports whether the counts have been dropped.
func (c summaryCounts) retired() bool { return c.bytes == summaryRetired }

// covers reports whether the counts account for every byte of a content n bytes
// long. The zero value accounts for none of a non-empty content, which is what
// makes it safe: a renderer built with its content already in it — a test fixture,
// or a restore path added later — is measured instead of priced at zero cells.
func (c summaryCounts) covers(n int) bool { return c.bytes == n }

// price is the summaryPrice these counts stand for, of a content covers has
// already said they account for. Both halves come from the same sums, so they
// cannot describe different messages.
func (c summaryCounts) price(raw string) summaryPrice {
	return summaryPrice{
		m:       measured{s: raw, cells: c.cells, ascii: true},
		escaped: c.cells + 2*c.newlines,
	}
}

// plainSummaryCounts prices s as a summaryCounts, and reports whether s is
// content counts can be kept for: every byte priced by the width table's
// byte-wise route, and left alone by prepareContent. One byte that fails either
// retires the counts for the whole content, so both questions are asked in the
// same pass over the delta.
func plainSummaryCounts(s string) (summaryCounts, bool) {
	var c summaryCounts
	for i := 0; i < len(s); i++ {
		b := s[i]
		if offASCIIRoute(b) || b == '\t' || b == '\r' {
			return summaryCounts{}, false
		}
		c.cells += asciiCells(b)
		if b == '\n' {
			c.newlines++
		}
	}
	c.bytes = len(s)
	return c, true
}

// foldedSummary returns the summary text for the folded view and
// the byte offset of the "…" truncation marker within that summary (or
// -1 if no marker is present). All non-delta text uses head+tail (so
// the user sees both topic and latest content); the only leading "…"
// is reserved for streaming delta content, which lives in toolRenderer.
//
// It takes the message already priced, because that price is what a renderer
// keeps as its content grows (summaryContent) and re-deriving either half of it
// is a pass over the whole message.
func foldedSummary(p summaryPrice, summaryWidth int) (string, int) {
	head, tail, truncated := headAndTailParts(p, summaryWidth)
	switch {
	case !truncated, tail == "":
		return head, -1
	default:
		return head + "…" + tail, len(head)
	}
}

// headAndTailParts returns the leading and trailing parts of a message for a
// folded text window (REASONING / ASSISTANT / USER PROMPT) as separate
// strings, plus a flag indicating whether the content was truncated. The
// middle "…" marker is not included — callers render it themselves with their
// own Style, which is what lets the marker be dimmed independently.
//
// Layout rule: first ~40% of maxWidth cols, a "…", then the last ~60%. The head
// conveys the topic of the message ("Here's how to…", "The user is asking
// about…"), the tail conveys the actual content / punchline. For streaming-tail
// content (tool deltas, UF snapshots) callers use tailParts instead — there the
// latest content is the only signal that matters.
//
//   - maxWidth <= 0  : return ("", "", false)
//   - maxWidth <= 2  : render the head only (no room for "…" + tail)
//   - maxWidth >= 3  : head + tail, ~40% / ~60% (integer math), with a hard
//     floor of 1 col on head so something is always emitted, and a floor of
//     1 col on tail (if head already claims the width, fall back to head-only)
//   - if the full content already fits maxWidth cols, head is the whole thing
//
// Grapheme-cluster-aware: head and tail are bounded by grapheme cluster
// boundaries (not runes) via measured.head/tail — takeCells/tailCells with the
// whole-string measurement hoisted — so multi-codepoint clusters like ZWJ
// emoji, combining marks, and variation selectors are never split
// mid-cluster. The budget and the cut come from the same width table (width.go).
//
// When truncated is false, head is the full content and tail is "".
// When truncated is true and tail is "", the function fell back to head-
// only (very narrow widths where there's no room for ellipsis + tail).
//
// The message arrives already priced (summaryPrice) rather than as a string: the
// fit check reads the escaped width and the cuts read the measurement, and all
// three questions used to derive what they needed themselves. A folded row so
// walked a 128 KB message four times over — three widths and the escape probe
// each cutter repeated — to draw two thirds of one line, and a CPU profile put
// the escape probe alone at a third of the frame. Pricing once and spending the
// answer is what makes a folded row O(budget) instead of O(message).
func headAndTailParts(p summaryPrice, maxWidth int) (head, tail string, truncated bool) {
	if maxWidth <= 0 {
		return "", "", false
	}
	if p.escaped <= maxWidth {
		return escapeBreaks(p.m.s), "", false
	}
	if maxWidth <= 2 {
		return cutMeasured(p.m, maxWidth, false), "", true
	}

	// 40/60 split. Integer math: headWidth = maxWidth * 40 / 100.
	// For typical widths (>= 5) this rounds close to 40%; very narrow
	// widths (3-4) end up head=1, which is the floor.
	headWidth := maxWidth * 40 / 100
	if headWidth < 1 {
		headWidth = 1
	}
	tailWidth := maxWidth - headWidth - 1
	if tailWidth < 1 {
		// Very narrow widths where head already claims most of the room.
		return cutMeasured(p.m, maxWidth, false), "", true
	}
	return cutMeasured(p.m, headWidth, false), cutMeasured(p.m, tailWidth, true), true
}

// escapeBreaks renders s as one logical line: each '\n' becomes the
// two-character marker `\n`, and a stray '\r' goes away. A summary is one row,
// and line heights count '\n', so a real break in one would move every row
// below it. Both ReplaceAll calls hand back their input untouched when there is
// nothing to replace, so this costs a scan plus a copy only when s has a
// newline in it.
//
// Not tool_handler.go's escapeNewlines, and the two must not be merged: that
// one also turns a tab into `\t` for a command line shown inline and keeps
// '\r', while a summary has already had its tabs unfolded to spaces
// (prepareContent / flattenDelta) and must not carry a carriage return.
func escapeBreaks(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", "\\n"), "\r", "")
}

// escapedWidth returns the width s will measure once escapeBreaks has run,
// without building it: a '\n' is 0 cells and its marker is 2, a '\r' is 0 cells
// and is deleted, and every other character keeps the width it has.
//
// cells is s's measured width — measure(s).cells — handed in rather than
// re-derived, because both callers have already measured s in order to cut it.
func escapedWidth(s string, cells int) int {
	return cells + 2*strings.Count(s, "\n")
}

// summaryPrice is one message priced for a folded row: the measurement its cuts
// spend, and the width that measurement becomes once escapeBreaks has run, which
// is what "does it all fit" reads. The two are facts about the same string, and a
// summary that priced the fit against one message and cut another would draw a row
// that does not fit it — so they travel together and are gathered in one call.
//
// A caller with the content in hand calls priceSummary. A textRenderer does not have
// it in hand: it keeps the two numbers beside its content as that content grows
// (summaryContent), which is the whole reason a folded 128 KB row is O(budget).
// That is the only place the pair is assembled by hand, and it is the only place
// that can get it wrong.
type summaryPrice struct {
	m       measured
	escaped int
}

// priceSummary prices s for a folded row.
func priceSummary(s string) summaryPrice {
	m := measure(s)
	return summaryPrice{m: m, escaped: escapedWidth(s, m.cells)}
}

// cutMeasured returns budget cells from the wanted end of already-measured
// content, escaped. Both summary paths call it, and both have measured their
// content already to answer "does it all fit".
//
// It cuts BEFORE it escapes, and then cuts the escaped result to the same
// budget from the same end. That is not the obvious order — the obvious one
// escapes the whole message and keeps 30 cells of it — but it is the same
// answer, because escaping can only widen a character: the escaped text that
// fits a budget always sits inside the raw cut, so re-cutting from the same end
// with the same budget lands on the same boundary. Both orders are run over the
// corpus at every budget by TestSummaryEscapeOrderIsEquivalent, which keeps the
// escape-first form as its oracle.
//
// The order is the whole point. A folded window re-derives its summary on every
// delta, so escaping first copied the entire message per frame to draw two
// thirds of one row — and the same shape sat in tailParts, on the streaming
// tool preview (docs/internal/virtual-rendering-performance.md).
//
// The re-cut of the escaped text goes through the one-shot cutters: that string
// is at most budget cells long, so there is nothing left to hoist. No budget or
// empty content needs a guard here either — head and tail answer "" for both.
func cutMeasured(m measured, budget int, fromTail bool) string {
	cut := m.head(budget)
	if fromTail {
		cut = m.tail(budget)
	}
	escaped := escapeBreaks(cut)
	if fromTail {
		return tailCells(escaped, budget)
	}
	return takeCells(escaped, budget)
}

// padLabel left-justifies a folded-window label to the fixed label
// column (FoldedLabelWidth, display columns) so content lines align
// across window types. Longer labels (e.g. "TOOL execute_command") are
// returned unchanged. Width is measured in display columns (not bytes) —
// labels may contain multi-byte glyphs such as the tool indicator.
func padLabel(label string) string {
	if label == "" {
		return ""
	}
	w := cellWidth(label)
	if w >= FoldedLabelWidth {
		return label
	}
	return label + strings.Repeat(" ", FoldedLabelWidth-w)
}
