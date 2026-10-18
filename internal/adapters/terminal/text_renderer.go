package terminal

// textRenderer draws assistant text (AT), reasoning (AR), and the system messages
// (SN/SE) that take the same shape.
//
// It is the one renderer whose content can be re-read as markdown ('r' toggles
// it), which is why its line cache has two invalidation paths: a plain delta
// appends to the wrapped rows, and anything that could form or re-flow a table
// falls back to a full re-wrap. The folded row it draws is composed here
// (renderFoldedLineWithEllipsis styles each run); the arithmetic behind it is in
// folded_summary.go.

import (
	"strings"
)

// textRenderer handles simple text content with optional streaming deltas.
// Used for AT, AR, SN, and SE tags.
type textRenderer struct {
	tag          string   // frame tag that created this window
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
	// incrementally by AppendFromFrame. Each row carries a continuation
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

func (r *textRenderer) AppendFromFrame(_ string, value string) {
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
		// stripANSI only (no expandTabs here): tabs are unfolded per
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
// This is the read path, not a test helper: BuildFolded derives a folded
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

// summaryContent is the content a folded summary draws, priced.
//
// While the counts cover every byte of raw, prepareContent has nothing to do — a
// tab, a carriage return and an escape each retire them — so the content is raw
// itself and the price is the counts. Those are the passes over the message this
// exists to avoid: on 128 KB, measure is 38μs of a 46μs BuildFolded,
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
	return r.tag == tagAssistantText || r.tag == tagAssistantReasoning
}

// BuildInner returns the inner content as visual lines.
// In normal mode the content is PLAIN TEXT (no styling — markdown tables
// are padded plain text when mdMode is on), so only wrapping is applied
// and body text renders in the terminal's default color. Under an
// overlay (styles from Styles.Dimmed) bodyStyled wraps the rows in the
// dim Body color. Each returned line is one terminal row (no '\n'
// inside) with a continuation mark: rows of the same original line join
// without '\n' (soft wrap); rows starting a new original line are
// separated by hard '\n'. lineCount is the window's unfolded height
// (len(lines) + 1): the content rows plus the labeled rule that opens
// them.
func (r *textRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	// Fast path: use cached wrapped lines if width matches.
	// wrappedLines is kept current by AppendFromFrame's incremental path.
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

	// stripANSI only — tabs are unfolded per original line inside
	// wrapVisualLines so the full path matches the incremental path.
	content := stripANSI(r.content)
	if r.mdMode {
		// Markdown table transform (toggled with 'r'). Tabs inside table
		// lines are unfolded per original line by the parser itself, so
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

// BuildFolded returns the single-line folded form: the label column, then a
// summary of the content — "REASONING  the head of the message…its tail" — cut to
// fit width minus foldedPrefixWidth, the arrow and the space after it.
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
// muted — the folded header is UI chrome, while the unfolded body
// stays plain text in normal mode (dimmed Body color under overlays).
// The folded preview always shows the RAW content
// (never the markdown table transform — the preview is one line and
// markdown state only affects unfolded rendering).
//
// The marker is rendered with styles.Status (the dim color) to separate it from
// the content, which uses the muted foreground — renderFoldedLineWithEllipsis
// splits the row at the marker's offset to do it. toolRenderer dims its own
// markers, middle and leading, at the places it builds them.
func (r *textRenderer) BuildFolded(width int, styles *Styles) (string, int) {
	label := labelForTag(r.tag)
	line := ""
	if label != "" {
		line = padLabel(label)
	}
	summaryWidth := max(0, width-foldedPrefixWidth-FoldedLabelWidth)
	summary, ellipsisOffset := foldedSummary(r.summaryContent(r.rawContent()), summaryWidth)
	line += summary
	line = truncateWithSuffix(line, max(0, width-foldedPrefixWidth)) // safety net

	if label == "" {
		if styles == nil {
			return line, 1
		}
		return styles.System.Render(line), 1
	}
	return renderFoldedLineWithEllipsis(line, label, ellipsisOffset, r.tag, styles), 1
}

// renderFoldedLineWithEllipsis styles a folded line: the label
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
func renderFoldedLineWithEllipsis(line, label string, ellipsisOffset int, tag string, styles *Styles) string {
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
	case tagAssistantReasoning:
		return "REASONING"
	case tagAssistantText:
		return "ASSISTANT"
	case tagUserText:
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
// The count is the window's unfolded height: the content rows plus the one
// labeled rule that opens it.
func (r *textRenderer) TryLineCount(width int) (int, bool) {
	innerWidth := max(0, width)
	if len(r.wrappedLines) > 0 && r.cacheWidth == innerWidth {
		return len(r.wrappedLines) + 1, true
	}
	return 0, false
}
