package terminal

// Window renderers: type-specific content management and rendering.
//
// Each renderer implements WindowRendering and owns its content storage
// and caching. The Window struct delegates to the renderer for everything
// that varies by window type.

import (
	"strings"

	ansi "github.com/charmbracelet/x/ansi"

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

	// Cached wrapped lines for fast incremental update via
	// appendDeltaToVisualLines. Populated by BuildInner, updated
	// incrementally by AppendFromTLV. Each row carries a continuation
	// mark (visualLine.Cont) — rows of the same original line join
	// without '\n' (terminal soft-wrap); different original lines are
	// separated by hard '\n'.
	wrappedLines []visualLine
	cacheWidth   int  // inner width used for wrapping (0 = unknown)
	cacheValid   bool // true = BuildInner can skip full re-wrap
}

func (r *textRenderer) Tag() string { return r.tag }

func (r *textRenderer) ToolInfo() *ToolInfo { return nil }

func (r *textRenderer) AppendFromTLV(_ string, value string) {
	r.contentParts = append(r.contentParts, value)
	r.contentLen += len(value)

	// Incremental update: append the delta to wrappedLines as PLAIN TEXT.
	// Streaming content deliberately carries no styling (markdown rendering
	// is a future concern), which keeps the incremental path simple:
	// pure-text hardwrap, no ANSI handling, no style state to maintain.
	if len(r.wrappedLines) > 0 && r.cacheWidth > 0 {
		r.wrappedLines = appendDeltaToVisualLines(r.wrappedLines, prepareContent(value), r.cacheWidth)
		// cacheValid stays false — border cache needs rebuild,
		// but wrappedLines is current for TryLineCount.
	} else {
		r.cacheValid = false
	}
}

func (r *textRenderer) Invalidate() {
	r.cacheValid = false
	r.wrappedLines = nil
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
// without styling: assistant text and reasoning are streaming content and
// deliberately carry no color/weight (markdown rendering is a future
// concern). System messages (SN/SE) keep their System/Error colors.
func (r *textRenderer) plainContent() bool {
	return r.tag == tlv.TagAssistantT || r.tag == tlv.TagAssistantR
}

// BuildInner returns the inner content as visual lines.
// For AT/AR this is PLAIN TEXT (no styling — markdown rendering is a
// future concern), so only wrapping is applied. System messages (SN/SE)
// keep their Error/System colors. Each returned line is one terminal
// row (no '\n' inside) with a continuation mark: rows of the same
// original line join without '\n' (soft wrap); rows starting a new
// original line are separated by hard '\n'. lineCount includes the 2
// box rules (len(lines) + 2); Window.Render adds the header.
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
		return r.wrappedLines, len(r.wrappedLines) + 2
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

	content := prepareContent(r.content)
	if !r.plainContent() && styles != nil {
		switch r.tag {
		case TagWindowSE:
			content = styleMultiline(content, styles.Error)
		case TagWindowSN:
			content = styleMultiline(content, styles.System)
		}
	}

	r.wrappedLines = wrapVisualLines(content, innerWidth)
	r.cacheWidth = innerWidth
	r.cacheValid = true

	return r.wrappedLines, len(r.wrappedLines) + 2
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
// stays plain text (markdown rendering is a future concern). SN/SE:
// label and content keep their System/Error colors.
func (r *textRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	content := prepareContent(r.rawContent())
	label := labelForTag(r.tag)
	line := ""
	if label != "" {
		line = padLabel(label)
	}
	line += tailSummary(content, max(0, width-2-CollapsedLabelWidth))
	line = truncateWithSuffix(line, max(0, width-2)) // safety net

	if label == "" {
		if styles == nil {
			return line, 1
		}
		return styles.System.Render(line), 1
	}
	// Style only the label (its full padded width); the content summary
	// is muted (uniform with every other collapsed window type).
	labelPart := padLabel(label)
	styledLabel := labelStyleForTag(r.tag, styles).Render(line[:min(len(labelPart), len(line))])
	if len(line) <= len(labelPart) {
		return styledLabel, 1
	}
	rest := line[len(labelPart):]
	if styles == nil {
		return styledLabel + rest, 1
	}
	return styledLabel + styles.System.Render(rest), 1
}

// renderCollapsedLine styles a truncated collapsed line: the label part
// in its type style, the content summary in the muted color. Used by
// renderers that still style their content (e.g. user messages). If the
// line was truncated inside the label (very narrow terminal) the whole
// line uses the label style; without a label the whole line is muted.
func renderCollapsedLine(line, label string, labelStyle, mutedStyle Style) string {
	if label == "" {
		return mutedStyle.Render(line)
	}
	if len(line) <= len(label) {
		return labelStyle.Render(line)
	}
	return labelStyle.Render(line[:len(label)]) + mutedStyle.Render(line[len(label):])
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
		return "NOTIFY"
	case TagWindowSE:
		return "ERROR"
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
func tailSummary(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	// Escape newlines first so the result is a single logical line.
	escaped := strings.ReplaceAll(content, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "")

	if ansi.StringWidth(escaped) <= maxWidth {
		return escaped
	}
	// Take the tail that fits, leaving 1 column for the leading "…".
	room := maxWidth - 1
	runes := []rune(escaped)
	width := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		w := ansi.StringWidth(string(runes[i]))
		if width+w > room {
			break
		}
		width += w
		start = i
	}
	return "…" + string(runes[start:])
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
	w := ansi.StringWidth(label)
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
// separated with "---" in System color. Each returned line is one
// terminal row (no '\n' inside); lineCount includes the 2 box rules.
func (r *userRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	var parts []string

	// Media portion — rendered first (on top)
	if len(r.mediaParts) > 0 {
		mediaBlockStr := wrapLabels(r.mediaParts, innerWidth, styles.Attachment)
		parts = append(parts, mediaBlockStr)
	}

	// Text portion: text parts separated by "---"
	if len(r.textParts) > 0 {
		var textBlock strings.Builder

		// Separate from media with "---"
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
			textBlock.WriteString(trimmed) // user text is plain (no bold/color)
			firstText = false
		}

		if textBlock.Len() > 0 {
			styledText := textBlock.String()
			if innerWidth > 0 {
				styledText = wrapContent(styledText, innerWidth)
			}
			parts = append(parts, styledText)
		}
	}

	result := strings.Join(parts, "\n")

	// Wrap into visual rows with continuation marks: rows of the same
	// original line join without '\n' (soft wrap); rows starting a new
	// original line are separated by hard '\n'.
	lines := wrapVisualLines(result, innerWidth)

	// Count lines (add 2 for border)
	return lines, len(lines) + 2
}

// BuildCollapsed returns the single-line collapsed form:
// "USER PROMPT …first-text-line-tail", truncated to fit width minus the
// arrow. Like every other collapsed summary, an over-long first line
// keeps its LATEST part with a leading "…" — never a trailing ellipsis.
func (r *userRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	first := ""
	if len(r.textParts) > 0 {
		first = firstLine(strings.TrimSpace(r.textParts[0]))
	}
	if first == "" && len(r.mediaParts) > 0 {
		first = r.mediaParts[0]
	}
	if first == "" {
		return "", 1
	}
	label := padLabel("USER PROMPT")
	room := max(0, width-2-ansi.StringWidth(label))
	line := label + tailSummary(first, room)
	line = truncateWithSuffix(line, max(0, width-2)) // safety net
	return renderCollapsedLine(line, padLabel("USER PROMPT"), labelStyleForTag(tlv.TagUserT, styles), styles.System), 1
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
		if p := r.previewOutput(innerWidth); p != "" {
			// Uf preview snapshot — single line, truncated.
			output = p
		}
		styled := prepareContent(output)
		lines := wrapVisualLines(styled, innerWidth)
		return lines, len(lines) + 2
	}

	// Input: streaming delta preview (truncated JSON) or the full input.
	// Neither carries the status indicator nor the "name: " prefix — the
	// indicator lives in the header line (TOOLUSE ⠋) and the tool name is
	// shown there too. Content is plain (no color, no bold); only the
	// "---" separator keeps its muted color.
	var call string
	if r.deltaBuffer != "" {
		// Arguments still streaming in — one-line preview showing the
		// LATEST chunk: like every other collapsed/delta summary, keep the
		// tail of the delta (new JSON arrives at the tail) and mark the
		// truncated head with a leading "…" — never a trailing ellipsis.
		deltaContent := flattenDelta(r.deltaBuffer)
		deltaContent = tailSummary(deltaContent, max(0, innerWidth))
		call = deltaContent
	} else {
		switch r.name {
		case "edit_file":
			// edit_file's input is a real diff (the model emits -/+
			// prefixed rows) — colored per row by RenderDiffContent.
			call = RenderDiffContent(r.input, r.name, styles)
		default:
			// Everything else — including write_file, whose input is the
			// RAW file content being written (not a diff; - / + prefixed
			// lines there are literal content and must stay plain).
			call = defaultToolRender(r.input, r.name)
		}
	}

	// Append output with a "---" separator — uniform across all tools
	// (parameters/results divider; edit_file and write_file included).
	// Output is plain text; the separator keeps its muted color.
	if r.output != "" {
		output := r.output
		if p := r.previewOutput(innerWidth); p != "" {
			// Uf preview snapshot — single line, truncated.
			output = p
		}
		sep := styles.System.Render(Separator)
		styled := prepareContent(output)
		call = call + "\n" + sep + "\n" + styled
	}

	lines := wrapVisualLines(call, innerWidth)
	return lines, len(lines) + 2
}

// BuildCollapsed returns the single-line collapsed form for tool windows:
// "TOOLUSE ⠋    execute_command lscpu…" — bold TOOLUSE + status indicator
// (rotating spinner while streaming/executing, plain ✓/✗ when done) in
// the fixed label column, then the tool name + first input line (or the
// streaming delta preview tail, ellipsis at the line start), truncated to
// fit width minus arrow. No wrapping is performed — only the first input
// line is read.
func (r *toolRenderer) BuildCollapsed(width int, styles *Styles) (string, int) {
	if styles == nil {
		return "", 1
	}
	// UF-only windows (no tool name) render like plain text: no label,
	// so the whole summary uses the muted color. An over-long first line
	// keeps its tail with a leading "…" like every other summary.
	if r.isUF && r.name == "" {
		first := firstLine(prepareContent(r.output))
		first = tailSummary(first, max(0, width-2))
		return styles.System.Render(first), 1
	}

	dot, dotStyle := r.status.statusDot()

	var inputFirst string
	if r.deltaBuffer != "" {
		// Streaming delta preview: keep the LATEST chunk's tail (new JSON
		// arrives at the tail) with the ellipsis at the line START, exactly
		// like the collapsed text windows — the tail shows what just
		// arrived, "…" marks the truncated head. Room is everything after
		// the label column + tool name + separator space.
		prefix := padLabel(toolLabelWithIndicator(dot))
		if r.name != "" {
			prefix += r.name + " "
		}
		room := max(0, width-2-ansi.StringWidth(prefix))
		inputFirst = tailSummary(flattenDelta(r.deltaBuffer), room)
	} else if r.input != "" {
		inputFirst = firstLine(prepareContent(r.input))
		// The input's first line is usually "name: args". The tool name is
		// shown right after the label column, so strip the repeated prefix
		// ("TOOLUSE ⠋ execute_command lscpu", not "execute_command:
		// execute_command: lscpu").
		if r.name != "" {
			if stripped, ok := strings.CutPrefix(inputFirst, r.name+":"); ok {
				inputFirst = strings.TrimSpace(stripped)
			}
		}
	}

	labelPart := padLabel(toolLabelWithIndicator(dot))
	line := labelPart
	if r.name != "" {
		line += r.name
	}
	if inputFirst != "" {
		line += " " + inputFirst
	}
	line = truncateWithSuffix(line, max(0, width-2))

	// Re-style the truncated plain line: "TOOLUSE" in bold (no color), the
	// separator space plain, the status indicator (spinner or ✓/✗)
	// unstyled — deliberately colorless — then name + input in muted.
	// NOTE: the indicator is multi-byte UTF-8 — slice by len(dot), never by
	// byte 1, and labelPart length is in bytes (padLabel pads to display
	// columns).
	labelStyle := labelStyleForTag(r.Tag(), styles)
	toolLen := len(toolHeaderLabel)
	sepLen := len(toolLabelSep)
	dotLen := len(dot)
	contentStart := len(labelPart)
	if len(line) <= toolLen {
		return labelStyle.Render(line), 1
	}
	var sb strings.Builder
	sb.WriteString(labelStyle.Render(line[:toolLen]))
	// Separator space between the label and the indicator (plain, part of
	// the fixed label column) — only when it survived truncation.
	if len(line) > toolLen {
		sb.WriteString(line[toolLen:min(len(line), toolLen+sepLen)])
	}
	// Status indicator — unstyled (colorless), multi-byte safe.
	if len(line) > toolLen+sepLen {
		sb.WriteString(dotStyle.Render(line[toolLen+sepLen : min(len(line), toolLen+sepLen+dotLen)]))
	}
	if len(line) > toolLen+sepLen+dotLen {
		// Label column padding (plain spaces) + content (muted).
		paddingEnd := min(len(line), contentStart)
		sb.WriteString(line[toolLen+sepLen+dotLen : paddingEnd])
		sb.WriteString(styles.ToolContent.Render(line[paddingEnd:]))
	}
	return sb.String(), 1
}

// previewOutput renders the Uf preview snapshot (Pending status) as a
// single line filling the window width, mirroring how Af previews fill
// the window. When the snapshot does not fit, the LATEST part of the
// output is kept with a leading "…" (never a trailing ellipsis) —
// consistent with the delta previews. Returns "" when the output is
// authoritative (UF has arrived) or empty, so callers fall through to
// full multiline rendering.
func (r *toolRenderer) previewOutput(innerWidth int) string {
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
	// Fill the width; tail kept with a leading ellipsis when it does not
	// fit.
	return tailSummary(out, max(0, innerWidth))
}

// defaultToolRender renders a tool call's input as a muted argument block:
// no status indicator (it lives in the header line's TOOLUSE ⠋) and no
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
