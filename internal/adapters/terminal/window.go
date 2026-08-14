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

	"charm.land/lipgloss/v2"

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

	// BuildInner returns the styled inner content and line count.
	// width is the full window width (renderer subtracts BorderInnerPadding).
	// Called only when the cache is invalid and the window is in the viewport.
	BuildInner(width int, folded bool, styles *Styles) (inner string, lineCount int)

	// Invalidate clears any cached rendering state.
	Invalidate()
}

// borderCache caches the border-wrapped render output for a Window.
// This is separate from any internal cache inside the renderer
// (e.g. textRenderer.wrappedLines for streaming optimization).
type borderCache struct {
	valid     bool
	width     int
	folded    bool
	blocked   bool   // cached blocked state (different → cache miss)
	rendered  string // full output with border
	inner     string // inner content (for cursor border swap)
	lineCount int
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

// Render returns the window with border, using cache if valid.
// When blocked is true, the content is rendered with dimmed colors.
func (w *Window) Render(width int, isCursor bool, styles *Styles, borderStyle, cursorStyle lipgloss.Style, blocked bool) string {
	// User messages use the same border color as focused input box
	if _, ok := w.renderer.(*userRenderer); ok {
		borderStyle = borderStyle.BorderForeground(styles.BorderFocused)
	}

	// Validate cache
	if w.border.valid && w.border.width == width && w.border.folded == w.Folded && w.border.blocked == blocked {
		if isCursor {
			return cursorStyle.Width(width).Render(w.border.inner)
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
		borderStyle = borderStyle.BorderForeground(styles.ColorDim)
		cursorStyle = cursorStyle.BorderForeground(styles.ColorDim)
	}

	// Cache miss — rebuild from renderer
	inner, lineCount := w.renderer.BuildInner(width, w.Folded, styles)

	// Apply folding if needed, then recalculate line count from folded output.
	if w.Folded {
		inner = w.applyFolding(inner, width, styles)
		lineCount = strings.Count(inner, "\n") + 1 + 2 // add 2 for border
	}

	w.border.inner = inner
	w.border.rendered = borderStyle.Width(width).Render(inner)
	w.border.lineCount = lineCount
	w.border.width = width
	w.border.folded = w.Folded
	w.border.blocked = blocked
	w.border.valid = true

	if isCursor {
		return cursorStyle.Width(width).Render(inner)
	}
	return w.border.rendered
}

// LineCount returns the cached line count (valid after Render).
func (w *Window) LineCount() int {
	return w.border.lineCount
}

// UpdateLineCountFast attempts to compute the line count without a full render.
// Returns (lineCount, ok). If ok is false, the caller must call Render().
// This fast path (~58μs) only applies when the renderer's internal cache is
// still valid (e.g. after resize or theme change, not after content append).
// During streaming, every append invalidates the cache, so this returns false
// and ensureLineHeights falls through to the full Render (~100-200μs).
func (w *Window) UpdateLineCountFast(width int) (int, bool) {
	if w.renderer == nil {
		return 0, false
	}
	lc, ok := w.renderLineCountFromCache(width)
	if ok && w.Folded && lc > 3+2 {
		// Folded windows show at most 3 content lines + 2 border lines.
		// The +2 accounts for the top and bottom border lines added by
		// borderStyle.Width(width).Render(inner). See ensureLineHeights.
		return 5, true
	}
	return lc, ok
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

// applyFolding collapses content to first line + indicator + last line.
// The first line is typically the header (tool name + args), so the fold
// keeps enough context to identify the window; unfold for details.
func (w *Window) applyFolding(content string, width int, styles *Styles) string {
	lines := splitLines(content)
	if len(lines) <= 3 {
		return content
	}

	indicator := lipgloss.NewStyle().
		Foreground(styles.ColorDim).
		Render(strings.Repeat(styles.FoldIndicator, width-BorderInnerPadding))

	return lines[0] + "\n" + indicator + "\n" + lines[len(lines)-1]
}

// splitLines splits a string into lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	lines := make([]string, 0, n+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
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
