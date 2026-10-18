package terminal

// toolRenderer draws a tool call and its result: the call's arguments as they
// stream, its output while it runs, and the settled pair.
//
// Its folded row is this renderer's alone: it is the one that can cut from the
// *front*, with a leading "…" marking a preview that shows where the output has
// got to rather than what the message is about. That is why tailParts, firstLine
// and flattenDelta live here, and not with the head+tail arithmetic the other two
// renderers share.

import (
	"strings"

	"github.com/alayacore/alayacore/internal/wire"
)

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
	// 1 from their own budget before calling (see renderUFOnlyFolded and
	// toolFoldedInput), since we don't know if they want a marker at all.
	//
	// The cut is tailCells, so it lands on a cluster boundary. It used to be a
	// backwards walk over []rune measuring one rune at a time, which split
	// multi-rune clusters: tailParts("aaaa 👨‍👩‍👧‍👦", 2) returned
	// ZWJ+boy, the back half of a family emoji, on the row a user watches while
	// a command runs. That walk also allocated a string per rune it examined.
	return cutMeasured(m, maxWidth, true), true
}

// firstLine returns the first line of s (up to the first '\n').
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// flattenDelta flattens a streaming delta to a single line, unfolding
// tabs first so width accounting matches the final render (expandTabs →
// TabWidth columns; the width table counts a tab as 0 cells).
func flattenDelta(delta string) string {
	d := strings.ReplaceAll(delta, "\n", " ")
	d = strings.ReplaceAll(d, "\r", "")
	return expandTabs(d)
}

// toolRenderer handles tool call windows that show input and optional output.
type toolRenderer struct {
	isUF      bool   // true for UF-only windows (no prior AF frame)
	name      string // tool name (e.g. "read_file")
	toolInput string // the call's arguments, formatted (complete, from AF) (complete, from AF)
	output    string // tool execution output
	status    ToolStatus

	// deltaBuffer accumulates partial JSON from Af frames during streaming.
	// Not appended to window content — rendered as a one-line preview
	// alongside the tool name in the pending state.
	deltaBuffer string
}

func (r *toolRenderer) Tag() string { return wire.TagAssistantF }

func (r *toolRenderer) ToolInfo() *ToolInfo {
	return &ToolInfo{
		Name:  r.name,
		Input: r.toolInput,
	}
}

// setName, setToolInput, setOutput and the delta writer below are the only things that
// assign a tool renderer's drawn fields, and each one repairs what it is given.
// That is what makes "a Window's content is well-formed UTF-8" hold structurally
// rather than by every caller remembering: the fields are written in four places
// and all four sanitize, so a path added later either goes through one of them or
// fails TestEveryContentIngressDrawsWellFormedUTF8.
func (r *toolRenderer) setName(s string) { r.name = sanitizeUTF8(s) }

func (r *toolRenderer) setToolInput(s string) { r.toolInput = sanitizeUTF8(s) }

func (r *toolRenderer) setOutput(s string) { r.output = sanitizeUTF8(s) }

func (r *toolRenderer) AppendFromFrame(_ string, value string) {
	// Tool data normally arrives via structured setters (HandleToolInput/HandleToolOutput).
	// For replayed content or direct testing, dispatch by window type.
	if r.isUF {
		r.setOutput(value)
	} else {
		r.setToolInput(value)
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
		// LATEST chunk: like every other folded/delta summary, keep the
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
			call = RenderDiffContent(r.toolInput, r.name, styles)
		default:
			// Everything else — including write_file, whose input is the
			// RAW file content being written (not a diff; - / + prefixed
			// lines there are literal content and must stay plain in
			// normal mode; dimmed under overlays by styleBodyLines).
			call = renderToolArgLine(r.name, defaultToolRender(r.toolInput, r.name), styles)
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

// BuildFolded returns the single-line folded form for tool windows:
// "TOOL CALL ⠋    execute_command lscpu…" — bold TOOL CALL + status
// indicator (rotating spinner while streaming/executing, ✓/✗ when done,
// all in the muted + bold label color) in the fixed label column, then
// the tool name (bold + muted) + first input line (or the streaming
// delta preview tail, ellipsis at the line start in dim), truncated to
// fit width minus arrow. No wrapping is performed — only the first
// input line is read.
func (r *toolRenderer) BuildFolded(width int, styles *Styles) (string, int) {
	if styles == nil {
		return "", 1
	}
	// UF-only windows (no tool name) render like plain text: no label,
	// so the whole summary uses the muted color. An over-long first line
	// keeps its tail with a leading "…" (dim) like every other summary.
	if r.isUF && r.name == "" {
		return renderUFOnlyFolded(r, width, styles), 1
	}

	labelStyle := lineStyleForTag(r.Tag(), styles)
	dot, dotStyle := r.status.statusDot(labelStyle)

	inputFirst, inputFirstHasEllipsis := r.toolFoldedInput(width, dot)

	labelPart := padLabel(toolLabelWithIndicator(dot))
	line := labelPart
	if r.name != "" {
		line += r.name
	}
	if inputFirst != "" {
		line += " " + inputFirst
	}
	line = truncateWithSuffix(line, max(0, width-foldedPrefixWidth))

	return renderToolFoldedLine(line, labelStyle, dotStyle, dot, r.name, inputFirstHasEllipsis, styles), 1
}

// renderUFOnlyFolded renders the folded view for UF-only tool
// windows (no tool name, no AF frame): plain muted text with head+tail
// truncation (same as text windows) so the user sees both the topic and
// the latest content.
func renderUFOnlyFolded(r *toolRenderer, width int, styles *Styles) string {
	first := firstLine(prepareContent(r.output))
	head, tail, truncated := headAndTailParts(priceSummary(first), max(0, width-foldedPrefixWidth))
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

// toolFoldedInput returns the input portion that follows the tool
// name in the folded view (either the streaming delta preview tail
// or the FIRST LINE of the completed input — a tool's argument block can be
// long, so its first line is the whole of what a one-row summary can promise,
// and that is the rule for every tool). A line a handler put below the first —
// an execute_command annotation — is therefore drawn in the unfolded body only.
// The second return value is true when the input was truncated and has a
// leading "…" marker.
func (r *toolRenderer) toolFoldedInput(width int, dot string) (string, bool) {
	if r.deltaBuffer != "" {
		// Streaming delta preview: keep the LATEST chunk's tail (new JSON
		// arrives at the tail) with the ellipsis at the line START, exactly
		// like the folded text windows — the tail shows what just
		// arrived, "…" marks the truncated head (rendered dim). Room is
		// everything after the label column + tool name + separator space.
		prefix := padLabel(toolLabelWithIndicator(dot))
		if r.name != "" {
			prefix += r.name + " "
		}
		room := max(0, width-foldedPrefixWidth-cellWidth(prefix))
		tail, hasEllipsis := tailParts(flattenDelta(r.deltaBuffer), room-1)
		if hasEllipsis {
			return "…" + tail, true
		}
		return tail, false
	}
	if r.toolInput == "" {
		return "", false
	}
	inputFirst := firstLine(prepareContent(r.toolInput))
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

// renderToolFoldedLine applies per-segment styling to a tool window's
// folded line. The window line's own chrome — the "TOOL CALL" label and
// the status indicator — takes labelStyle, the single style the whole line
// is drawn in (see lineStyleForTag); the separator space and the
// label-column padding are plain (they are spaces); the tool name takes
// toolNameStyle and everything after it is content and takes the muted
// content color (with the "…" marker dim when inputFirstHasEllipsis is
// true).
//
// The name is the one glyph on the row that is not the line's style, and it
// has to be the same style the unfolded row paints it with: see
// toolNameStyle.
//
// Every offset here is a position in the line as BuildFolded BUILT it, and
// the line it receives has been through truncateWithSuffix since — so each one
// goes through cut (runeBoundary) before it is used, and the name segment is
// measured against the line rather than against `name`, which the truncation
// may have shortened (a 32-column pane does it to "execute_command"). See
// runeBoundary for what an unaligned offset costs.
func renderToolFoldedLine(
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
	// toolNameStyle — the same style the unfolded line paints it with — so
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
	// Unfold tabs BEFORE truncation so width accounting matches the final
	// render (expandTabs → TabWidth columns): the width table counts a tab
	// as 0 cells, so truncating raw tabs would let the unfolded preview
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

// renderToolArgLine renders a tool call's argument block for the unfolded window
// body. content is the block as defaultToolRender produced it.
//
// execute_command's block is the command and, at most, one annotation the
// handler appended on a line of its own (see ExecuteCommandHandler.FormatCall) —
// that line is drawn bold, the first one stays plain body text.
//
// Both go through styles.Body so the block dims as one under an overlay, which
// is why the plain line is rendered rather than passed through: styleBodyLines
// leaves a row that already carries SGR alone, and the annotation's row does.
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
