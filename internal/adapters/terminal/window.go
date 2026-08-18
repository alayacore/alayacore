package terminal

// Window is a single display unit with border and content.
//
// Architecture
//
// Window holds fields accessed by WindowBuffer in hot paths (.Visible,
// .Folded, .ID, .HistoryID) and delegates type-specific rendering to
// a WindowRendering interface. This keeps ForEachVisible iteration
// fast (direct field access) while allowing each window type to have
// its own rendering and content management.
//
// Renderers (window_renderer.go):
//   - textRenderer:  assistant text (AT, At), reasoning (AR, Ar), sys msg (SN), sys err (SE)
//   - userRenderer:  user messages with optional media attachments (UT)
//   - toolRenderer:  tool calls and results (AF, Af, UF)
//
// Related files:
//   - window_renderer.go — WindowRendering interface and implementations
//   - window_buffer.go   — WindowBuffer, line tracking, virtual rendering
//   - wrap.go            — wrapContent, wrapLines, appendDeltaToLines

import (
	"strings"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ToolInfo holds the identifying details of a tool call window.
type ToolInfo struct {
	ID    string
	Name  string
	Input string
}

// WindowRendering handles type-specific rendering and content management.
// Each Window has one renderer; implementations are not shared across windows.
type WindowRendering interface {
	// Tag returns the TLV tag for cursor navigation.
	Tag() string

	// ToolInfo returns tool call details, or nil if not a tool window.
	ToolInfo() *ToolInfo

	// AppendFromTLV processes one incoming TLV frame.
	AppendFromTLV(tag string, value string)

	// BuildInner returns the styled inner content lines and line count.
	// width is the full window width (content wraps at the full width —
	// open boxes have no side borders or padding).
	// The folded parameter is legacy (folded windows now render via
	// BuildCollapsed); it is always false.
	//
	// The returned lines are the VISUAL content lines (each element is one
	// terminal row, no '\n' inside) with continuation marks (visualLine
	// Cont): rows of the same original line join without '\n' (terminal
	// soft-wrap), rows starting a new original line are separated by hard
	// '\n' — the soft-wrap breakpoints that the viewport clips against
	// (see docs/internal/virtual-rendering-performance.md). lineCount includes the 2 box rules
	// (len(lines) + 2).
	BuildInner(width int, folded bool, styles *Styles) (lines []visualLine, lineCount int)

	// BuildCollapsed returns the single-line collapsed representation of
	// the window (label + first line, truncated to fit width), WITHOUT the
	// leading collapse arrow — Window.Render adds the arrow. lineCount is
	// always 1.
	BuildCollapsed(width int, styles *Styles) (inner string, lineCount int)

	// Invalidate clears any cached rendering state.
	Invalidate()
}

// borderCache caches the rendered output for a Window.
// This is separate from any internal cache inside the renderer
// (e.g. textRenderer.wrappedLines for streaming optimization).
//
// rendered is the full output with a dim arrow; inner is the content
// after the arrow. The cursor highlight only recolors the arrow, so a
// cursor render is arrow + inner — no border re-render needed.
//
// lines is the same content as inner, but as a VISUAL line array (one
// element per terminal row, no '\n' inside; visualLine.Cont marks rows
// that continue the same original line — soft-wrap — while rows starting
// a new original line are separated by hard '\n'). It is the structure
// the viewport clips against for soft-wrap fragment output (docs/internal/virtual-rendering-performance.md);
// inner/rendered are the '\n'-joined projections kept for the current
// line-based output path.
//
// widths and arrowWidth cache display widths (ansi.StringWidth) computed
// once at render time, so renderVirtual can pad lines for soft-wrap
// fragment output without re-measuring every line on every view.
// arrow caches the dim (non-cursor) arrow glyph so the fragment output
// path never re-renders it per view (Style.Render is hot).
type borderCache struct {
	valid      bool
	width      int
	folded     bool
	blocked    bool         // cached blocked state (different → cache miss)
	rendered   string       // full output with dim arrow (non-cursor)
	inner      string       // content after the arrow (for cursor arrow swap)
	lines      []visualLine // visual rows, content after the arrow (line 0 = header/collapsed line, no arrow)
	widths     []int        // display width per line (parallel to lines)
	arrow      string       // dim arrow glyph, pre-rendered (non-cursor)
	arrowWidth int          // display width of the fold-state arrow glyph
	lineCount  int
}

// Window represents a single display window.
//
// Hot-path fields (.Visible, .Folded, .ID, .HistoryID) are struct fields
// for direct access by WindowBuffer. Type-specific behavior is delegated
// to the renderer.
type Window struct {
	ID        string
	HistoryID uint64
	Visible   bool
	Folded    bool
	styles    *Styles

	renderer WindowRendering

	// border caches the border-wrapped render output.
	border borderCache
}

// NewWindow creates a window with the appropriate renderer for the given tag.
func NewWindow(id string, tag string, styles *Styles) *Window {
	w := &Window{
		ID:     id,
		styles: styles,
	}
	w.setRenderer(tag)
	return w
}

// setRenderer sets the renderer based on the TLV tag.
func (w *Window) setRenderer(tag string) {
	switch tag {
	case tlv.TagUserT:
		w.renderer = &userRenderer{}
	case tlv.TagAssistantF, tlv.TagUserF:
		w.renderer = &toolRenderer{isUF: tag == tlv.TagUserF}
	default:
		w.renderer = &textRenderer{tag: tag}
	}
}

// ToolInfo returns tool call details, or nil if not a tool window.
func (w *Window) ToolInfo() *ToolInfo {
	if w.renderer == nil {
		return nil
	}
	return w.renderer.ToolInfo()
}

// Tag returns the TLV tag for cursor navigation.
func (w *Window) Tag() string {
	if w.renderer == nil {
		return ""
	}
	return w.renderer.Tag()
}

// AppendFromTLV processes one incoming TLV frame.
func (w *Window) AppendFromTLV(tag string, value string) {
	if w.renderer == nil {
		return
	}
	w.renderer.AppendFromTLV(tag, value)
	w.border.valid = false
}

// AppendContent adds content from a non-TLV source (e.g. directly from output.go).
// Used for system messages (SE, SN) that don't go through TLV dispatch.
func (w *Window) AppendContent(content string) {
	if w.renderer == nil {
		return
	}
	w.renderer.AppendFromTLV(w.renderer.Tag(), content)
	w.border.valid = false
}

// EnsureVisibleContent marks the window visible if it has non-whitespace content.
func (w *Window) EnsureVisibleContent(content string) {
	if !w.Visible && hasVisibleContent(content) {
		w.Visible = true
	}
}

// Invalidate marks the cache as stale.
func (w *Window) Invalidate() {
	w.border.valid = false
	if w.renderer != nil {
		w.renderer.Invalidate()
	}
}

// SetRendererForTool switches the renderer to toolRenderer (for AF/UF frames).
func (w *Window) SetRendererForTool(name, input string) {
	w.renderer = &toolRenderer{
		name:   name,
		input:  input,
		status: ToolStatusPending,
	}
	w.border.valid = false
}

// HandleToolInput updates the tool call data on an existing tool window
// or creates a tool renderer if none exists.
func (w *Window) HandleToolInput(data protocol.ToolInputData, historyID uint64) {
	if w.renderer == nil || w.renderer.Tag() != tlv.TagAssistantF {
		w.renderer = &toolRenderer{}
	}
	if tr, ok := w.renderer.(*toolRenderer); ok {
		if data.Name != "" && len(data.Input) == 0 {
			// Start frame — set name, keep existing input
			tr.name = data.Name
			if tr.input == "" {
				tr.input = string(data.Input)
			}
		} else {
			if data.Name != "" {
				tr.name = data.Name
			} else if tr.name == "" {
				// AF frame arrived with empty name. Without a fallback
				// the tool window's BuildCollapsed path treats name=="" as
				// a UF-only window and renders nothing useful.
				tr.name = "_"
			}
			tr.input = string(data.Input)
			// Complete input arrived, clear delta preview.
			tr.deltaBuffer = ""
		}
		if tr.status == ToolStatusNone {
			tr.status = ToolStatusPending
		}
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.border.valid = false
}

// HandleToolOutput sets the output and status on a tool window.
func (w *Window) HandleToolOutput(output string, isError bool, historyID uint64) {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		tr.output = output
		if isError {
			tr.status = ToolStatusError
		} else {
			tr.status = ToolStatusSuccess
		}
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.border.valid = false
}

// SetHistoryID sets the history ID if the given value is larger.
func (w *Window) SetHistoryID(hid uint64) {
	if hid > w.HistoryID {
		w.HistoryID = hid
	}
}

// RawContent returns the accumulated text content for testing.
func (w *Window) RawContent() string {
	if w.renderer == nil {
		return ""
	}
	switch r := w.renderer.(type) {
	case *textRenderer:
		return r.rawContent()
	case *userRenderer:
		return strings.Join(r.textParts, "\n")
	case *toolRenderer:
		return r.input
	}
	return ""
}

// ToggleMarkdownMode toggles markdown table rendering for plain-text
// windows (assistant text AT / reasoning AR). Returns false for windows
// that never render markdown (user prompts, tools, system messages).
func (w *Window) ToggleMarkdownMode() bool {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return false
	}
	tr.ToggleMarkdownMode()
	w.border.valid = false
	return true
}

// MarkdownMode reports whether the window renders markdown tables.
func (w *Window) MarkdownMode() bool {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return false
	}
	return tr.mdMode
}

// SetMarkdownDefault sets the initial markdown rendering state for
// plain-text windows (assistant text AT / reasoning AR). No-op for other
// window types. Existing state is overwritten (used at window creation).
func (w *Window) SetMarkdownDefault(on bool) {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return
	}
	tr.mdMode = on
	w.border.valid = false
}

// RawStatus returns the tool status for testing.
func (w *Window) RawStatus() ToolStatus {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.status
	}
	return ToolStatusNone
}

// RawToolName returns the tool name for testing.
func (w *Window) RawToolName() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.name
	}
	return ""
}

// RawTag returns the TLV tag for testing.
func (w *Window) RawTag() string {
	return w.Tag()
}

// RawDelta returns the current tool delta buffer for testing.
// Returns empty string for non-tool windows.
func (w *Window) RawDelta() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.deltaBuffer
	}
	return ""
}

// Render returns the window, using cache if valid.
// When blocked is true, the content is rendered with dimmed colors.
//
// Two visual states:
//   - Folded: a single line — collapse arrow + label + first line (no box).
//   - Expanded: a header line — expand arrow + label — above an open box
//     (top/bottom rules only, no side borders).
//
// The cursor highlight colors the fold-state arrow with the selection
// color; the arrow glyph itself comes from the theme; borders never
// change color on navigation.
func (w *Window) Render(width int, isCursor bool, styles *Styles, borderStyle Style, blocked bool) string {
	if w.renderer == nil {
		return ""
	}

	// User messages use the same border color as focused input box
	if _, ok := w.renderer.(*userRenderer); ok {
		borderStyle = borderStyle.Foreground(styles.BorderFocused)
	}

	// Validate cache
	if w.border.valid && w.border.width == width && w.border.folded == w.Folded && w.border.blocked == blocked {
		if isCursor {
			return w.renderCursorArrow(blocked)
		}
		return w.border.rendered
	}

	// Invalidate renderer cache when blocked state changes, so BuildInner
	// does a full re-styled render with the new (dimmed or normal) styles.
	if w.border.valid && w.border.blocked != blocked && w.renderer != nil {
		w.renderer.Invalidate()
	}

	// Use dimmed styles and border when blocked
	if blocked {
		styles = styles.Dimmed()
		borderStyle = borderStyle.Foreground(styles.ColorDim)
	}

	arrow := w.arrowChar()
	arrowWidth := ansi.StringWidth(arrow)
	w.border.arrow = arrowStyle(styles).Render(arrow)
	if w.Folded {
		// Collapsed: single line — arrow + label + first line, truncated.
		// BuildCollapsed skips full wrapping: only the first content line
		// is read and truncated, so folding a large window is O(1).
		inner, _ := w.renderer.BuildCollapsed(width, styles)
		w.border.lines = []visualLine{{Text: " " + inner}}
		w.border.widths = nil // computed lazily by renderVirtual (fragment output)
		w.border.inner = w.border.lines[0].Text
		w.border.rendered = w.border.arrow + w.border.inner
		w.border.lineCount = 1
	} else {
		// Expanded: header line (expand arrow + label) above the open box.
		// BuildInner returns the visual content lines (soft-wrap
		// breakpoints); the box rules and the header are separate visual
		// lines, so the whole window is one flat visual line array.
		contentLines, _ := w.renderer.BuildInner(width, false, styles)
		header := w.buildExpandHeader(styles)
		boxLines := styles.RenderOpenBoxLines(contentLines, width, borderStyle.GetForeground())
		lines := make([]visualLine, 0, len(boxLines)+1)
		lines = append(lines, visualLine{Text: header})
		lines = append(lines, boxLines...)
		w.border.lines = lines
		w.border.widths = nil // computed lazily by renderVirtual (fragment output)
		w.border.inner = joinVisualLines(lines)
		w.border.rendered = w.border.arrow + w.border.inner
		w.border.lineCount = len(lines)
	}

	w.border.arrowWidth = arrowWidth

	w.border.width = width
	w.border.folded = w.Folded
	w.border.blocked = blocked
	w.border.valid = true

	if isCursor {
		return w.renderCursorArrow(blocked)
	}
	return w.border.rendered
}

// renderCursorArrow recolors only the collapse/expand arrow with the
// selection color; the rest of the cached output is reused as-is.
func (w *Window) renderCursorArrow(blocked bool) string {
	// When blocked (overlay active), the selection color is replaced by
	// the dim color so the highlight disappears under the overlay.
	color := Color("")
	if w.styles != nil {
		color = w.styles.BorderCursor
		if blocked {
			color = w.styles.ColorDim
		}
	}
	return NewStyle().Foreground(color).Render(w.arrowChar()) + w.border.inner
}

// arrowChar returns the fold-state arrow glyph, configured by the theme
// (falling back to conventional defaults if no theme is attached).
func (w *Window) arrowChar() string {
	if w.styles != nil {
		if w.Folded {
			if w.styles.FoldArrow != "" {
				return w.styles.FoldArrow
			}
		} else if w.styles.UnfoldArrow != "" {
			return w.styles.UnfoldArrow
		}
	}
	if w.Folded {
		return "▶"
	}
	return "▼"
}

// arrowStyle returns the style for the collapse/expand arrow.
// Cursor highlighting is handled separately by renderCursorArrow.
func arrowStyle(styles *Styles) Style {
	if styles == nil {
		return NewStyle()
	}
	return NewStyle().Foreground(styles.ColorDim)
}

// windowLabel returns the header label for the window type, e.g.
// "TOOLUSE edit_file", "REASONING", "ASSISTANT", "USER PROMPT", "NOTIFY",
// "ERROR".
func (w *Window) windowLabel() string {
	switch w.Tag() {
	case tlv.TagAssistantF, tlv.TagUserF:
		if ti := w.ToolInfo(); ti != nil && ti.Name != "" {
			return toolHeaderLabel + " " + ti.Name
		}
		return toolHeaderLabel
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

// buildExpandHeader returns the expanded header line content (the part
// after the arrow). Tool windows use the collapsed-style layout — bold
// "TOOLUSE" + a space + the status indicator in the fixed label column,
// then the muted tool name: "TOOLUSE ⠋    execute_command". Other windows
// use their plain label ("ASSISTANT", "NOTIFY", …).
func (w *Window) buildExpandHeader(styles *Styles) string {
	if tr, ok := w.renderer.(*toolRenderer); ok && tr.name != "" {
		dot, dotStyle := tr.status.statusDot()
		label := padLabel(toolLabelWithIndicator(dot))
		var sb strings.Builder
		sb.WriteString(" ")
		sb.WriteString(labelStyleForTag(w.Tag(), styles).Render(label[:len(toolHeaderLabel)]))
		// Separator space between the label and the status indicator.
		sb.WriteString(label[len(toolHeaderLabel) : len(toolHeaderLabel)+len(toolLabelSep)])
		sb.WriteString(dotStyle.Render(dot))
		// Label-column padding — the indicator is multi-byte UTF-8, so skip
		// len(dot) bytes (not 1) after the label + separator.
		sb.WriteString(label[len(toolHeaderLabel)+len(toolLabelSep)+len(dot):])
		sb.WriteString(styles.ToolContent.Render(tr.name))
		return sb.String()
	}
	label := w.windowLabel()
	if label == "" {
		return ""
	}
	return " " + w.labelStyle(styles).Render(label)
}

// labelStyle returns the style used for the window's header label.
func (w *Window) labelStyle(styles *Styles) Style {
	return labelStyleForTag(w.Tag(), styles)
}

// labelStyleForTag returns the style used for a window type's header label
// (e.g. "REASONING", "TOOLUSE", "ERROR"). All labels are bold + muted — bright
// default-foreground labels distract the eye in the collapsed list; only
// ERROR keeps its red semantic color. Shared by the expanded header line
// and the collapsed label segment.
func labelStyleForTag(tag string, styles *Styles) Style {
	if styles == nil {
		return NewStyle().Bold(true)
	}
	switch tag {
	case TagWindowSE:
		return styles.Error
	default:
		// All other labels: bold + muted.
		return styles.System.Bold(true)
	}
}

// LineCount returns the cached line count (valid after Render).
func (w *Window) LineCount() int {
	return w.border.lineCount
}

// UpdateLineCountFast attempts to compute the line count without a full render.
// Returns (lineCount, ok). If ok is false, the caller must call Render().
//
// Folded windows are always a single line, so this returns immediately
// without touching the renderer — during streaming, deltas to folded
// windows no longer trigger any wrapping or rendering for line tracking.
// This is the main performance win of the collapsed-line design.
func (w *Window) UpdateLineCountFast(width int) (int, bool) {
	if w.renderer == nil {
		return 0, false
	}
	if w.Folded {
		return 1, true
	}
	// Unfolded: try the renderer's internal cache. This fast path
	// (~58μs) only applies when the renderer's internal cache is still
	// valid (e.g. after resize or theme change, not after content append).
	// During streaming, every append invalidates the cache, so this
	// returns false and ensureLineHeights falls through to the full
	// Render (~100-200μs).
	return w.renderLineCountFromCache(width)
}

// renderLineCountFromCache tries to get line count from the renderer's cache.
func (w *Window) renderLineCountFromCache(width int) (int, bool) {
	// Check if renderer supports fast line count
	type lineCounter interface {
		TryLineCount(width int) (int, bool)
	}
	if lc, ok := w.renderer.(lineCounter); ok {
		return lc.TryLineCount(width)
	}
	return 0, false
}

// hasVisibleContent returns true if content has at least one non-whitespace character.
func hasVisibleContent(content string) bool {
	for _, r := range content {
		if !isWhitespace(r) {
			return true
		}
	}
	return false
}

// isWhitespace returns true if the character is whitespace.
func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
