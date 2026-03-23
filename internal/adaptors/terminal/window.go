package terminal

// Window buffer, rendering, and display for the terminal UI.
// Provides virtual scrolling, incremental updates, diff visualization,
// and viewport management.

import (
	"strings"
	"sync"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Rebuild State (replaces sentinel values)
// ============================================================================

// rebuildState represents the cache invalidation state for WindowBuffer.
type rebuildState int

const (
	rebuildClean rebuildState = iota // No windows need re-rendering
	rebuildAll                       // All windows need re-rendering
	rebuildOne                       // Only one window needs re-rendering
)

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     string         // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
	Folded  bool           // true if window is in folded (collapsed) mode showing only first/last lines

	// For diff windows - if non-nil, Content is ignored and Diff is rendered instead
	Diff *DiffContainer

	// For write_file windows - if non-nil, Content is ignored and WriteFile is rendered instead
	WriteFile *WriteFileContainer

	// Status indicator for tool windows
	Status ToolStatus // success, error, pending, or none

	// Cached wrapped lines for incremental wrap optimization
	Lines     []string // wrapped display lines (cached for O(1) delta append)
	LineWidth int      // width used for wrapping (invalidated on resize)

	// Cached rendering state
	lastContentLen  int    // length of content when last rendered (for quick change detection)
	lastFolded      bool   // folded state when last rendered (for diff windows)
	cachedRender    string // full output with border
	cachedInnerCont string // inner content before border (for cursor border swap)
	cachedWidth     int    // width used for cached render
}

// IsDiffWindow returns true if the window is a diff window
func (w *Window) IsDiffWindow() bool {
	return w.Diff != nil
}

// IsWriteFileWindow returns true if the window is a write_file window
func (w *Window) IsWriteFileWindow() bool {
	return w.WriteFile != nil
}

// ============================================================================
// WindowBuffer
// ============================================================================

// WindowBuffer holds a sequence of windows in order of creation.
type WindowBuffer struct {
	mu           sync.Mutex
	Windows      []*Window
	idIndex      map[string]int
	width        int
	borderStyle  lipgloss.Style
	cursorStyle  lipgloss.Style
	styles       *Styles      // styles for diff rendering
	lineHeights  []int        // cached line heights for each window (after rendering)
	totalLines   int          // total lines across all windows
	rebuild      rebuildState // cache invalidation state
	rebuildIndex int          // window index when rebuild == rebuildOne
	cachedRender string       // cached full render of all windows

	// Virtual rendering state
	viewportYOffset int // current viewport scroll position (0-indexed line number)
	viewportHeight  int // viewport height in lines (0 = disabled, use full render)
}

// NewWindowBuffer creates a new window buffer with given width and styles.
func NewWindowBuffer(width int, styles *Styles) *WindowBuffer {
	// Dimmed border: rounded border with invisible color (matches background)
	dimmedBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.ColorBase).
		Padding(0, 1)

	// Highlighted border for cursor
	cursorBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.BorderCursor).
		Padding(0, 1)

	return &WindowBuffer{
		Windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		borderStyle: dimmedBorder,
		cursorStyle: cursorBorder,
		styles:      styles,
		lineHeights: []int{},
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
		// Invalidate all line caches since width changed
		for _, w := range wb.Windows {
			w.LineWidth = 0
		}
		wb.rebuild = rebuildAll
	}
}

// Width returns the current window width.
func (wb *WindowBuffer) Width() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.width
}

// AppendOrUpdate adds content to an existing window identified by id,
// or creates a new window if id not found.
func (wb *WindowBuffer) AppendOrUpdate(id string, tag string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	innerWidth := max(0, wb.width-4)

	if idx, ok := wb.idIndex[id]; ok {
		window := wb.Windows[idx]
		window.Content += content

		// Incremental wrap: only rewrap the affected portion
		if window.LineWidth == innerWidth && len(window.Lines) > 0 && innerWidth > 0 {
			// Width unchanged - incrementally wrap delta
			window.Lines = appendDeltaToLines(window.Lines, content, innerWidth)
		} else {
			// Width changed or no lines yet - full rewrap needed
			window.LineWidth = 0 // Invalidate, will be recomputed on render
		}
		wb.markDirty(idx)
		return
	}
	// User and Assistant messages should NOT be folded (show full content)
	// All other window types default to folded (collapsed view)
	folded := true
	if tag == stream.TagTextUser || tag == stream.TagTextAssistant {
		folded = false
	}

	window := &Window{
		ID:        id,
		Tag:       tag,
		Content:   content,
		Style:     wb.borderStyle,
		Folded:    folded,
		LineWidth: 0, // Will be computed on first render
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// AppendDiff adds a diff window with side-by-side old/new content.
func (wb *WindowBuffer) AppendDiff(id string, path string, lines []DiffLinePair) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	diff := &DiffContainer{
		Path:  path,
		Lines: lines,
	}

	window := &Window{
		ID:     id,
		Tag:    stream.TagFunctionNotify,
		Style:  wb.borderStyle,
		Diff:   diff,
		Folded: true, // Enable folding like other windows
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// AppendWriteFile adds a write_file window with path and content.
func (wb *WindowBuffer) AppendWriteFile(id string, path string, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	writeFile := &WriteFileContainer{
		Path:    path,
		Content: content,
	}

	window := &Window{
		ID:        id,
		Tag:       stream.TagFunctionNotify,
		Style:     wb.borderStyle,
		WriteFile: writeFile,
		Folded:    true, // Enable folding like other windows
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// markDirty marks a window as needing re-render.
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.rebuild == rebuildAll {
		return // Already marked for full rebuild
	}
	if wb.rebuild == rebuildOne && wb.rebuildIndex != idx {
		wb.rebuild = rebuildAll // Different window dirty - need full rebuild
	} else {
		wb.rebuild = rebuildOne
		wb.rebuildIndex = idx
	}
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.cachedRender = ""
	wb.rebuild = rebuildAll
}

// GetWindowCount returns the number of windows.
func (wb *WindowBuffer) GetWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.Windows)
}

// GetWindowStartLine returns the starting line number (0-indexed) for the window at given index.
func (wb *WindowBuffer) GetWindowStartLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	startLine := 0
	for i := range windowIndex {
		startLine += wb.lineHeights[i]
	}
	return startLine
}

// GetWindowEndLine returns the ending line number (0-indexed, exclusive) for the window at given index.
func (wb *WindowBuffer) GetWindowEndLine(windowIndex int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0
	}

	endLine := 0
	for i := 0; i <= windowIndex; i++ {
		endLine += wb.lineHeights[i]
	}
	return endLine
}

// GetTotalLines returns the total number of lines across all windows.
func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if wb.rebuild != rebuildClean {
		if wb.rebuild == rebuildAll {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.rebuildIndex)
		}
		wb.rebuild = rebuildClean
	}
	return wb.totalLines
}

// ToggleFold toggles the fold state of the window at the given index.
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

// UpdateToolStatus updates the status indicator for a tool window.
func (wb *WindowBuffer) UpdateToolStatus(toolCallID string, status ToolStatus) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[toolCallID]; ok {
		w := wb.Windows[idx]
		w.Status = status
		w.LineWidth = 0 // Invalidate line cache
		if (status == ToolStatusSuccess || status == ToolStatusError) && len(w.Content) > 0 {
			if isWriteFileWindow(w.Content) || w.IsWriteFileWindow() {
				w.Folded = true
			}
		}
		wb.markDirty(idx)
	}
}

// isWriteFileWindow checks if window content is from write_file tool (legacy, for Content-based windows)
func isWriteFileWindow(content string) bool {
	if len(content) < 10 {
		return false
	}
	return strings.Contains(content[:min(30, len(content))], "write_file")
}

// getOrBuildLines returns wrapped lines, using cache if valid or rebuilding if needed.
func (w *Window) getOrBuildLines(content string, width int) []string {
	if w.LineWidth == width && len(w.Lines) > 0 {
		return w.Lines
	}
	w.Lines = wrapLines(content, width)
	w.LineWidth = width
	return w.Lines
}

// ============================================================================
// Virtual Rendering
// ============================================================================

// SetViewportPosition updates the viewport scroll position and height.
func (wb *WindowBuffer) SetViewportPosition(yOffset, height int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.viewportYOffset = yOffset
	wb.viewportHeight = height
}

// GetTotalLinesVirtual returns total lines, ensuring lineHeights are calculated.
func (wb *WindowBuffer) GetTotalLinesVirtual() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
}

// ensureLineHeights calculates lineHeights if needed.
func (wb *WindowBuffer) ensureLineHeights() {
	if wb.rebuild == rebuildClean && len(wb.lineHeights) == len(wb.Windows) {
		return
	}

	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	if wb.rebuild == rebuildOne {
		wb.rebuildOneWindowLineHeight(wb.rebuildIndex)
	} else if wb.rebuild == rebuildAll {
		wb.rebuildAllLineHeights()
	}
	wb.rebuild = rebuildClean
}

// rebuildOneWindowLineHeight re-renders only one window and updates its line height.
func (wb *WindowBuffer) rebuildOneWindowLineHeight(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)
	styled := w.Style.Width(wb.width).Render(innerContent)
	newLineCount := strings.Count(styled, "\n") + 1

	oldLineCount := wb.lineHeights[idx]
	wb.totalLines += newLineCount - oldLineCount

	wb.lineHeights[idx] = newLineCount
	w.cachedRender = styled
	w.cachedInnerCont = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastFolded = w.Folded
}

// rebuildAllLineHeights rebuilds all window line heights.
func (wb *WindowBuffer) rebuildAllLineHeights() {
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	innerWidth := max(0, wb.width-4)
	for i, w := range wb.Windows {
		innerContent := wb.renderWindowContent(w, innerWidth)
		styled := w.Style.Width(wb.width).Render(innerContent)
		lineCount := strings.Count(styled, "\n") + 1

		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount

		w.cachedRender = styled
		w.cachedInnerCont = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
		w.lastFolded = w.Folded
	}
}

// getVirtualRender returns rendered content using virtual rendering.
func (wb *WindowBuffer) getVirtualRender(cursorIndex int) string {
	wb.ensureLineHeights()

	if len(wb.Windows) == 0 {
		return ""
	}

	bufferWindows := 5
	viewportLines := wb.viewportHeight
	if viewportLines < 10 {
		viewportLines = 10
	}

	startLine := wb.viewportYOffset - viewportLines
	if startLine < 0 {
		startLine = 0
	}
	endLine := wb.viewportYOffset + wb.viewportHeight + viewportLines

	startWindow := wb.findWindowAtLine(startLine)
	endWindow := wb.findWindowAtLine(endLine)

	startWindow = max(0, startWindow-bufferWindows)
	endWindow = min(len(wb.Windows)-1, endWindow+bufferWindows)

	var sb strings.Builder

	for i := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		if i >= startWindow && i <= endWindow {
			styled := wb.renderWindowCached(i, cursorIndex == i)
			sb.WriteString(styled)
		} else {
			lineCount := wb.lineHeights[i]
			for j := 0; j < lineCount; j++ {
				if j > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(" ")
			}
		}
	}

	return sb.String()
}

// findWindowAtLine returns the window index containing the given line.
func (wb *WindowBuffer) findWindowAtLine(line int) int {
	currentLine := 0
	for i, h := range wb.lineHeights {
		if currentLine+h > line {
			return i
		}
		currentLine += h
	}
	return len(wb.Windows) - 1
}

// renderWindowCached renders a single window, using cache if valid.
func (wb *WindowBuffer) renderWindowCached(i int, isCursor bool) string {
	w := wb.Windows[i]

	cacheValid := w.cachedRender != "" && w.cachedWidth == wb.width &&
		(w.IsDiffWindow() && w.Folded == w.lastFolded || !w.IsDiffWindow() && len(w.Content) == w.lastContentLen)

	if cacheValid {
		if isCursor {
			return wb.cursorStyle.Width(wb.width).Render(w.cachedInnerCont)
		}
		return w.cachedRender
	}

	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)

	if isCursor {
		styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
		w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
		w.cachedInnerCont = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
		w.lastFolded = w.Folded
		return styled
	}

	styled := w.Style.Width(wb.width).Render(innerContent)
	w.cachedRender = styled
	w.cachedInnerCont = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastFolded = w.Folded
	return styled
}

// ============================================================================
// Full Rendering
// ============================================================================

// GetAll returns the concatenated rendered windows as a single string.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if wb.viewportHeight > 0 {
		return wb.getVirtualRender(cursorIndex)
	}

	if wb.rebuild != rebuildClean {
		if wb.rebuild == rebuildAll {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.rebuildIndex)
		}
		wb.rebuild = rebuildClean
	}

	if cursorIndex < 0 || cursorIndex >= len(wb.Windows) {
		return wb.cachedRender
	}

	return wb.renderWithCursor(cursorIndex)
}

// rebuildCache rebuilds the cached render for all windows
func (wb *WindowBuffer) rebuildCache() {
	var sb strings.Builder
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		styled := wb.renderAndCacheWindow(i, w)
		sb.WriteString(styled)
	}
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}
	wb.cachedRender = sb.String()
}

// rebuildOneWindow re-renders only the window at idx.
func (wb *WindowBuffer) rebuildOneWindow(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	oldLineHeight := wb.lineHeights[idx]

	styled := wb.renderAndCacheWindow(idx, w)
	newLineHeight := strings.Count(styled, "\n") + 1
	wb.lineHeights[idx] = newLineHeight

	wb.totalLines += newLineHeight - oldLineHeight

	var sb strings.Builder
	for i := 0; i < len(wb.Windows); i++ {
		if i > 0 {
			sb.WriteString("\n")
		}
		if i == idx {
			sb.WriteString(styled)
		} else {
			sb.WriteString(wb.Windows[i].cachedRender)
		}
	}
	wb.cachedRender = sb.String()
}

// renderAndCacheWindow renders a window and updates its cache.
func (wb *WindowBuffer) renderAndCacheWindow(i int, w *Window) string {
	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)
	styled := w.Style.Width(wb.width).Render(innerContent)
	lineCount := strings.Count(styled, "\n") + 1

	if i < len(wb.lineHeights) {
		wb.lineHeights[i] = lineCount
	}
	w.cachedRender = styled
	w.cachedInnerCont = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastFolded = w.Folded
	return styled
}

// isCacheValid checks if a window's cache is valid
func (wb *WindowBuffer) isCacheValid(w *Window) bool {
	if w.cachedWidth != wb.width {
		return false
	}
	if w.IsDiffWindow() {
		return w.Folded == w.lastFolded
	}
	return len(w.Content) == w.lastContentLen
}

// renderWithCursor renders all windows with cursor highlighting.
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		if i != cursorIndex {
			if w.cachedRender != "" && wb.isCacheValid(w) {
				sb.WriteString(w.cachedRender)
			} else {
				innerWidth := max(0, wb.width-4)
				innerContent := wb.renderWindowContent(w, innerWidth)
				styled := w.Style.Width(wb.width).Render(innerContent)
				w.cachedRender = styled
				w.cachedInnerCont = innerContent
				w.cachedWidth = wb.width
				w.lastContentLen = len(w.Content)
				w.lastFolded = w.Folded
				sb.WriteString(styled)
			}
		} else {
			if w.cachedInnerCont != "" && wb.isCacheValid(w) {
				sb.WriteString(wb.cursorStyle.Width(wb.width).Render(w.cachedInnerCont))
			} else {
				innerWidth := max(0, wb.width-4)
				innerContent := wb.renderWindowContent(w, innerWidth)
				styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
				w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
				w.cachedInnerCont = innerContent
				w.cachedWidth = wb.width
				w.lastContentLen = len(w.Content)
				w.lastFolded = w.Folded
				sb.WriteString(styled)
			}
		}
	}
	return sb.String()
}

// ============================================================================
// Window Content Rendering
// ============================================================================

// renderWindowContent renders the content of a window
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	var fullContent string

	switch {
	case w.IsWriteFileWindow():
		fullContent = RenderWriteFileContent(w.WriteFile, w.Status, wb.styles)
	case w.IsDiffWindow():
		fullContent = RenderDiffContent(w.Diff, w.Status, wb.styles)
	default:
		fullContent = wb.renderGenericContent(w, innerWidth)
	}

	// Apply folding if needed
	if w.Folded {
		return wb.applyFolding(fullContent, innerWidth)
	}
	return fullContent
}

// applyFolding collapses content to first line + indicator + last 3 lines if > 5 lines
func (wb *WindowBuffer) applyFolding(content string, innerWidth int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 5 {
		return content
	}

	firstLine := lines[0]
	lastThreeLines := lines[len(lines)-3:]

	wrapIndicator := lipgloss.NewStyle().
		Foreground(wb.styles.ColorBase).
		Render(strings.Repeat("⁝", innerWidth))

	return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
}

// renderGenericContent renders a generic tool window content
func (wb *WindowBuffer) renderGenericContent(w *Window, innerWidth int) string {
	content := w.Content
	if w.Tag == stream.TagFunctionNotify {
		content = w.Status.Indicator(wb.styles) + content
	}

	lines := w.getOrBuildLines(content, innerWidth)
	return strings.Join(lines, "\n")
}

// ============================================================================
// Line Wrapping
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

	lastLine := lines[len(lines)-1]
	combined := lastLine + delta
	newLines := wrapLines(combined, width)

	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	deltaParts := strings.Split(delta, "\n")

	for i, part := range deltaParts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastLine := lines[len(lines)-1]
				combined := lastLine + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:len(lines)-1], newLines...)
			}
		} else {
			newLines := wrapLines(part, width)
			lines = append(lines, newLines...)
		}
	}

	return lines
}

// ============================================================================
// DisplayModel - Viewport over WindowBuffer
// ============================================================================

// DisplayModel holds the viewport over WindowBuffer content.
// It manages the visible portion of the buffer and cursor navigation.
type DisplayModel struct {
	viewport            viewport.Model
	windowBuffer        *WindowBuffer
	styles              *Styles
	width               int
	height              int
	windowCursor        int    // index of the currently selected window (-1 means no selection)
	userMovedCursorAway bool   // true when user moved cursor away from last (k, g, H, L, M, etc.)
	displayFocused      bool   // true when display has focus (for showing cursor highlight)
	lastContent         string // cached content to avoid unnecessary updates
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
		userMovedCursorAway: false, // follow by default
		displayFocused:      false,
	}
}

// Init initializes the display
func (m DisplayModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the display (WindowSizeMsg only; content updates via updateContent)
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

// SetDisplayFocused sets whether the display is focused (for cursor highlight)
func (m *DisplayModel) SetDisplayFocused(focused bool) {
	m.displayFocused = focused
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

	// For virtual rendering, we need to calculate the correct YOffset first
	// This is a chicken-and-egg problem: GotoBottom needs content, but virtual rendering needs YOffset
	// Solution: Get total lines first, calculate YOffset, then render
	totalLines := m.windowBuffer.GetTotalLinesVirtual()
	viewportHeight := m.viewport.Height()

	// Calculate target YOffset
	targetYOffset := m.viewport.YOffset()
	if m.shouldFollow() && totalLines > viewportHeight {
		targetYOffset = totalLines - viewportHeight
		if targetYOffset < 0 {
			targetYOffset = 0
		}
	}

	// Set viewport position for virtual rendering
	m.windowBuffer.SetViewportPosition(targetYOffset, viewportHeight)

	// Now render with the correct position
	newContent := m.windowBuffer.GetAll(cursorIndex)

	// Skip update if content hasn't changed
	if newContent == m.lastContent {
		return
	}
	m.lastContent = newContent

	m.viewport.SetContent(newContent)

	// Sync to bottom if in follow mode.
	// Note: SetContent already adjusts YOffset if it's beyond maxYOffset(),
	// so we don't need to restore the old YOffset when not in follow mode.
	// This fixes a bug where restoring an invalid old YOffset after resize
	// would cause the display to jump to the wrong position.
	if m.shouldFollow() {
		m.viewport.GotoBottom()
	}
}

// ScrollDown scrolls down by lines.
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
	height := max(0, totalHeight-LayoutGap)
	m.viewport.SetHeight(height)
	m.updateContent()
}

// shouldFollow returns true when viewport and cursor should auto-follow new content.
// Follow when user has not moved cursor away from last window (k, g, H, L, M, etc.).
func (m *DisplayModel) shouldFollow() bool {
	return !m.userMovedCursorAway
}

// GetWindowCursor returns the current window cursor index (-1 if none).
func (m *DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// SetWindowCursor sets the window cursor to a specific index.
// Pass -1 to deselect all windows.
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

// MoveWindowCursorDown moves the window cursor down by one window.
// Returns true if the cursor moved, false if already at the last window.
func (m *DisplayModel) MoveWindowCursorDown() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}
	// Already at last window, don't move
	if m.windowCursor == windowCount-1 {
		return false
	}
	// If cursor is invalid or before last, move down
	if m.windowCursor < 0 {
		m.windowCursor = 0
	} else {
		m.windowCursor++
	}
	if m.windowCursor == windowCount-1 {
		m.userMovedCursorAway = false
	} else {
		m.userMovedCursorAway = true
	}
	return true
}

// MoveWindowCursorUp moves the window cursor up by one window.
// Returns true if the cursor moved, false if already at the first window.
func (m *DisplayModel) MoveWindowCursorUp() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}
	// Already at first window, don't move
	if m.windowCursor == 0 {
		return false
	}
	// If cursor is invalid, set to first
	if m.windowCursor < 0 {
		m.windowCursor = 0
		return true
	}
	m.windowCursor--
	m.userMovedCursorAway = true
	return true
}

// EnsureCursorVisible scrolls the viewport to make the cursor window fully visible.
func (m *DisplayModel) EnsureCursorVisible() {
	if m.windowCursor < 0 {
		return
	}

	startLine := m.windowBuffer.GetWindowStartLine(m.windowCursor)
	endLine := m.windowBuffer.GetWindowEndLine(m.windowCursor)

	viewportTop := m.viewport.YOffset()
	viewportHeight := m.viewport.Height()
	viewportBottom := viewportTop + viewportHeight

	// If window is above viewport, scroll up to show it
	if startLine < viewportTop {
		m.viewport.SetYOffset(startLine)
		return
	}

	// If window end is below viewport, scroll down to show it fully
	if endLine > viewportBottom {
		newTop := endLine - viewportHeight
		m.viewport.SetYOffset(newTop)
	}
}

// ValidateCursor ensures the window cursor is within valid bounds and visible.
// This should be called after resize events when window layout changes.
func (m *DisplayModel) ValidateCursor() {
	windowCount := m.windowBuffer.GetWindowCount()

	// Clamp cursor to valid range
	if m.windowCursor >= windowCount {
		m.windowCursor = windowCount - 1
	}
	if m.windowCursor < -1 {
		m.windowCursor = -1
	}

	// Ensure cursor is visible if we have a valid cursor
	if m.windowCursor >= 0 && windowCount > 0 {
		m.EnsureCursorVisible()
	}
}

// SetCursorToLastWindow sets the cursor to the last window.
func (m *DisplayModel) SetCursorToLastWindow() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
	} else {
		m.windowCursor = windowCount - 1
		m.userMovedCursorAway = false
	}
}

// ToggleWindowFold toggles the fold state of the currently selected window.
// Returns true if a window was toggled, false if no window is selected.
func (m *DisplayModel) ToggleWindowFold() bool {
	if m.windowCursor < 0 {
		return false
	}
	return m.windowBuffer.ToggleFold(m.windowCursor)
}

// MarkUserScrolled marks that the user has manually scrolled away from the bottom.
// This prevents auto-follow until the user returns to the last window.
func (m *DisplayModel) MarkUserScrolled() {
	m.userMovedCursorAway = true
}

// MoveWindowCursorToTop moves the window cursor to the window at the top of the visible screen.
// Returns true if the cursor moved, false otherwise.
func (m *DisplayModel) MoveWindowCursorToTop() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportTop := m.viewport.YOffset()

	// Find the window that contains or is closest to the top of the viewport
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		// If this window overlaps with viewport top
		if startLine <= viewportTop && endLine > viewportTop {
			m.windowCursor = i
			m.userMovedCursorAway = true
			return true
		}
		// If this window is below viewport top (first visible window)
		if startLine >= viewportTop {
			m.windowCursor = i
			m.userMovedCursorAway = true
			return true
		}
	}
	return false
}

// MoveWindowCursorToBottom moves the window cursor to the window at the bottom of the visible screen.
// Returns true if the cursor moved, false otherwise.
func (m *DisplayModel) MoveWindowCursorToBottom() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportBottom := m.viewport.YOffset() + m.viewport.Height()

	// Find the window that contains or is closest to the bottom of the viewport
	// Iterate in reverse to find the first window from bottom
	for i := windowCount - 1; i >= 0; i-- {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		// If this window overlaps with viewport bottom
		if startLine < viewportBottom && endLine >= viewportBottom {
			m.windowCursor = i
			// Only set userMovedCursorAway if not selecting the actual last window
			if i < windowCount-1 {
				m.userMovedCursorAway = true
			} else {
				m.userMovedCursorAway = false
			}
			return true
		}
		// If this window is above viewport bottom (last visible window)
		if endLine <= viewportBottom {
			m.windowCursor = i
			if i < windowCount-1 {
				m.userMovedCursorAway = true
			} else {
				m.userMovedCursorAway = false
			}
			return true
		}
	}
	return false
}

// MoveWindowCursorToCenter moves the window cursor to the middle window among visible windows.
// Returns true if the cursor moved, false otherwise.
func (m *DisplayModel) MoveWindowCursorToCenter() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	// Get viewport bounds
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()

	// Find all windows that are at least partially visible
	var visibleWindows []int
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		// Window is visible if it overlaps with viewport
		if startLine < viewportBottom && endLine > viewportTop {
			visibleWindows = append(visibleWindows, i)
		}
	}

	if len(visibleWindows) == 0 {
		return false
	}

	// Select the middle window among visible windows
	middleIndex := len(visibleWindows) / 2
	targetWindow := visibleWindows[middleIndex]

	m.windowCursor = targetWindow
	m.userMovedCursorAway = (targetWindow < windowCount-1)
	return true
}

var _ tea.Model = (*DisplayModel)(nil)
