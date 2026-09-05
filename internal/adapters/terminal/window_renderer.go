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
	mdMode       bool     // render markdown (toggled with 'r'; AT/AR only)

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
	r.contentParts = append(r.contentParts, value)
	r.contentLen += len(value)
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
		// cacheValid stays false — border cache needs rebuild,
		// but wrappedLines is current for TryLineCount.
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
		colored[i] = visualLine{Text: styles.Body.Render(l.Text), Cont: l.Cont}
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
		out[i] = visualLine{Text: styles.Body.Render(l.Text), Cont: l.Cont}
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

// rawContent returns the full accumulated content for testing.
func (r *textRenderer) rawContent() string {
	if len(r.contentParts) > 0 {
		var buf strings.Builder
		buf.WriteString(r.content)
		for _, p := range r.contentParts {
			buf.WriteString(p)
		}
		return buf.String()
	}
	return r.content
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
// separated by hard '\n'. lineCount includes the 2 box rules
// (len(lines) + 2); Window.Render adds the header.
func (r *textRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	// Fast path: use cached wrapped lines if width matches.
	// wrappedLines is kept current by AppendFromTLV's incremental path.
	if r.cacheWidth == innerWidth && len(r.wrappedLines) > 0 {
		// Still merge contentParts for eventual consistency (resize, slow path).
		// This prevents unbounded growth during long streaming sessions.
		if len(r.contentParts) > 0 {
			var buf strings.Builder
			buf.WriteString(r.content)
			for _, part := range r.contentParts {
				buf.WriteString(part)
			}
			r.content = buf.String()
			r.contentParts = nil
		}
		return r.bodyStyled(r.wrappedLines, styles), len(r.wrappedLines) + 2
	}

	// Full render: prepare, style (system messages only), and wrap
	// Ensure full content from parts
	if len(r.contentParts) > 0 {
		var buf strings.Builder
		buf.WriteString(r.content)
		for _, part := range r.contentParts {
			buf.WriteString(part)
		}
		r.content = buf.String()
		r.contentParts = nil
	}

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

	return r.bodyStyled(r.wrappedLines, styles), len(r.wrappedLines) + 2
}

// BuildCollapsed returns the single-line collapsed form:
// "REASONING …latest content" (label + trailing content), truncated to
// fit width minus the arrow.
//
// The summary shows the LATEST characters of the content (not the first
// line), so streaming deltas are visible in the collapsed list — new
// input arrives at the tail. Newlines are escaped as the literal "\n"
// to preserve the single-line invariant (line heights count '\n'). A
// leading "…" marks truncation when the content is wider than the box.
//
// AT/AR: the label is styled (bold + muted) and the content summary is
// muted — the collapsed header is UI chrome, while the expanded body
// stays plain text in normal mode (dimmed Body color under overlays).
// The collapsed preview always shows the RAW content
// (never the markdown table transform — the preview is one line and
// markdown state only affects expanded rendering). All non-delta text
// uses head+tail (so the user sees both the topic and the latest
// content); the only leading "…" is reserved for streaming delta
// content, which lives in toolRenderer.
//
// Truncation markers ("…") are rendered with styles.Status (the dim
// color) to visually separate them from the actual content — content
// uses the muted foreground, the marker uses the lighter dim
// foreground. This applies to both the middle "…" in head+tail and
// the leading "…" in tail-only summaries.
func (r *textRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	content := prepareContent(r.rawContent())
	label := labelForTag(r.tag)
	line := ""
	if label != "" {
		line = padLabel(label)
	}
	summaryWidth := max(0, width-collapsedPrefixWidth-CollapsedLabelWidth)
	summary, ellipsisOffset := r.collapsedSummary(content, summaryWidth)
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
func (r *textRenderer) collapsedSummary(content string, summaryWidth int) (string, int) {
	head, tail, truncated := headAndTailParts(content, summaryWidth)
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
	labelEnd := min(len(labelPart), len(line))
	styledLabel := labelStyleForTag(tag, styles).Render(line[:labelEnd])
	if len(line) <= labelEnd {
		return styledLabel
	}
	if styles == nil {
		return styledLabel + line[labelEnd:]
	}
	content := line[labelEnd:]
	if ellipsisOffset < 0 {
		return styledLabel + styles.System.Render(content)
	}
	// Content = padding + head + "…" + tail. Re-split at the marker
	// (which sits at byte offset ellipsisOffset within content, since the
	// summary's ellipsisOffset was computed against the post-label string).
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
// The count includes 2 border lines + 1 header line (expanded form).
func (r *textRenderer) TryLineCount(width int) (int, bool) {
	innerWidth := max(0, width)
	if len(r.wrappedLines) > 0 && r.cacheWidth == innerWidth {
		return len(r.wrappedLines) + 3, true
	}
	return 0, false
}

// tailSummary renders the trailing part of content for a collapsed window:
// the latest maxWidth display columns, with newlines escaped as the literal
// two-character "\n" (a collapsed line must stay one visual line — line
// heights are counted by '\n'). When the content is wider than maxWidth a
// leading "…" marks the truncation. Multi-byte safe (rune + display width).
//
// The truncation marker ("…") is *not* part of the returned string when
// styled — callers that want to dim the marker should use tailParts and
// render the marker themselves.
func tailSummary(content string, maxWidth int) string {
	// Reserve 1 column for the leading "…" when truncated.
	tail, truncated := tailParts(content, maxWidth-1)
	if !truncated {
		return tail
	}
	return "…" + tail
}

// tailParts is the structured form of tailSummary: returns the tail
// content (no leading "…") and a flag indicating whether truncation
// occurred. Callers that need to style the "…" marker differently from
// the content (e.g. dim vs muted) should use this so they can render
// each piece with its own Style.
//
// When maxWidth <= 1, returns ("", false) — there's no room for content
// even without a marker.
func tailParts(content string, maxWidth int) (string, bool) {
	if maxWidth <= 1 {
		return "", false
	}
	// Escape newlines first so the result is a single logical line.
	escaped := strings.ReplaceAll(content, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "")

	if cellWidth(escaped) <= maxWidth {
		return escaped, false
	}
	// Take the tail that fits. We deliberately use the FULL maxWidth
	// here (not maxWidth-1) — callers that prepend a "…" marker should
	// subtract 1 from their own budget before calling, since we don't
	// know if they want a marker at all. tailSummary does that adjustment.
	room := maxWidth
	runes := []rune(escaped)
	width := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		w := cellWidth(string(runes[i]))
		if width+w > room {
			break
		}
		width += w
		start = i
	}
	return string(runes[start:]), true
}

// headAndTailSummary renders the leading AND trailing parts of content for
// a collapsed text window (REASONING / ASSISTANT / USER PROMPT):
//
//	first ~40% of maxWidth cols  +  "…"  +  last ~60% of maxWidth cols
//
// Split rationale: the head conveys the topic of the message
// ("Here's how to…", "The user is asking about…"), the tail conveys the
// actual content / punchline. For long completed text both halves are
// useful; for streaming-tail content (tool deltas, UF snapshots,
// attachments) use tailSummary instead — there the latest content is the
// only signal that matters.
//
// Layout (maxWidth cols total):
//   - maxWidth <= 0  : return ""
//   - maxWidth <= 2  : render the head only (no room for "…" + tail)
//   - maxWidth >= 3  : head + "…" + tail, ~40% / ~60% (integer math),
//     with a hard floor of 1 col on head so we always emit something,
//     and a floor of 1 col on tail (if head already claims the width,
//     fall back to "head only").
//   - if the full content already fits maxWidth cols, return as-is.
//
// Grapheme-cluster-aware: head and tail are bounded by grapheme cluster
// boundaries (not runes) via takeCells/tailCells, so multi-codepoint
// clusters like ZWJ emoji, combining marks, and variation selectors are
// never split mid-cluster — unlike the per-rune tailSummary above, which
// is fine for CJK and BMP but can chop multi-rune clusters. The budget and
// the cut come from the same width table (width.go).
//
// The "…" marker is *not* part of the returned string when styled —
// callers that want to dim the marker should use headAndTailParts and
// render the marker themselves.
func headAndTailSummary(content string, maxWidth int) string {
	head, tail, truncated := headAndTailParts(content, maxWidth)
	switch {
	case !truncated:
		return head
	case tail == "":
		// Narrow widths: head only (no room for ellipsis + tail).
		return head
	default:
		return head + "…" + tail
	}
}

// headAndTailParts is the structured form of headAndTailSummary: returns
// the head and tail portions separately, and a flag indicating whether
// the content was truncated. The middle "…" marker is *not* included in
// either — callers render it themselves with their own Style.
//
// When truncated is false, head is the full content and tail is "".
// When truncated is true and tail is "", the function fell back to head-
// only (very narrow widths where there's no room for ellipsis + tail).
func headAndTailParts(content string, maxWidth int) (head, tail string, truncated bool) {
	if maxWidth <= 0 {
		return "", "", false
	}
	escaped := strings.ReplaceAll(content, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "")

	if cellWidth(escaped) <= maxWidth {
		return escaped, "", false
	}
	if maxWidth <= 2 {
		return takeCells(escaped, maxWidth), "", true
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
		return takeCells(escaped, maxWidth), "", true
	}
	return takeCells(escaped, headWidth), tailCells(escaped, tailWidth), true
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
// labels may contain multi-byte glyphs such as the status dot.
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
// TabWidth columns; ansi.Hardwrap counts a tab as 0 width).
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
// order: media parts precede the text part. Multiple text parts are
// separated with "───" (Separator) in System color. Each returned line is one
// terminal row (no '\n' inside); lineCount includes the 2 box rules.
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

		// Separate from media with Separator ("───")
		if len(r.mediaParts) > 0 {
			textBlock.WriteString(styles.System.Render(Separator))
			textBlock.WriteString("\n")
		}

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
	return styleBodyLines(lines, styles), len(lines) + 2
}

// BuildCollapsed returns the single-line collapsed form:
// "USER PROMPT media-summary …content-tail", truncated to fit width minus
// the arrow. Media badges always come first and therefore remain visible
// even when the text is long. A media-only message shows all attachment
// types and their counts in the available width.
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
	head, tail, truncated := headAndTailParts(content, room)

	plainLine := label + head
	if tail != "" {
		plainLine += "…" + tail
	}
	line := truncateWithSuffix(plainLine, max(0, width-collapsedPrefixWidth)) // safety net

	// Render with styling: label muted+bold, "…" dim, content muted.
	labelStyle := labelStyleForTag(tlv.TagUserT, styles)
	if len(line) <= len(label) {
		return labelStyle.Render(line), 1
	}
	var rendered strings.Builder
	rendered.WriteString(labelStyle.Render(line[:len(label)]))
	rest := line[len(label):]
	if !truncated || tail == "" {
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String(), 1
	}
	// head + "…" + tail. The "…" might have been cut by truncateWithSuffix.
	headEnd := len(label) + len(head)
	if headEnd > len(line) {
		// head was truncated mid-way — fall back to muted
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String(), 1
	}
	rendered.WriteString(styles.System.Render(line[len(label):headEnd]))
	if headEnd+len("…") <= len(line) {
		rendered.WriteString(styles.Status.Render(line[headEnd : headEnd+len("…")]))
		if headEnd+len("…") < len(line) {
			rendered.WriteString(styles.System.Render(line[headEnd+len("…"):]))
		}
	} else {
		// "…" was cut — render the rest as muted
		rendered.WriteString(styles.System.Render(line[headEnd:]))
	}
	return rendered.String(), 1
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

func (r *toolRenderer) AppendFromTLV(_ string, value string) {
	// Tool data normally arrives via structured setters (HandleToolInput/HandleToolOutput).
	// For replayed content or direct testing, dispatch by window type.
	if r.isUF {
		r.output = value
	} else {
		r.input = value
	}
}

func (r *toolRenderer) Invalidate() {}

// AppendDelta sets the latest partial JSON chunk for one-line preview.
// Each call replaces the previous delta — the window shows only the
// most recently received chunk.
func (r *toolRenderer) AppendDelta(delta string) {
	r.deltaBuffer = delta
}

// BuildInner renders the tool window content as visual lines. Each
// returned line is one terminal row (no '\n' inside); lineCount
// includes the 2 box rules.
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
		return styleBodyLines(lines, styles), len(lines) + 2
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
			call = defaultToolRender(r.input, r.name)
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
	return styleBodyLines(lines, styles), len(lines) + 2
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

	labelStyle := labelStyleForTag(r.Tag(), styles)
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
	head, tail, truncated := headAndTailParts(first, max(0, width-collapsedPrefixWidth))
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
// or the first line of the completed input). The second return value
// is true when the input was truncated and has a leading "…" marker.
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
// collapsed line: label + status indicator in the label color (muted +
// bold), separator plain, label-column padding plain, tool name bold +
// muted, and the input tail muted (with the "…" marker dim when
// inputFirstHasEllipsis is true).
//
// The indicator is multi-byte UTF-8 — slice by len(dot), never by byte 1.
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
	if len(line) <= toolLen {
		return labelStyle.Render(line)
	}
	var sb strings.Builder
	sb.WriteString(labelStyle.Render(line[:toolLen]))
	// Separator space between the label and the indicator (plain, part
	// of the fixed label column) — only when it survived truncation.
	if len(line) > toolLen {
		sb.WriteString(line[toolLen:min(len(line), toolLen+sepLen)])
	}
	// Status indicator — uses the label color (muted + bold), so it
	// visually joins the "TOOL CALL" label. Multi-byte safe (slice by
	// len(dot), never by byte 1).
	if len(line) > toolLen+sepLen {
		sb.WriteString(dotStyle.Render(line[toolLen+sepLen : min(len(line), toolLen+sepLen+dotLen)]))
	}
	if len(line) <= toolLen+sepLen+dotLen {
		return sb.String()
	}
	// Label column padding (plain spaces) + content: tool name in
	// bold + muted, arguments in muted. The name's byte length is
	// bounded by what survived truncation.
	paddingEnd := min(len(line), contentStart)
	sb.WriteString(line[toolLen+sepLen+dotLen : paddingEnd])
	nameByteLen := min(len(name), max(0, len(line)-contentStart))
	nameEnd := contentStart + nameByteLen
	if nameByteLen > 0 {
		sb.WriteString(styles.ToolContent.Bold(true).Render(line[contentStart:nameEnd]))
	}
	// When the inputFirst delta was truncated, the leading "…" in the
	// content area gets the dim color (styles.Status) instead of the
	// muted ToolContent. Position: right after the name + space.
	if inputFirstHasEllipsis && len(line) > nameEnd+1+len("…") {
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
	// render (expandTabs → TabWidth columns): ansi.Hardwrap counts a tab
	// as 0 width, so truncating raw tabs would let the expanded preview
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
func defaultToolRender(input, name string) string {
	content := prepareContent(input)
	if name != "" {
		if stripped, ok := strings.CutPrefix(content, name+":"); ok {
			content = strings.TrimSpace(stripped)
		}
	}
	return content
}
