package terminal

// Window renderers: type-specific content management and rendering.
//
// Each renderer implements WindowRendering and owns its content storage
// and caching. The Window struct delegates to the renderer for everything
// that varies by window type.

import (
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// textRenderer — Assistant text (AT), reasoning (AR), system messages (SN/SE)
// ============================================================================

// textRenderer handles simple text content with optional streaming deltas.
// Used for AT, AR, SN, and SE tags.
type textRenderer struct {
	tag          string   // TLV tag that created this window
	content      string   // full content (built from parts on demand)
	contentLen   int      // cumulative length of all deltas
	contentParts []string // streaming deltas (avoids O(n²) string concat)

	// The price of the content, kept as the content grows so a folded row does not
	// walk the whole message to draw two thirds of one line. summaryCounts carries
	// the reasoning, the conditions a byte has to meet to be counted, and why the
	// zero value is safe.
	summary summaryCounts

	mdMode bool // render markdown (toggled with 'r'; AT/AR only)

	// mdTailInTable reports whether the accumulated content ends inside a
	// table block (last line starts with '|' and no closing blank/text
	// line yet). While true, any delta may re-flow already-rendered table
	// rows, so the incremental path is unsafe. Only meaningful in mdMode.
	mdTailInTable bool

	// Cached wrapped lines for fast incremental update via
	// appendDeltaToVisualLines. Populated by BuildInner, updated
	// incrementally by AppendFromTLV. Each row carries a continuation
	// mark (visualLine.Cont) — rows of the same original line join
	// without '\n' (terminal soft-wrap); different original lines are
	// separated by hard '\n'.
	wrappedLines []visualLine
	cacheWidth   int  // inner width used for wrapping (0 = unknown)
	cacheValid   bool // true = BuildInner can skip full re-wrap

	// Body-colored copy of wrappedLines, materialized only while an
	// overlay is active (styles.Body carries a foreground). coloredDirty
	// is set whenever the plain cache changes (incremental append), so
	// the next BuildInner recolors once instead of on every frame.
	colored      []visualLine
	coloredDirty bool
}

func (r *textRenderer) Tag() string { return r.tag }

func (r *textRenderer) ToolInfo() *ToolInfo { return nil }

func (r *textRenderer) AppendFromTLV(_ string, value string) {
	// Sanitized before anything counts it: contentLen and the summary's counts both
	// have to describe the bytes that will be drawn, and those are the repaired ones.
	value = sanitizeUTF8(value)
	r.contentParts = append(r.contentParts, value)
	r.contentLen += len(value)
	r.summary.note(value)
	// The plain wrappedLines cache may change on any of the paths below,
	// so the body-colored copy must be rebuilt on the next BuildInner.
	r.coloredDirty = true

	// Markdown mode: plain deltas — no '|'-prefixed line and the content
	// tail not inside an open table — go through the incremental wrap
	// path, exactly like raw mode. Anything that could form, extend, or
	// re-flow a table (a '|'-prefixed line, or any delta while the tail is
	// still inside a table) falls back to a full re-render: column widths and
	// cell wrap points are a whole-table property, so the incremental path
	// cannot re-flow already-rendered rows.
	if r.mdMode {
		switch {
		case r.mdTailInTable || deltaHasPipeLine(value):
			r.cacheValid = false
			r.wrappedLines = nil
		case len(r.wrappedLines) > 0 && r.cacheWidth > 0:
			r.wrappedLines = appendDeltaToVisualLines(r.wrappedLines, stripANSI(value), r.cacheWidth)
		default:
			r.cacheValid = false
		}
		r.updateMDTail(value)
		return
	}

	// Incremental update: append the delta to wrappedLines as PLAIN TEXT.
	// This is only valid for windows that render plain (AT/AR — streaming
	// content deliberately carries no styling in normal mode; markdown
	// table rendering is handled above and produces plain text too).
	// Every text window (AT/AR/SN/SE) is plain, so the incremental append
	// is always safe. Under an overlay the dim Body color is applied on
	// top later, when BuildInner returns (bodyStyled) — never here — so
	// the incremental path stays ANSI-free and O(delta).
	if r.plainContent() && len(r.wrappedLines) > 0 && r.cacheWidth > 0 {
		// stripANSI only (no expandTabs here): tabs are expanded per
		// original line inside wrapVisualLines, so incremental and full
		// re-wrap agree on tab columns even when a delta starts with '\t'.
		r.wrappedLines = appendDeltaToVisualLines(r.wrappedLines, stripANSI(value), r.cacheWidth)
		// cacheValid stays false — the wrap cache needs a rebuild, but
		// wrappedLines is current for TryLineCount.
	} else {
		r.cacheValid = false
	}
}

func (r *textRenderer) Invalidate() {
	r.cacheValid = false
	r.wrappedLines = nil
	r.colored = nil
	r.coloredDirty = true
}

// bodyStyled returns the visual lines with styles.Body applied. In
// normal mode Body carries no foreground, so the plain lines are
// returned unchanged (body text stays in the terminal's default color,
// no ANSI emitted — zero allocation). Under an overlay (Dimmed styles)
// Body carries ColorDim, and the colored copy is cached so incremental
// streaming recolors only once per delta, not on every frame.
func (r *textRenderer) bodyStyled(lines []visualLine, styles *Styles) []visualLine {
	if styles == nil || styles.Body.GetForeground() == nil {
		r.colored = nil
		r.coloredDirty = false
		return lines
	}
	if !r.coloredDirty && len(r.colored) == len(lines) {
		return r.colored
	}
	colored := make([]visualLine, len(lines))
	for i, l := range lines {
		// The padding carries over: Body.Render wraps the row in SGR sequences,
		// and escapes charge no cells, so the row draws the same width as before
		// and needs the same spaces after it. Dropping it here would take the
		// padding off every dimmed row, which is every row an overlay draws.
		colored[i] = visualLine{Text: styles.Body.Render(l.Text), Pad: l.Pad, Cont: l.Cont}
	}
	r.colored = colored
	r.coloredDirty = false
	return colored
}

// styleBodyLines applies styles.Body to the plain (ANSI-free) rows of a
// window's inner content. Rows already carrying SGR styling (separators,
// diff rows, media badges) are left untouched — under an overlay those
// styles already resolve to ColorDim via Styles.Dimmed, so applying Body
// again would only nest redundant sequences. When styles.Body has no
// foreground (normal mode) the lines are returned unchanged, keeping
// body text in the terminal's default color.
func styleBodyLines(lines []visualLine, styles *Styles) []visualLine {
	if styles == nil || styles.Body.GetForeground() == nil {
		return lines
	}
	out := make([]visualLine, len(lines))
	for i, l := range lines {
		if strings.Contains(l.Text, "\x1b[") {
			out[i] = l
			continue
		}
		out[i] = visualLine{Text: styles.Body.Render(l.Text), Pad: l.Pad, Cont: l.Cont}
	}
	return out
}

// updateMDTail updates the open-table tail state after appending a delta.
// A delta WITHOUT '\n' merges into the last line: when that line is a
// table row (mdTailInTable), the MERGED line still starts with '|' — e.g.
// "| … | /run/user/" + "1 |" = "| … | /run/user/1 |" — so the tail stays
// inside the table even though the delta's own text ("1 |") does not
// start with '|'. Judging the delta in isolation here was the cause of
// the df-output bug: a mid-cell token split ("/run/user/" + "1 |" +
// "001 |") reset the state to false and the final delta was appended
// incrementally onto the rendered table row.
func (r *textRenderer) updateMDTail(value string) {
	if r.mdTailInTable && !strings.Contains(value, "\n") {
		return // merged into the table row; tail stays inside the table
	}
	r.mdTailInTable = hasPipePrefix(lastLine(value))
}

// ToggleMarkdownMode flips markdown rendering and invalidates the
// wrapped-line cache so the next BuildInner re-renders from scratch.
// Returns the new state.
func (r *textRenderer) ToggleMarkdownMode() bool {
	r.mdMode = !r.mdMode
	if r.mdMode {
		// Re-derive the open-table tail state from the accumulated content
		// so the first delta after toggling uses the right path.
		r.mdTailInTable = hasPipePrefix(lastLine(r.rawContent()))
	}
	r.Invalidate()
	return r.mdMode
}

// rawContent returns the full accumulated content: r.content with the
// pending streaming deltas folded in. It does NOT store the merge — the
// deltas stay separate until mergeParts folds them for good.
//
// This is the read path, not a test helper: BuildCollapsed derives a folded
// summary from it (one call per frame while a text window streams), as does
// Window.RawContent and the markdown tail probe.
func (r *textRenderer) rawContent() string {
	if len(r.contentParts) == 0 {
		return r.content
	}
	// Size the buffer exactly, so the merge is one allocation and one copy
	// rather than the builder's doubling. The scan is over the pending
	// deltas only, which maxContentParts bounds.
	n := len(r.content)
	for _, p := range r.contentParts {
		n += len(p)
	}
	var buf strings.Builder
	buf.Grow(n)
	buf.WriteString(r.content)
	for _, p := range r.contentParts {
		buf.WriteString(p)
	}
	return buf.String()
}

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
// AppendFromTLV, the one path every byte of a text window's content arrives by —
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

// summaryContent is the content a folded summary draws, priced.
//
// While the counts cover every byte of raw, prepareContent has nothing to do — a
// tab, a carriage return and an escape each retire them — so the content is raw
// itself and the price is the counts. Those are the passes over the message this
// exists to avoid: on 128 KB, measure is 38μs of a 46μs BuildCollapsed,
// prepareContent's two scans are 2.7μs more, and escapedWidth counts the line
// breaks in a third.
func (r *textRenderer) summaryContent(raw string) summaryPrice {
	if r.summary.covers(len(raw)) {
		return r.summary.price(raw)
	}
	return priceSummary(prepareContent(raw))
}

// maxContentParts bounds how many streaming deltas may sit unmerged in
// contentParts.
//
// Append keeps each delta separate so one costs O(delta) instead of
// O(content) — that is the whole point of the slice. But the list cannot grow
// without bound: every header is memory, and every rawContent (one per frame
// while a text window streams folded) walks the lot. Folding is a full copy of
// the accumulated content, so it is amortized over this many deltas rather
// than paid per frame: at 64, a 128 KB message copies 2 KB per delta instead
// of 128 KB per frame.
const maxContentParts = 64

// mergeParts folds the pending deltas into r.content and drops them, so the
// paths that need one string see it. Callers that only need to READ the
// content use rawContent, which merges without storing.
func (r *textRenderer) mergeParts() {
	if len(r.contentParts) == 0 {
		return
	}
	r.content = r.rawContent()
	r.contentParts = nil
}

// plainContent returns true when this text window's content must render
// as plain body text: assistant text and reasoning are streaming content
// and deliberately carry no color/weight in normal mode — markdown table
// rendering (mdMode) also emits plain text, only re-arranging columns.
// The returned lines gain the dim Body color only under an overlay
// (see bodyStyled / styles.Body).
func (r *textRenderer) plainContent() bool {
	return r.tag == tlv.TagAssistantT || r.tag == tlv.TagAssistantR
}

// BuildInner returns the inner content as visual lines.
// In normal mode the content is PLAIN TEXT (no styling — markdown tables
// are padded plain text when mdMode is on), so only wrapping is applied
// and body text renders in the terminal's default color. Under an
// overlay (styles from Styles.Dimmed) bodyStyled wraps the rows in the
// dim Body color. Each returned line is one terminal row (no '\n'
// inside) with a continuation mark: rows of the same original line join
// without '\n' (soft wrap); rows starting a new original line are
// separated by hard '\n'. lineCount is the window's expanded height
// (len(lines) + 1): the content rows plus the labeled rule that opens
// them.
func (r *textRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	// Fast path: use cached wrapped lines if width matches.
	// wrappedLines is kept current by AppendFromTLV's incremental path.
	if r.cacheWidth == innerWidth && len(r.wrappedLines) > 0 {
		// The rows are already right, so this frame has no use for one
		// merged string — only the pending-delta list must stay bounded
		// (maxContentParts). This used to merge on EVERY frame, copying the
		// whole accumulated content to produce a string nothing then read.
		if len(r.contentParts) >= maxContentParts {
			r.mergeParts()
		}
		return r.bodyStyled(r.wrappedLines, styles), len(r.wrappedLines) + 1
	}

	// Full render: prepare, style (system messages only), and wrap.
	// The wrap reads one string, so the deltas are folded for good.
	r.mergeParts()

	// stripANSI only — tabs are expanded per original line inside
	// wrapVisualLines so the full path matches the incremental path.
	content := stripANSI(r.content)
	if r.mdMode {
		// Markdown table transform (toggled with 'r'). Tabs inside table
		// lines are expanded per original line by the parser itself, so
		// column widths match what the terminal will render; the padded
		// output contains no tabs, leaving wrapVisualLines' expandTabs a
		// no-op. Cells are wrapped by the transform itself, so no row ever
		// exceeds innerWidth and the terminal never has to soft-wrap one.
		content = renderMarkdownTables(content, innerWidth)
	}
	r.wrappedLines = wrapVisualLines(content, innerWidth)
	r.cacheWidth = innerWidth
	r.cacheValid = true

	return r.bodyStyled(r.wrappedLines, styles), len(r.wrappedLines) + 1
}

// BuildCollapsed returns the single-line collapsed form: the label column, then a
// summary of the content — "REASONING  the head of the message…its tail" — cut to
// fit width minus collapsedPrefixWidth, the arrow and the space after it.
//
// The summary is head + "…" + tail, not the tail alone: the head carries the topic
// of the message and the tail carries where it has got to, which in a window that
// re-summarizes on every delta is the part that moves. headAndTailParts gives the
// head 40% of the budget and the tail what is left after the marker; when the whole
// content fits, there is no marker and the summary is all of it. Newlines are escaped
// to the literal "\n" so the row stays one line — line heights count '\n', so a real
// break in a summary would move every row below it. The "…" is therefore in the
// middle or nowhere. A *leading* "…" is a different summary shape, belongs to the
// streaming delta previews, and lives in toolRenderer.
//
// AT/AR: the label is styled (bold + muted) and the content summary is
// muted — the collapsed header is UI chrome, while the expanded body
// stays plain text in normal mode (dimmed Body color under overlays).
// The collapsed preview always shows the RAW content
// (never the markdown table transform — the preview is one line and
// markdown state only affects expanded rendering).
//
// The marker is rendered with styles.Status (the dim color) to separate it from
// the content, which uses the muted foreground — renderCollapsedLineWithEllipsis
// splits the row at the marker's offset to do it. toolRenderer dims its own
// markers, middle and leading, at the places it builds them.
func (r *textRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	label := labelForTag(r.tag)
	line := ""
	if label != "" {
		line = padLabel(label)
	}
	summaryWidth := max(0, width-collapsedPrefixWidth-CollapsedLabelWidth)
	summary, ellipsisOffset := collapsedSummary(r.summaryContent(r.rawContent()), summaryWidth)
	line += summary
	line = truncateWithSuffix(line, max(0, width-collapsedPrefixWidth)) // safety net

	if label == "" {
		if styles == nil {
			return line, 1
		}
		return styles.System.Render(line), 1
	}
	return renderCollapsedLineWithEllipsis(line, label, ellipsisOffset, r.tag, styles), 1
}

// collapsedSummary returns the summary text for the collapsed view and
// the byte offset of the "…" truncation marker within that summary (or
// -1 if no marker is present). All non-delta text uses head+tail (so
// the user sees both topic and latest content); the only leading "…"
// is reserved for streaming delta content, which lives in toolRenderer.
//
// It takes the message already priced, because that price is what a renderer
// keeps as its content grows (summaryContent) and re-deriving either half of it
// is a pass over the whole message.
func collapsedSummary(p summaryPrice, summaryWidth int) (string, int) {
	head, tail, truncated := headAndTailParts(p, summaryWidth)
	switch {
	case !truncated, tail == "":
		return head, -1
	default:
		return head + "…" + tail, len(head)
	}
}

// renderCollapsedLineWithEllipsis styles a collapsed line: the label
// portion in its type style, the content portion (padding + head) in
// muted (NOT bold — only the label is bold), the "…" truncation marker
// in dim, and the tail in muted. If styles is nil, the line is returned
// unstyled beyond the label.
//
// Note: labelStyleForTag is applied ONLY to the label portion. Earlier
// revisions applied it to line[:ellipsisAbs], which included the head
// content — that accidentally bolded the head while the tail stayed
// muted-only, producing inconsistent visual weight between head and
// tail. The label is bold by design (it's the "chrome"); the head is
// content and should match the tail's muted weight.
func renderCollapsedLineWithEllipsis(line, label string, ellipsisOffset int, tag string, styles *Styles) string {
	labelPart := padLabel(label)
	// The label column is a byte offset into a line a later truncation may
	// have shortened into the middle of the column — and the ellipsis it
	// cut with is multi-byte, so the offset has to land on a rune boundary
	// (see runeBoundary).
	labelEnd := runeBoundary(line, len(labelPart))
	styledLabel := lineStyleForTag(tag, styles).Render(line[:labelEnd])
	if len(line) <= labelEnd {
		return styledLabel
	}
	if styles == nil {
		return styledLabel + line[labelEnd:]
	}
	content := line[labelEnd:]
	// The marker's offset was computed before the caller's safety-net
	// truncation, so it is only usable while it still points at a marker:
	// a line the truncation shortened has its ellipsis somewhere else (or
	// none), and slicing at a stale byte offset would split a rune or run
	// off the end. Verified, not assumed — the whole content is styled as
	// one run when the offset no longer holds.
	if ellipsisOffset < 0 || ellipsisOffset+len("…") > len(content) ||
		!strings.HasPrefix(content[ellipsisOffset:], "…") {
		return styledLabel + styles.System.Render(content)
	}
	// Content = padding + head + "…" + tail.
	head := content[:ellipsisOffset]
	marker := content[ellipsisOffset : ellipsisOffset+len("…")]
	tail := content[ellipsisOffset+len("…"):]
	return styledLabel +
		styles.System.Render(head) +
		styles.Status.Render(marker) +
		styles.System.Render(tail)
}

// labelForTag returns the header label for text-type windows.
func labelForTag(tag string) string {
	switch tag {
	case tlv.TagAssistantR:
		return "REASONING"
	case tlv.TagAssistantT:
		return "ASSISTANT"
	case tlv.TagUserT:
		return "USER PROMPT"
	case TagWindowSN:
		return "SYSTEM NOTIFY"
	case TagWindowSE:
		return "SYSTEM ERROR"
	default:
		return ""
	}
}

// TryLineCount returns the line count from cached wrapped lines (fast path).
// With incremental append, wrappedLines is kept current during streaming,
// so this succeeds even after content changes (no cacheValid check).
// The count is the window's expanded height: the content rows plus the one
// labeled rule that opens it.
func (r *textRenderer) TryLineCount(width int) (int, bool) {
	innerWidth := max(0, width)
	if len(r.wrappedLines) > 0 && r.cacheWidth == innerWidth {
		return len(r.wrappedLines) + 1, true
	}
	return 0, false
}

// tailParts returns the tail of content (no leading "…") and a flag
// indicating whether truncation occurred. Callers that need to style the
// "…" marker differently from the content (e.g. dim vs muted) use this so
// they can render each piece with its own Style.
//
// When maxWidth <= 1, returns ("", false) — there's no room for content
// even without a marker.
func tailParts(content string, maxWidth int) (string, bool) {
	if maxWidth <= 1 {
		return "", false
	}
	m := measure(content)
	if escapedWidth(content, m.cells) <= maxWidth {
		return escapeBreaks(content), false
	}
	// Take the tail that fits. We deliberately use the FULL maxWidth
	// here (not maxWidth-1) — callers that prepend a "…" marker subtract
	// 1 from their own budget before calling (see window_renderer.go),
	// since we don't know if they want a marker at all.
	//
	// The cut is tailCells, so it lands on a cluster boundary. It used to be a
	// backwards walk over []rune measuring one rune at a time, which split
	// multi-rune clusters: tailParts("aaaa 👨‍👩‍👧‍👦", 2) returned
	// ZWJ+boy, the back half of a family emoji, on the row a user watches while
	// a command runs. That walk also allocated a string per rune it examined.
	return cutMeasured(m, maxWidth, true), true
}

// headAndTailParts returns the leading and trailing parts of a message for a
// collapsed text window (REASONING / ASSISTANT / USER PROMPT) as separate
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
// '\r', while a summary has already had its tabs expanded to spaces
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

// summaryPrice is one message priced for a collapsed row: the measurement its cuts
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

// priceSummary prices s for a collapsed row.
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

// firstLine returns the first line of s (up to the first '\n').
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// padLabel left-justifies a collapsed-window label to the fixed label
// column (CollapsedLabelWidth, display columns) so content lines align
// across window types. Longer labels (e.g. "TOOL execute_command") are
// returned unchanged. Width is measured in display columns (not bytes) —
// labels may contain multi-byte glyphs such as the tool indicator.
func padLabel(label string) string {
	if label == "" {
		return ""
	}
	w := cellWidth(label)
	if w >= CollapsedLabelWidth {
		return label
	}
	return label + strings.Repeat(" ", CollapsedLabelWidth-w)
}

// flattenDelta flattens a streaming delta to a single line, expanding
// tabs first so width accounting matches the final render (expandTabs →
// TabWidth columns; the width table counts a tab as 0 cells).
func flattenDelta(delta string) string {
	d := strings.ReplaceAll(delta, "\n", " ")
	d = strings.ReplaceAll(d, "\r", "")
	return expandTabs(d)
}

// compactMediaSummary renders attachment labels as a compact single-line
// summary. Duplicate media types are counted (for example, "📷1 🎵1") so a
// collapsed window does not hide the fact that more than one attachment is
// present.
func compactMediaSummary(mediaParts []string) string {
	counts := make(map[string]int, len(mediaParts))
	order := make([]string, 0, len(mediaParts))
	seen := make(map[string]struct{}, len(mediaParts))

	for _, label := range mediaParts {
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			counts[label]++
			continue
		}
		seen[label] = struct{}{}
		counts[label] = 1
		order = append(order, label)
	}

	parts := make([]string, 0, len(order))
	for _, label := range order {
		parts = append(parts, compactMediaIcon(label)+strconv.Itoa(counts[label]))
	}
	return strings.Join(parts, " ")
}

// compactMediaIcon returns the compact icon used by compactMediaSummary.
// Known labels are explicit; unknown labels use their first token.
func compactMediaIcon(label string) string {
	switch label {
	case tlv.MediaLabel(tlv.TagUserI):
		return "📷"
	case tlv.MediaLabel(tlv.TagUserV):
		return "🎬"
	case tlv.MediaLabel(tlv.TagUserA):
		return "🎵"
	case tlv.MediaLabel(tlv.TagUserD):
		return "📄"
	}
	if i := strings.IndexByte(label, ' '); i >= 0 {
		return label[:i]
	}
	return label
}

// ============================================================================
// userRenderer — User messages with optional media attachments (UT)
// ============================================================================

// userRenderer handles user messages that may include media attachments.
// Text parts and media labels are stored separately and combined at render time.
type userRenderer struct {
	textParts  []string // user text, in order
	mediaParts []string // media labels, in order
	contentLen int
}

func (r *userRenderer) Tag() string { return tlv.TagUserT }

func (r *userRenderer) ToolInfo() *ToolInfo { return nil }

func (r *userRenderer) AppendFromTLV(tag string, value string) {
	value = sanitizeUTF8(value)
	switch tag {
	case tlv.TagUserT:
		if value != "" {
			r.textParts = append(r.textParts, value)
		}
	case tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		r.mediaParts = append(r.mediaParts, tlv.MediaLabel(tag))
	}
	r.contentLen += len(value)
}

func (r *userRenderer) Invalidate() {}

// BuildInner renders the user message as visual lines: media section
// first (on top), then text below. This matches the natural content
// order: media parts precede the text part. The two need no divider
// between them — the media block is the window's own header material
// (default color, bold, four fixed labels), the text under it is plain,
// and the window's line above already opened the block; a rule there would
// delimit a boundary nobody mistakes. Multiple text parts ARE separated
// with "───" (Separator) in System color, because two text parts carry
// identical styling and nothing else tells them apart. Each returned line
// is one terminal row (no '\n' inside); lineCount is the content rows plus
// the window's own line (len(lines) + 1).
//
// Wrapping is performed by wrapVisualLines (NOT by a pre-pass of
// wrapContent): a pre-pass would insert hard '\n' at wrap points and
// wrapVisualLines would then mistake them for original-line breaks,
// collapsing soft-wrap semantics. Only genuinely-long SINGLE lines
// use soft-wrap (their continuation rows join without '\n'); ordinary
// multi-line content stays multi-line.
func (r *userRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	var parts []string

	// Media portion — rendered first (on top)
	if len(r.mediaParts) > 0 {
		mediaBlockStr := wrapLabels(r.mediaParts, innerWidth, styles.Attachment)
		parts = append(parts, mediaBlockStr)
	}

	// Text portion: text parts separated by Separator ("───")
	if len(r.textParts) > 0 {
		var textBlock strings.Builder

		firstText := true
		for _, part := range r.textParts {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if !firstText {
				textBlock.WriteString("\n")
				textBlock.WriteString(styles.System.Render(Separator))
				textBlock.WriteString("\n")
			}
			textBlock.WriteString(trimmed) // user text is plain (no bold/color); overlay dimming is applied later via styleBodyLines
			firstText = false
		}

		if textBlock.Len() > 0 {
			parts = append(parts, textBlock.String())
		}
	}

	result := strings.Join(parts, "\n")

	// Wrap into visual rows with continuation marks: rows of the same
	// original line join without '\n' (soft wrap); rows starting a new
	// original line are separated by hard '\n'.
	lines := wrapVisualLines(result, innerWidth)

	// Body color for the plain text rows (overlay dimming); styled rows
	// (media badges, separators) keep their own styles.
	return styleBodyLines(lines, styles), len(lines) + 1
}

// BuildCollapsed returns the single-line collapsed form: the label column, then a
// head + "…" + tail summary of the content — "USER PROMPT 📷2 the head of the
// prompt…its tail" — cut to fit width minus the arrow. The content summarized is
// the media badges followed by the text (compactMediaSummary, "📷2 📄1"), so the
// badges land in the head and stay visible however long the prompt is. A media-only
// message has no text after them, and shows the attachment types and their counts
// in the available width.
//
// Truncation markers ("…") are rendered with styles.Status to visually
// separate them from the actual content (muted).
func (r *userRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	textContent := prepareContent(strings.Join(r.textParts, "\n"))
	textContent = strings.TrimSpace(textContent)
	mediaSummary := compactMediaSummary(r.mediaParts)
	if textContent == "" && mediaSummary == "" {
		return "", 1
	}

	label := padLabel("USER PROMPT")
	room := max(0, width-collapsedPrefixWidth-cellWidth(label))

	// Combine media + text into a single content string and run head+tail
	// truncation on it (same rule as all other non-delta text windows).
	// The "…" naturally falls between media and text when truncation cuts
	// there, and never appears at the line start — only streaming delta
	// content uses leading "…".
	var content string
	switch {
	case mediaSummary != "" && textContent != "":
		content = mediaSummary + " " + textContent
	case mediaSummary != "":
		content = mediaSummary
	default:
		content = textContent
	}
	head, tail, truncated := headAndTailParts(priceSummary(content), room)

	plainLine := label + head
	if tail != "" {
		plainLine += "…" + tail
	}
	line := truncateWithSuffix(plainLine, max(0, width-collapsedPrefixWidth)) // safety net

	return collapsedRow{
		line: line, label: label, head: head, tail: tail,
		truncated: truncated, media: mediaSummary,
	}.style(styles), 1
}

// collapsedRow is a user window's collapsed line: the plain row as it will be
// drawn, plus the runs that produced it. userRenderer.BuildCollapsed decides
// where to cut, collapsedRow.style decides which run each byte belongs to, and
// the second question can only be answered from the first one's result — so
// the row travels with its parts instead of the styler re-deriving them from
// the content. (Splitting the two is also what fits BuildCollapsed back into
// the complexity budget it had outgrown.)
type collapsedRow struct {
	line      string // plain, already truncated to the row's width
	label     string // the padded label column; a prefix of line
	head      string // content kept from the front, after the label
	tail      string // content kept from the back; "" when nothing was cut
	truncated bool   // head+tail truncation cut into the content
	media     string // the badge summary as it stood before truncation
}

// style paints the row's runs: the label in the window line's register, the
// badge run in the attachment style, the text muted, "…" dim.
//
// The badges take styles.Attachment — the one style the expanded body draws
// them in — because the collapsed row and the expanded block show the same
// tokens, and folding must not restyle them (toolNameStyle, window.go, is the
// same lesson from the other side). The summary is a prefix of the content, so
// a prefix test on the plain line locates it exactly; when truncation cut into
// the badge run itself there is no intact run to paint, and the summary stays
// in the content's muted color rather than showing half a badge in bold.
func (row collapsedRow) style(styles *Styles) string {
	labelStyle := lineStyleForTag(tlv.TagUserT, styles)
	if len(row.line) <= len(row.label) {
		return labelStyle.Render(row.line)
	}
	var rendered strings.Builder
	rendered.WriteString(labelStyle.Render(row.line[:len(row.label)]))

	rest := row.line[len(row.label):]
	mediaRun := 0
	if strings.HasPrefix(rest, row.media) {
		mediaRun = min(len(row.media), len(row.head))
	}
	if mediaRun > 0 {
		rendered.WriteString(styles.Attachment.Render(rest[:mediaRun]))
		rest = rest[mediaRun:]
		row.head = row.head[mediaRun:]
	}

	if !row.truncated || row.tail == "" {
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String()
	}
	// head + "…" + tail, now that the badge run is off the front. The "…"
	// might have been cut by the safety-net truncation.
	headEnd := len(row.head)
	if headEnd > len(rest) {
		// head was truncated mid-way — fall back to muted
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String()
	}
	rendered.WriteString(styles.System.Render(rest[:headEnd]))
	ell := len("…")
	if headEnd+ell > len(rest) {
		// "…" was cut — render the rest as muted
		rendered.WriteString(styles.System.Render(rest[headEnd:]))
		return rendered.String()
	}
	rendered.WriteString(styles.Status.Render(rest[headEnd : headEnd+ell]))
	if headEnd+ell < len(rest) {
		rendered.WriteString(styles.System.Render(rest[headEnd+ell:]))
	}
	return rendered.String()
}

// ============================================================================
// toolRenderer — Tool calls and results (AF, UF)
// ============================================================================

// toolRenderer handles tool call windows that show input and optional output.
type toolRenderer struct {
	isUF   bool   // true for UF-only windows (no prior AF frame)
	name   string // tool name (e.g. "read_file")
	input  string // formatted tool call input (complete, from AF)
	output string // tool execution output
	status ToolStatus

	// deltaBuffer accumulates partial JSON from Af frames during streaming.
	// Not appended to window content — rendered as a one-line preview
	// alongside the tool name in the pending state.
	deltaBuffer string
}

func (r *toolRenderer) Tag() string { return tlv.TagAssistantF }

func (r *toolRenderer) ToolInfo() *ToolInfo {
	return &ToolInfo{
		Name:  r.name,
		Input: r.input,
	}
}

// setName, setInput, setOutput and the delta writer below are the only things that
// assign a tool renderer's drawn fields, and each one repairs what it is given.
// That is what makes "a Window's content is well-formed UTF-8" hold structurally
// rather than by every caller remembering: the fields are written in four places
// and all four sanitize, so a path added later either goes through one of them or
// fails TestEveryContentIngressDrawsWellFormedUTF8.
func (r *toolRenderer) setName(s string) { r.name = sanitizeUTF8(s) }

func (r *toolRenderer) setInput(s string) { r.input = sanitizeUTF8(s) }

func (r *toolRenderer) setOutput(s string) { r.output = sanitizeUTF8(s) }

func (r *toolRenderer) AppendFromTLV(_ string, value string) {
	// Tool data normally arrives via structured setters (HandleToolInput/HandleToolOutput).
	// For replayed content or direct testing, dispatch by window type.
	if r.isUF {
		r.setOutput(value)
	} else {
		r.setInput(value)
	}
}

func (r *toolRenderer) Invalidate() {}

// AppendDelta sets the latest partial JSON chunk for one-line preview.
// Each call replaces the previous delta — the window shows only the
// most recently received chunk.
func (r *toolRenderer) AppendDelta(delta string) {
	r.deltaBuffer = sanitizeUTF8(delta)
}

// BuildInner renders the tool window content as visual lines. Each
// returned line is one terminal row (no '\n' inside); lineCount
// includes the window's own line (len(lines) + 1).
func (r *toolRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	// UF-only windows (no tool name, created from UF tag) render as plain text.
	if r.isUF {
		output := r.output
		if p := r.previewOutput(innerWidth, styles); p != "" {
			// Uf preview snapshot — single line, truncated.
			output = p
		}
		styled := prepareContent(output)
		lines := wrapVisualLines(styled, innerWidth)
		return styleBodyLines(lines, styles), len(lines) + 1
	}

	// Input: streaming delta preview (truncated JSON) or the full input.
	// Neither carries the status indicator nor the "name: " prefix — the
	// indicator lives in the header line (TOOL CALL ⠋) and the tool name is
	// shown there too. Content is plain (no color, no bold); only the
	// "───" separator and any leading "…" truncation marker keep their
	// styled colors (muted / dim). The plain rows gain the dim Body color
	// under an overlay, via styleBodyLines on the wrapped result below.
	var call string
	if r.deltaBuffer != "" {
		// Arguments still streaming in — one-line preview showing the
		// LATEST chunk: like every other collapsed/delta summary, keep the
		// tail of the delta (new JSON arrives at the tail) and mark the
		// truncated head with a leading "…" (never a trailing ellipsis).
		deltaContent := flattenDelta(r.deltaBuffer)
		tail, truncated := tailParts(deltaContent, max(0, innerWidth-1))
		var sb strings.Builder
		if truncated {
			sb.WriteString(styles.Status.Render("…"))
		}
		sb.WriteString(tail)
		call = sb.String()
	} else {
		switch r.name {
		case "edit_file":
			// edit_file's input is a real diff (the model emits -/+
			// prefixed rows) — colored per row by RenderDiffContent.
			call = RenderDiffContent(r.input, r.name, styles)
		default:
			// Everything else — including write_file, whose input is the
			// RAW file content being written (not a diff; - / + prefixed
			// lines there are literal content and must stay plain in
			// normal mode; dimmed under overlays by styleBodyLines).
			call = renderToolArgLine(r.name, defaultToolRender(r.input, r.name), styles)
		}
	}

	// Append output with a "───" separator — uniform across all tools
	// (parameters/results divider; edit_file and write_file included).
	// Output rows are plain text (dim Body color under overlays); the
	// separator keeps its muted color.
	if r.output != "" {
		output := r.output
		if p := r.previewOutput(innerWidth, styles); p != "" {
			// Uf preview snapshot — single line, truncated.
			output = p
		}
		sep := styles.System.Render(Separator)
		styled := prepareContent(output)
		call = call + "\n" + sep + "\n" + styled
	}

	lines := wrapVisualLines(call, innerWidth)
	return styleBodyLines(lines, styles), len(lines) + 1
}

// BuildCollapsed returns the single-line collapsed form for tool windows:
// "TOOL CALL ⠋    execute_command lscpu…" — bold TOOL CALL + status
// indicator (rotating spinner while streaming/executing, ✓/✗ when done,
// all in the muted + bold label color) in the fixed label column, then
// the tool name (bold + muted) + first input line (or the streaming
// delta preview tail, ellipsis at the line start in dim), truncated to
// fit width minus arrow. No wrapping is performed — only the first
// input line is read.
func (r *toolRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	if styles == nil {
		return "", 1
	}
	// UF-only windows (no tool name) render like plain text: no label,
	// so the whole summary uses the muted color. An over-long first line
	// keeps its tail with a leading "…" (dim) like every other summary.
	if r.isUF && r.name == "" {
		return renderUFOnlyCollapsed(r, width, styles), 1
	}

	labelStyle := lineStyleForTag(r.Tag(), styles)
	dot, dotStyle := r.status.statusDot(labelStyle)

	inputFirst, inputFirstHasEllipsis := r.toolCollapsedInput(width, dot)

	labelPart := padLabel(toolLabelWithIndicator(dot))
	line := labelPart
	if r.name != "" {
		line += r.name
	}
	if inputFirst != "" {
		line += " " + inputFirst
	}
	line = truncateWithSuffix(line, max(0, width-collapsedPrefixWidth))

	return renderToolCollapsedLine(line, labelStyle, dotStyle, dot, r.name, inputFirstHasEllipsis, styles), 1
}

// renderUFOnlyCollapsed renders the collapsed view for UF-only tool
// windows (no tool name, no AF frame): plain muted text with head+tail
// truncation (same as text windows) so the user sees both the topic and
// the latest content.
func renderUFOnlyCollapsed(r *toolRenderer, width int, styles *Styles) string {
	first := firstLine(prepareContent(r.output))
	head, tail, truncated := headAndTailParts(priceSummary(first), max(0, width-collapsedPrefixWidth))
	var sb strings.Builder
	if truncated && tail != "" {
		sb.WriteString(styles.System.Render(head))
		sb.WriteString(styles.Status.Render("…"))
		sb.WriteString(styles.System.Render(tail))
		return sb.String()
	}
	sb.WriteString(styles.System.Render(head))
	return sb.String()
}

// toolCollapsedInput returns the input portion that follows the tool
// name in the collapsed view (either the streaming delta preview tail
// or the FIRST LINE of the completed input — a tool's argument block can be
// long, so its first line is the whole of what a one-row summary can promise,
// and that is the rule for every tool). The second return value is true when
// the input was truncated and has a leading "…" marker.
//
// The first line is also why a line a handler put BELOW the first — an
// execute_command workdir annotation — is not in the folded row at all: it is
// shown when the window is expanded, the way edit_file shows its diff rows.
func (r *toolRenderer) toolCollapsedInput(width int, dot string) (string, bool) {
	if r.deltaBuffer != "" {
		// Streaming delta preview: keep the LATEST chunk's tail (new JSON
		// arrives at the tail) with the ellipsis at the line START, exactly
		// like the collapsed text windows — the tail shows what just
		// arrived, "…" marks the truncated head (rendered dim). Room is
		// everything after the label column + tool name + separator space.
		prefix := padLabel(toolLabelWithIndicator(dot))
		if r.name != "" {
			prefix += r.name + " "
		}
		room := max(0, width-collapsedPrefixWidth-cellWidth(prefix))
		tail, hasEllipsis := tailParts(flattenDelta(r.deltaBuffer), room-1)
		if hasEllipsis {
			return "…" + tail, true
		}
		return tail, false
	}
	if r.input == "" {
		return "", false
	}
	inputFirst := firstLine(prepareContent(r.input))
	// The input's first line is usually "name: args". The tool name is
	// shown right after the label column, so strip the repeated prefix
	// ("TOOL CALL ⠋ execute_command lscpu", not "execute_command:
	// execute_command: lscpu").
	if r.name != "" {
		if stripped, ok := strings.CutPrefix(inputFirst, r.name+":"); ok {
			inputFirst = strings.TrimSpace(stripped)
		}
	}
	return inputFirst, false
}

// renderToolCollapsedLine applies per-segment styling to a tool window's
// collapsed line. The window line's own chrome — the "TOOL CALL" label and
// the status indicator — takes labelStyle, the single style the whole line
// is drawn in (see lineStyleForTag); the separator space and the
// label-column padding are plain (they are spaces); the tool name takes
// toolNameStyle and everything after it is content and takes the muted
// content color (with the "…" marker dim when inputFirstHasEllipsis is
// true).
//
// The name is the one glyph on the row that is not the line's style, and it
// has to be the same style the expanded row paints it with: see
// toolNameStyle.
//
// Every offset here is a position in the line as BuildCollapsed BUILT it, and
// the line it receives has been through truncateWithSuffix since — so each one
// goes through cut (runeBoundary) before it is used, and the name segment is
// measured against the line rather than against `name`, which the truncation
// may have shortened (a 32-column pane does it to "execute_command"). See
// runeBoundary for what an unaligned offset costs.
func renderToolCollapsedLine(
	line string,
	labelStyle, dotStyle Style,
	dot, name string,
	inputFirstHasEllipsis bool,
	styles *Styles,
) string {
	toolLen := len(toolHeaderLabel)
	sepLen := len(toolLabelSep)
	dotLen := len(dot)
	contentStart := len(padLabel(toolLabelWithIndicator(dot)))

	// cut turns a built-position into a usable one.
	cut := func(i int) int { return runeBoundary(line, i) }

	labelEnd := cut(toolLen)
	if labelEnd == len(line) {
		return labelStyle.Render(line)
	}
	var sb strings.Builder
	sb.WriteString(labelStyle.Render(line[:labelEnd]))

	// Separator space between the label and the indicator (plain, part of
	// the fixed label column) — only when it survived truncation.
	sepEnd := cut(toolLen + sepLen)
	sb.WriteString(line[labelEnd:sepEnd])

	// Status indicator — the label color (muted + bold), so it visually
	// joins the "TOOL CALL" label.
	dotEnd := cut(toolLen + sepLen + dotLen)
	if dotEnd > sepEnd {
		sb.WriteString(dotStyle.Render(line[sepEnd:dotEnd]))
	}
	if dotEnd == len(line) {
		return sb.String()
	}

	// Label column padding (plain spaces) + the tool name. The name takes
	// toolNameStyle — the same style the expanded line paints it with — so
	// that folding a window does not repaint it; the padding around it is
	// part of the label column and moves with the line.
	padEnd := cut(contentStart)
	sb.WriteString(line[dotEnd:padEnd])

	// What follows the label column is the name only for as far as the name
	// actually survived: the truncation can cut the name itself ("execute_
	// comma…" where `name` still says "execute_command"), so the segment is
	// measured against the line rather than against `name`.
	nameEnd := cut(padEnd + sharedPrefixLen(line[padEnd:], name))
	if nameEnd > padEnd {
		sb.WriteString(toolNameStyle(styles).Render(line[padEnd:nameEnd]))
	}

	// When the inputFirst delta was truncated, the leading "…" in the
	// content area gets the dim color (styles.Status) instead of the muted
	// ToolContent. Position: right after the name + space. The layout is
	// checked rather than assumed — a name the truncation cut leaves the
	// marker somewhere else, and styling from a guessed offset is what
	// split the rune.
	if inputFirstHasEllipsis && nameEnd+1+len("…") <= len(line) &&
		line[nameEnd] == ' ' && strings.HasPrefix(line[nameEnd+1:], "…") {
		sb.WriteString(line[nameEnd : nameEnd+1]) // space
		sb.WriteString(styles.Status.Render(line[nameEnd+1 : nameEnd+1+len("…")]))
		sb.WriteString(styles.ToolContent.Render(line[nameEnd+1+len("…"):]))
		return sb.String()
	}
	if len(line) > nameEnd {
		sb.WriteString(styles.ToolContent.Render(line[nameEnd:]))
	}
	return sb.String()
}

// sharedPrefixLen returns the count of leading bytes s and prefix have in
// common, rounded down to a rune boundary of s — so a caller can slice
// s[:sharedPrefixLen(s, prefix)] and get whole characters.
func sharedPrefixLen(s, prefix string) int {
	n := 0
	for n < len(s) && n < len(prefix) && s[n] == prefix[n] {
		n++
	}
	return runeBoundary(s, n)
}

// previewOutput renders the Uf preview snapshot (Pending status) as a
// single line filling the window width, mirroring how Af previews fill
// the window. When the snapshot does not fit, the LATEST part of the
// output is kept with a leading "…" (never a trailing ellipsis) —
// consistent with the delta previews. The truncation marker is
// rendered dim (styles.Status) so it's visually distinct from the
// actual content. Returns "" when the output is authoritative (UF has
// arrived) or empty, so callers fall through to full multiline
// rendering.
func (r *toolRenderer) previewOutput(innerWidth int, styles *Styles) string {
	if r.status != ToolStatusPending || r.output == "" {
		return ""
	}
	// Flatten to a single line.
	out := strings.ReplaceAll(r.output, "\n", " ")
	out = strings.ReplaceAll(out, "\r", "")
	// Expand tabs BEFORE truncation so width accounting matches the final
	// render (expandTabs → TabWidth columns): the width table counts a tab
	// as 0 cells, so truncating raw tabs would let the expanded preview
	// overflow the window and soft-wrap at the terminal.
	out = expandTabs(out)
	// Tail kept with a leading ellipsis (dim) when it does not fit.
	tail, truncated := tailParts(out, max(0, innerWidth-1))
	if !truncated {
		return tail
	}
	return styles.Status.Render("…") + tail
}

// defaultToolRender renders a tool call's input as a muted argument block:
// no status indicator (it lives in the header line's TOOL CALL ⠋) and no
// "name: " prefix (the tool name lives in the header line too).
//
// What it strips is exactly the framing FormatCall added — "<name>:", one
// space, and the trailing newline — and not a byte more. TrimSpace would also
// eat whitespace the handler meant to keep, and with it the block's line
// structure: execute_command's annotations are read positionally (a line after
// the first, see renderToolArgLine), so a call whose command is empty would lose
// the line its annotation sits on and stop being drawn as an annotation at all.
func defaultToolRender(input, name string) string {
	content := prepareContent(input)
	if name != "" {
		if stripped, ok := strings.CutPrefix(content, name+":"); ok {
			content = strings.TrimSuffix(strings.TrimPrefix(stripped, " "), "\n")
		}
	}
	return content
}

// renderToolArgLine renders a tool call's argument block for the expanded window
// body. content is the block as defaultToolRender produced it.
//
// For execute_command the block's first line is the command and any line after
// it is an annotation the handler appended — the workdir (see dirMarker), which
// is harness note rather than command. Those lines are drawn in bold, the
// channel this UI uses to mark a position without spending a color (the tool
// name on the line above does the same), so they cannot be read as more of the
// command. Everything on the first line stays plain body text.
//
// Which lines those are needs no matching: an execute_command command cannot
// contain a line break, so a block with a second line can only have got it from
// the handler. Reading the block's shape here — rather than carrying the
// annotation alongside it — is what edit_file's rows do too: the text is the
// encoding, the renderer that draws it is the one that reads it, and the tool
// name says which encoding to expect, so write_file's literal "[dir=…]" is not
// mistaken for one.
//
// Both parts go through styles.Body so the block dims as a unit under an
// overlay: styleBodyLines leaves a row that already carries SGR alone, which
// the annotation's row does. Under Dimmed() Body resolves to ColorDim and keeps
// the bold, so the annotation still stands out from the command it annotates.
func renderToolArgLine(name, content string, styles *Styles) string {
	if name != "execute_command" {
		return content
	}
	command, annotation, ok := strings.Cut(content, "\n")
	if !ok {
		return content
	}
	return styles.Body.Render(command) + "\n" + styles.Body.Bold(true).Render(annotation)
}
