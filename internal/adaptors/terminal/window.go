package terminal

// Window buffer, rendering, and display for the terminal UI.
// Provides virtual scrolling, incremental updates, diff visualization,
// and viewport management.
//
// # Architecture
//
// The rendering model is intentionally simple:
//
//	Window.render(width, isCursor, styles) → string
//
// All caching is internal to Window. Callers don't need to know about
// cache invalidation, line heights, or rebuild states.
//
// WindowBuffer coordinates multiple windows and provides:
//   - Virtual rendering (only visible windows are rendered)
//   - Line height tracking (for scroll positioning)
//   - ID-based window lookup (for incremental updates)

import (
	"strings"
	"sync"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Window - Single Display Window with Internal Caching
// ============================================================================

// Window represents a single display window with border and content.
// Caching is handled internally - callers just call Render().
type Window struct {
	ID       string     // stream ID or generated unique ID
	Tag      string     // TLV tag that created this window
	ToolName string     // tool name (for FC/FR tags)
	Content  string     // accumulated content (raw, unstyled)
	Folded   bool       // true if window is in folded (collapsed) mode
	Status   ToolStatus // status indicator for tool windows
	styles   *Styles    // reference to styles for incremental updates

	// Internal cache - updated on render, invalidated on content change
	cache windowCache
}

// windowCache holds rendered output and derived state
type windowCache struct {
	valid        bool     // true if cache is valid
	width        int      // width used for cached render
	folded       bool     // folded state when cached
	contentLen   int      // content length when cached
	rendered     string   // full output with border
	inner        string   // inner content (for cursor border swap)
	lineCount    int      // number of lines in rendered output
	wrappedLines []string // wrapped lines for incremental update
}

// IsDiffWindow returns true if the window is a diff window
func (w *Window) IsDiffWindow() bool {
	return w.ToolName == "edit_file"
}

// Render returns the window with border, using cache if valid.
// This is the single entry point for rendering a window.
func (w *Window) Render(width int, isCursor bool, styles *Styles, borderStyle, cursorStyle lipgloss.Style) string {
	// Check if cache is valid
	cacheValid := w.cache.valid && w.cache.width == width && w.cache.folded == w.Folded
	if cacheValid {
		// Diff windows only need folded state to match; regular windows need content length match
		if !w.IsDiffWindow() && len(w.Content) != w.cache.contentLen {
			w.cache.valid = false
		}
	} else {
		w.cache.valid = false
	}

	// Rebuild cache if needed
	if !w.cache.valid {
		w.rebuildCache(width, styles, borderStyle)
	}

	// Return with appropriate border
	if isCursor {
		return cursorStyle.Width(width).Render(w.cache.inner)
	}
	return w.cache.rendered
}

// rebuildCache renders the window content and updates the cache
func (w *Window) rebuildCache(width int, styles *Styles, borderStyle lipgloss.Style) {
	innerWidth := max(0, width-4)

	// Render content based on window type
	var inner string
	switch {
	case w.IsDiffWindow():
		inner = RenderDiffContent(w.Content, w.Status, styles)
	default:
		inner = w.renderGenericContent(innerWidth, styles)
	}

	// Apply folding if needed
	if w.Folded {
		inner = w.applyFolding(inner, innerWidth, styles)
	}

	// Update cache
	w.cache.rendered = borderStyle.Width(width).Render(inner)
	w.cache.inner = inner
	w.cache.width = width
	w.cache.folded = w.Folded
	w.cache.contentLen = len(w.Content)
	w.cache.lineCount = strings.Count(w.cache.rendered, "\n") + 1
	w.cache.valid = true
}

// renderGenericContent renders a generic tool window content
func (w *Window) renderGenericContent(innerWidth int, styles *Styles) string {
	innerWidth = max(0, innerWidth)

	// FAST PATH: Use cached wrapped lines if width matches
	// This avoids re-styling and re-wrapping the entire content
	if len(w.cache.wrappedLines) > 0 && w.cache.width-4 == innerWidth && innerWidth > 0 {
		return strings.Join(w.cache.wrappedLines, "\n")
	}

	// SLOW PATH: Full styling and wrapping
	content := w.Content

	// Prepare content FIRST: strip input ANSI and expand tabs
	// This must happen BEFORE styling so we don't strip lipgloss ANSI
	content = prepareContent(content)

	// Apply styling based on tag
	content = w.styleContent(content, styles)

	// Wrap content
	if innerWidth <= 0 {
		return content
	}

	wrapped := lipgloss.Wrap(content, innerWidth, " ")
	w.cache.wrappedLines = strings.Split(wrapped, "\n")
	return wrapped
}

// styleMultiline applies a style to each line of text
func styleMultiline(content string, style lipgloss.Style) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// applyFolding collapses content to first line + indicator + last 3 lines
func (w *Window) applyFolding(content string, innerWidth int, styles *Styles) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 5 {
		return content
	}

	indicator := lipgloss.NewStyle().
		Foreground(styles.ColorDim).
		Render(strings.Repeat("⁝", innerWidth))

	return lines[0] + "\n" + indicator + "\n" + strings.Join(lines[len(lines)-3:], "\n")
}

// Invalidate marks the cache as invalid (called when content changes)
func (w *Window) Invalidate() {
	w.cache.valid = false
	w.cache.wrappedLines = nil
}

// AppendContent adds content incrementally, updating wrapped lines if possible
func (w *Window) AppendContent(delta string, innerWidth int) {
	w.Content += delta

	// Try incremental update if we have cached wrapped lines and styles
	// Skip incremental updates for diff windows as they need special rendering
	if len(w.cache.wrappedLines) > 0 && innerWidth > 0 && w.styles != nil && !w.IsDiffWindow() {
		// Prepare delta before styling (strip input ANSI, expand tabs)
		preparedDelta := prepareContent(delta)
		styledDelta := w.styleContent(preparedDelta, w.styles)
		w.cache.wrappedLines = appendDeltaToLines(w.cache.wrappedLines, styledDelta, innerWidth)
		// Mark cache as needing rebuild for rendered output, but wrappedLines is updated
		// The rebuild will use cached wrappedLines instead of re-wrapping
		w.cache.valid = false
	} else {
		// Can't do incremental - need full rebuild
		w.cache.valid = false
		w.cache.wrappedLines = nil
	}
}

// styleContent applies styling to content based on window tag
func (w *Window) styleContent(content string, styles *Styles) string {
	if styles == nil {
		return content
	}

	// Apply styling based on tag
	switch w.Tag {
	case stream.TagFunctionCall:
		return w.Status.Indicator(styles) + ColorizeTool(content, styles)
	case stream.TagFunctionResult:
		return styleMultiline(content, styles.Text)
	case stream.TagTextAssistant:
		return styleMultiline(content, styles.Text)
	case stream.TagTextReasoning:
		return styleMultiline(content, styles.Reasoning)
	case stream.TagTextUser:
		return styles.Prompt.Render("> ") + styles.UserInput.Render(content)
	case stream.TagSystemError:
		return styleMultiline(content, styles.Error)
	case stream.TagSystemNotify:
		return styleMultiline(content, styles.System)
	default:
		return content
	}
}

// LineCount returns the cached line count (valid after Render())
func (w *Window) LineCount() int {
	return w.cache.lineCount
}

// ============================================================================
// WindowBuffer - Manages Multiple Windows with Virtual Rendering
// ============================================================================

// WindowBuffer holds a sequence of windows with virtual rendering support.
type WindowBuffer struct {
	mu          sync.Mutex
	Windows     []*Window // public for tests
	idIndex     map[string]int
	width       int
	styles      *Styles
	borderStyle lipgloss.Style
	cursorStyle lipgloss.Style

	// Line height tracking (for cursor navigation)
	lineHeights []int
	totalLines  int
	dirty       bool // true if lineHeights needs rebuild
	dirtyIndex  int  // index of single dirty window, -1 = clean, -2 = full rebuild

	// Virtual rendering state
	viewportYOffset int
	viewportHeight  int
}

// Sentinel values for dirtyIndex
const (
	dirtyClean       = -1 // no dirty windows
	dirtyFullRebuild = -2 // multiple windows dirty, need full rebuild
)

// NewWindowBuffer creates a new window buffer with given width and styles.
func NewWindowBuffer(width int, styles *Styles) *WindowBuffer {
	return &WindowBuffer{
		Windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		styles:      styles,
		borderStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1),
		cursorStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1),
		lineHeights: []int{},
		dirtyIndex:  dirtyClean,
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
		// Invalidate all windows
		for _, w := range wb.Windows {
			w.Invalidate()
		}
		wb.dirty = true
		wb.dirtyIndex = dirtyFullRebuild // all windows affected
	}
}

// Width returns the current window width.
func (wb *WindowBuffer) Width() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.width
}

// SetStyles updates the styles for the window buffer.
func (wb *WindowBuffer) SetStyles(styles *Styles) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.styles = styles
	wb.borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1)
	wb.cursorStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1)
	// Invalidate all windows to pick up new styles
	for _, w := range wb.Windows {
		w.styles = styles // Update window's styles reference
		w.Invalidate()
	}
	wb.dirty = true
	wb.dirtyIndex = dirtyFullRebuild
}

// AppendOrUpdate adds content to an existing window or creates a new one.
func (wb *WindowBuffer) AppendOrUpdate(id string, tag string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	innerWidth := max(0, wb.width-4)

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.Windows[idx]
		w.AppendContent(content, innerWidth)
		wb.markDirty(idx)
		return
	}

	// Create new window
	folded := tag != stream.TagTextUser && tag != stream.TagTextAssistant
	w := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Folded:  folded,
		styles:  wb.styles,
	}
	wb.Windows = append(wb.Windows, w)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// AppendToolCall adds a tool call window with tool name.
func (wb *WindowBuffer) AppendToolCall(id string, toolName string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.Windows[idx]
		w.AppendContent(content, max(0, wb.width-4))
		wb.markDirty(idx)
		return
	}

	w := &Window{
		ID:       id,
		Tag:      stream.TagFunctionCall,
		ToolName: toolName,
		Content:  content,
		Folded:   true,
		styles:   wb.styles,
	}
	wb.Windows = append(wb.Windows, w)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// GetHandler returns the tool display handler for a window by ID.
func (wb *WindowBuffer) GetHandler(id string) ToolDisplayHandler {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.Windows[idx]
		if w.ToolName != "" {
			return GetHandler(w.ToolName)
		}
	}
	return nil
}

// markDirty marks that line heights need rebuilding.
// Uses sentinel values to track single vs multiple dirty windows:
//   - dirtyClean (-1): no dirty windows
//   - dirtyFullRebuild (-2): multiple windows dirty, need full rebuild
//   - >= 0: index of the single dirty window
//
// This enables incremental updates during streaming (same window repeatedly)
// while correctly triggering full rebuild for session loading (multiple windows rapidly).
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.dirtyIndex == dirtyFullRebuild {
		// Already marked for full rebuild, keep it
		return
	}
	if wb.dirtyIndex >= 0 && wb.dirtyIndex != idx {
		// Different window already dirty - need full rebuild
		wb.dirtyIndex = dirtyFullRebuild
	} else {
		// Either clean or same window - mark just this one
		wb.dirtyIndex = idx
	}
	wb.dirty = true
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.dirty = true
	wb.dirtyIndex = dirtyClean
}

// GetWindowCount returns the number of windows.
func (wb *WindowBuffer) GetWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.Windows)
}

// GetWindow returns the window at the given index (for testing).
func (wb *WindowBuffer) GetWindow(index int) *Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if index < 0 || index >= len(wb.Windows) {
		return nil
	}
	return wb.Windows[index]
}

// ToggleFold toggles the fold state of a window.
func (wb *WindowBuffer) ToggleFold(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return false
	}
	wb.Windows[windowIndex].Folded = !wb.Windows[windowIndex].Folded
	wb.markDirty(windowIndex)
	return true
}

// GetWindowContent returns the raw content of a window by index.
// Returns empty string if index is out of bounds.
func (wb *WindowBuffer) GetWindowContent(windowIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return ""
	}

	return wb.Windows[windowIndex].Content
}

// UpdateToolStatus updates the status indicator for a tool window.
func (wb *WindowBuffer) UpdateToolStatus(toolCallID string, status ToolStatus) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[toolCallID]; ok {
		w := wb.Windows[idx]
		w.Status = status
		w.Invalidate()
		if status == ToolStatusSuccess || status == ToolStatusError {
			if w.ToolName == "write_file" {
				w.Folded = true
			}
		}
		wb.markDirty(idx)
	}
}

// ============================================================================
// Line Height Tracking
// ============================================================================

// ensureLineHeights rebuilds line heights if dirty.
// Supports incremental update when only one window changed.
func (wb *WindowBuffer) ensureLineHeights() {
	if !wb.dirty && len(wb.lineHeights) == len(wb.Windows) {
		return
	}

	// Extend lineHeights slice if needed
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Incremental update: only re-render the dirty window
	if wb.dirtyIndex >= 0 && wb.dirtyIndex < len(wb.Windows) {
		w := wb.Windows[wb.dirtyIndex]
		w.Render(wb.width, false, wb.styles, wb.borderStyle, wb.cursorStyle)
		oldHeight := wb.lineHeights[wb.dirtyIndex]
		newHeight := w.LineCount()
		wb.lineHeights[wb.dirtyIndex] = newHeight
		wb.totalLines += newHeight - oldHeight
	} else {
		// Full rebuild (dirtyIndex == dirtyFullRebuild or first init)
		wb.totalLines = 0
		for i, w := range wb.Windows {
			w.Render(wb.width, false, wb.styles, wb.borderStyle, wb.cursorStyle)
			wb.lineHeights[i] = w.LineCount()
			wb.totalLines += wb.lineHeights[i]
		}
	}
	wb.dirty = false
	wb.dirtyIndex = dirtyClean
}

// GetWindowStartLine returns the starting line number for a window.
// IMPORTANT: This calls ensureLineHeights() to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowStartLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	start := 0
	for i := range windowIndex {
		start += wb.lineHeights[i]
	}
	return start
}

// GetWindowEndLine returns the ending line number for a window.
// IMPORTANT: This calls ensureLineHeights() to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowEndLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	end := 0
	for i := 0; i <= windowIndex; i++ {
		end += wb.lineHeights[i]
	}
	return end
}

// GetTotalLines returns total lines across all windows.
func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
}

// GetTotalLinesVirtual returns total lines (ensuring lineHeights are calculated).
func (wb *WindowBuffer) GetTotalLinesVirtual() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
}

// ============================================================================
// Virtual Rendering
// ============================================================================

// SetViewportPosition updates viewport state for virtual rendering.
func (wb *WindowBuffer) SetViewportPosition(yOffset, height int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.viewportYOffset = yOffset
	wb.viewportHeight = height
}

// GetAll returns rendered windows, using virtual rendering if viewport is set.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if len(wb.Windows) == 0 {
		return ""
	}

	// Ensure line heights are current
	wb.ensureLineHeights()

	// Use virtual rendering if viewport is set
	if wb.viewportHeight > 0 {
		return wb.renderVirtual(cursorIndex)
	}

	// Full render
	return wb.renderAll(cursorIndex)
}

// renderVirtual renders only visible windows (with buffer)
func (wb *WindowBuffer) renderVirtual(cursorIndex int) string {
	// Calculate visible range with buffer
	bufferLines := wb.viewportHeight
	if bufferLines < 10 {
		bufferLines = 10
	}

	startLine := max(0, wb.viewportYOffset-bufferLines)
	endLine := wb.viewportYOffset + wb.viewportHeight + bufferLines

	startWindow := wb.findWindowAtLine(startLine)
	endWindow := wb.findWindowAtLine(endLine)

	// Add extra buffer windows
	bufferWindows := 5
	startWindow = max(0, startWindow-bufferWindows)
	endWindow = min(len(wb.Windows)-1, endWindow+bufferWindows)

	var sb strings.Builder
	for i := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		if i >= startWindow && i <= endWindow {
			// Render actual content
			sb.WriteString(wb.Windows[i].Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, wb.cursorStyle))
		} else {
			// Render placeholder (blank lines)
			for j := 0; j < wb.lineHeights[i]; j++ {
				if j > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(" ")
			}
		}
	}
	return sb.String()
}

// renderAll renders all windows
func (wb *WindowBuffer) renderAll(cursorIndex int) string {
	var sb strings.Builder
	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(w.Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, wb.cursorStyle))
	}
	return sb.String()
}

// findWindowAtLine returns the window index containing the given line.
func (wb *WindowBuffer) findWindowAtLine(line int) int {
	current := 0
	for i, h := range wb.lineHeights {
		if current+h > line {
			return i
		}
		current += h
	}
	return len(wb.Windows) - 1
}

// RenderWindowContent renders the content of a window (for testing).
func (wb *WindowBuffer) RenderWindowContent(w *Window, innerWidth int) string {
	return w.renderGenericContent(innerWidth, wb.styles)
}

// ============================================================================
// Line Wrapping Utilities
// ============================================================================

// wrapLines wraps content into lines at the given width.
func wrapLines(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := lipgloss.Wrap(content, width, " ")
	return strings.Split(wrapped, "\n")
}

// appendDeltaToLines incrementally wraps a delta onto existing lines.
func appendDeltaToLines(lines []string, delta string, width int) []string {
	if len(lines) == 0 {
		return wrapLines(delta, width)
	}
	if width <= 0 {
		lines[len(lines)-1] += delta
		return lines
	}

	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlines(lines, delta, width)
	}

	// Append to last line and rewrap
	lastLine := lines[len(lines)-1]
	combined := lastLine + delta
	newLines := wrapLines(combined, width)
	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	parts := strings.Split(delta, "\n")
	for i, part := range parts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastIdx := len(lines) - 1
				combined := lines[lastIdx] + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:lastIdx], newLines...)
			}
		} else {
			lines = append(lines, wrapLines(part, width)...)
		}
	}
	return lines
}

// ============================================================================
// DisplayModel - Viewport over WindowBuffer
// ============================================================================

// DisplayModel holds the viewport over WindowBuffer content.
type DisplayModel struct {
	viewport            viewport.Model
	windowBuffer        *WindowBuffer
	styles              *Styles
	width               int
	height              int
	windowCursor        int
	userMovedCursorAway bool
	displayFocused      bool
	lastContent         string
}

// NewDisplayModel creates a new display model
func NewDisplayModel(windowBuffer *WindowBuffer, styles *Styles) DisplayModel {
	vp := viewport.New(viewport.WithWidth(DefaultWidth), viewport.WithHeight(DefaultHeight))
	return DisplayModel{
		viewport:            vp,
		windowBuffer:        windowBuffer,
		styles:              styles,
		width:               DefaultWidth,
		height:              DefaultHeight,
		windowCursor:        -1,
		userMovedCursorAway: false,
		displayFocused:      false,
	}
}

// Init initializes the display
func (m DisplayModel) Init() tea.Cmd { return nil }

// Update handles messages for the display
func (m DisplayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if windowMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = windowMsg.Width
		m.viewport.SetWidth(max(0, windowMsg.Width))
	}
	return m, nil
}

// View renders the display
func (m DisplayModel) View() tea.View {
	return tea.NewView(m.viewport.View())
}

// SetHeight sets the viewport height
func (m *DisplayModel) SetHeight(height int) {
	m.height = height
	m.viewport.SetHeight(max(0, height))
}

// GetHeight returns the current viewport height
func (m DisplayModel) GetHeight() int {
	return m.viewport.Height()
}

// SetWidth sets the viewport width
func (m *DisplayModel) SetWidth(width int) {
	m.width = width
	m.viewport.SetWidth(max(0, width))
}

// SetDisplayFocused sets whether the display is focused
func (m *DisplayModel) SetDisplayFocused(focused bool) {
	m.displayFocused = focused
}

// SetStyles updates the styles for the display
func (m *DisplayModel) SetStyles(styles *Styles) {
	m.styles = styles
}

// YOffset returns the current scroll position
func (m DisplayModel) YOffset() int {
	return m.viewport.YOffset()
}

// updateContent updates the viewport content from the window buffer
func (m *DisplayModel) updateContent() {
	cursorIndex := -1
	if m.displayFocused {
		cursorIndex = m.windowCursor
	}

	totalLines := m.windowBuffer.GetTotalLinesVirtual()
	viewportHeight := m.viewport.Height()

	targetYOffset := m.viewport.YOffset()
	if m.shouldFollow() && totalLines > viewportHeight {
		targetYOffset = max(0, totalLines-viewportHeight)
	}

	m.windowBuffer.SetViewportPosition(targetYOffset, viewportHeight)

	newContent := m.windowBuffer.GetAll(cursorIndex)
	if newContent == m.lastContent {
		return
	}
	m.lastContent = newContent

	m.viewport.SetContent(newContent)

	if m.shouldFollow() {
		m.viewport.GotoBottom()
	}
}

// ScrollDown scrolls down by lines
func (m *DisplayModel) ScrollDown(lines int) {
	m.viewport.ScrollDown(lines)
}

// AtBottom returns whether viewport is at bottom
func (m DisplayModel) AtBottom() bool {
	return m.viewport.AtBottom()
}

// ScrollUp scrolls up by lines
func (m *DisplayModel) ScrollUp(lines int) {
	m.viewport.ScrollUp(lines)
}

// GotoBottom goes to bottom
func (m *DisplayModel) GotoBottom() {
	m.viewport.GotoBottom()
}

// GotoTop goes to top
func (m *DisplayModel) GotoTop() {
	m.viewport.GotoTop()
}

// UpdateHeight sets the viewport height based on total window height
func (m *DisplayModel) UpdateHeight(totalHeight int) {
	m.viewport.SetHeight(max(0, totalHeight-LayoutGap))
	m.updateContent()
}

// shouldFollow returns true when viewport should auto-follow new content
func (m *DisplayModel) shouldFollow() bool {
	return !m.userMovedCursorAway
}

// GetWindowCursor returns the current window cursor index
func (m *DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// GetCursorWindowContent returns the content of the currently selected window.
// Returns empty string if no window is selected.
func (m *DisplayModel) GetCursorWindowContent() string {
	if m.windowCursor < 0 {
		return ""
	}
	return m.windowBuffer.GetWindowContent(m.windowCursor)
}

// SetWindowCursor sets the window cursor to a specific index
func (m *DisplayModel) SetWindowCursor(index int) {
	windowCount := m.windowBuffer.GetWindowCount()
	if index < -1 {
		index = -1
	} else if index >= windowCount {
		index = windowCount - 1
	}
	m.windowCursor = index
	if windowCount > 0 && index == windowCount-1 {
		m.userMovedCursorAway = false
	} else if index >= 0 {
		m.userMovedCursorAway = true
	}
}

// MoveWindowCursorDown moves the window cursor down
func (m *DisplayModel) MoveWindowCursorDown() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 || m.windowCursor == windowCount-1 {
		return false
	}
	if m.windowCursor < 0 {
		m.windowCursor = 0
	} else {
		m.windowCursor++
	}
	m.userMovedCursorAway = m.windowCursor != windowCount-1
	return true
}

// MoveWindowCursorUp moves the window cursor up
func (m *DisplayModel) MoveWindowCursorUp() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 || m.windowCursor == 0 {
		return false
	}
	if m.windowCursor < 0 {
		m.windowCursor = 0
	} else {
		m.windowCursor--
	}
	m.userMovedCursorAway = true
	return true
}

// EnsureCursorVisible scrolls the viewport to make the cursor window visible
func (m *DisplayModel) EnsureCursorVisible() {
	if m.windowCursor < 0 {
		return
	}

	startLine := m.windowBuffer.GetWindowStartLine(m.windowCursor)
	endLine := m.windowBuffer.GetWindowEndLine(m.windowCursor)
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()

	if startLine < viewportTop {
		m.viewport.SetYOffset(startLine)
	} else if endLine > viewportBottom {
		m.viewport.SetYOffset(endLine - m.viewport.Height())
	}
}

// ValidateCursor ensures the window cursor is valid
func (m *DisplayModel) ValidateCursor() {
	windowCount := m.windowBuffer.GetWindowCount()
	if m.windowCursor >= windowCount {
		m.windowCursor = windowCount - 1
	}
	if m.windowCursor < -1 {
		m.windowCursor = -1
	}
	if m.windowCursor >= 0 && windowCount > 0 {
		m.EnsureCursorVisible()
	}
}

// SetCursorToLastWindow sets the cursor to the last window
func (m *DisplayModel) SetCursorToLastWindow() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
	} else {
		m.windowCursor = windowCount - 1
		m.userMovedCursorAway = false
	}
}

// ToggleWindowFold toggles the fold state of the selected window
func (m *DisplayModel) ToggleWindowFold() bool {
	if m.windowCursor < 0 {
		return false
	}
	return m.windowBuffer.ToggleFold(m.windowCursor)
}

// MarkUserScrolled marks that the user scrolled manually
func (m *DisplayModel) MarkUserScrolled() {
	m.userMovedCursorAway = true
}

// MoveWindowCursorToTop moves cursor to top visible window
func (m *DisplayModel) MoveWindowCursorToTop() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportTop := m.viewport.YOffset()
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		if (startLine <= viewportTop && endLine > viewportTop) || startLine >= viewportTop {
			m.windowCursor = i
			m.userMovedCursorAway = true
			return true
		}
	}
	return false
}

// MoveWindowCursorToBottom moves cursor to bottom visible window
func (m *DisplayModel) MoveWindowCursorToBottom() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportBottom := m.viewport.YOffset() + m.viewport.Height()
	for i := windowCount - 1; i >= 0; i-- {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		if (startLine < viewportBottom && endLine >= viewportBottom) || endLine <= viewportBottom {
			m.windowCursor = i
			m.userMovedCursorAway = i < windowCount-1
			return true
		}
	}
	return false
}

// MoveWindowCursorToCenter moves cursor to the window at the visual center of the screen.
// It finds the window that contains the center line of the visible viewport.
// If no window contains the center line, it finds the window closest to the center.
func (m *DisplayModel) MoveWindowCursorToCenter() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// Calculate the center line of the visible viewport
	viewportHeight := m.viewport.Height()
	viewportTop := m.viewport.YOffset()
	viewportCenter := viewportTop + viewportHeight/2

	// First, try to find the window that contains the viewport center line
	// endLine is exclusive, so we use < for the upper bound
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)

		// Check if viewport center line falls within this window
		if viewportCenter >= startLine && viewportCenter < endLine {
			m.windowCursor = i
			m.userMovedCursorAway = m.windowCursor < windowCount-1
			return true
		}
	}

	// If center line falls in a gap (or all windows are above/below center),
	// find the visible window whose center is closest to the viewport center
	var bestWindow int
	bestDistance := -1

	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)

		// Only consider visible windows
		if startLine >= viewportTop+viewportHeight || endLine <= viewportTop {
			continue
		}

		// Calculate window center
		windowCenter := (startLine + endLine) / 2

		// Calculate absolute distance from window center to viewport center
		distance := windowCenter - viewportCenter
		if distance < 0 {
			distance = -distance
		}

		if bestDistance < 0 || distance < bestDistance {
			bestWindow = i
			bestDistance = distance
		}
	}

	if bestDistance >= 0 {
		m.windowCursor = bestWindow
		m.userMovedCursorAway = m.windowCursor < windowCount-1
		return true
	}

	return false
}

var _ tea.Model = (*DisplayModel)(nil)
