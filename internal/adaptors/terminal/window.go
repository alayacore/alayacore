package terminal

// Window buffer and rendering for the terminal display.
// Provides virtual scrolling, incremental updates, and diff visualization.

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

const fullRebuild = -2 // dirtyIndex value meaning all windows need re-render

// CRITICAL: markDirty sentinel preservation
// The markDirty function uses sentinel value fullRebuild = -2. Once set, it must NOT be
// overwritten by a single-window index. The check `if wb.dirtyIndex == fullRebuild { return }`
// is critical - without it, rapid window creation (e.g., session loading) would set
// fullRebuild, then overwrite it with the last window index, causing only the last
// window to render.

// ============================================================================
// Window Types
// ============================================================================

// Status constants for diff display
const (
	statusSuccess = "success"
	statusError   = "error"
	statusPending = "pending"
)

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     string         // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
	Wrapped bool           // true if window is in wrapped (3-row) mode

	// For diff windows - if non-nil, Content is ignored and Diff is rendered instead
	Diff *DiffContainer

	// For write_file windows - if non-nil, Content is ignored and WriteFile is rendered instead
	WriteFile *WriteFileContainer

	// Status indicator for tool windows
	Status string // "success", "error", "pending", or "" (default: dimmed hollow dot for loaded sessions)

	// Cached wrapped lines for incremental wrap optimization
	Lines     []string // wrapped display lines (cached for O(1) delta append)
	LineWidth int      // width used for wrapping (invalidated on resize)

	// Cached rendering state
	lastContentLen     int    // length of content when last rendered (for quick change detection)
	lastWrapped        bool   // wrapped state when last rendered (for diff windows)
	cachedRender       string // full output with border
	cachedInnerContent string // inner content before border (for cursor border swap)
	cachedWidth        int    // width used for cached render
}

// DiffContainer holds two panes side by side for diff display
type DiffContainer struct {
	Path  string         // file path for header
	Lines []DiffLinePair // raw line pairs
}

// DiffLinePair represents a pair of old/new lines in a diff
type DiffLinePair struct {
	Old string
	New string
}

// WriteFileContainer holds the path and content for write_file display
type WriteFileContainer struct {
	Path    string // file path for header
	Content string // file content
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
	styles       *Styles // styles for diff rendering
	lineHeights  []int   // cached line heights for each window (after rendering)
	totalLines   int     // total lines across all windows
	dirtyIndex   int     // -1 = clean, -2 = full rebuild needed, >=0 = only this window dirty
	cachedRender string  // cached full render of all windows

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
		wb.dirtyIndex = fullRebuild
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
	// User and Assistant messages should NOT be wrapped (show full content)
	// All other window types default to wrapped (folded/collapsed)
	wrapped := true
	if tag == stream.TagTextUser || tag == stream.TagTextAssistant {
		wrapped = false
	}

	window := &Window{
		ID:        id,
		Tag:       tag,
		Content:   content,
		Style:     wb.borderStyle,
		Wrapped:   wrapped,
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
		ID:      id,
		Tag:     stream.TagFunctionNotify,
		Style:   wb.borderStyle,
		Diff:    diff,
		Wrapped: true, // Enable folding like other windows
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
		Wrapped:   true, // Enable folding like other windows
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// markDirty marks a window as needing re-render.
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.dirtyIndex == fullRebuild {
		return // Already marked for full rebuild
	}
	if wb.dirtyIndex >= 0 && wb.dirtyIndex != idx {
		wb.dirtyIndex = fullRebuild // Different window dirty - need full rebuild
	} else {
		wb.dirtyIndex = idx
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
	wb.dirtyIndex = fullRebuild
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

	if wb.dirtyIndex != -1 {
		if wb.dirtyIndex == fullRebuild {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.dirtyIndex)
		}
		wb.dirtyIndex = -1
	}
	return wb.totalLines
}

// ToggleWrap toggles the wrap state of the window at the given index.
func (wb *WindowBuffer) ToggleWrap(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.Windows) {
		return false
	}

	wb.Windows[windowIndex].Wrapped = !wb.Windows[windowIndex].Wrapped
	wb.markDirty(windowIndex)
	return true
}

// UpdateToolStatus updates the status indicator for a tool window.
func (wb *WindowBuffer) UpdateToolStatus(toolCallID string, status string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[toolCallID]; ok {
		w := wb.Windows[idx]
		w.Status = status
		w.LineWidth = 0 // Invalidate line cache
		if (status == statusSuccess || status == statusError) && len(w.Content) > 0 {
			if isWriteFileWindow(w.Content) || w.IsWriteFileWindow() {
				w.Wrapped = true
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
	if wb.dirtyIndex == -1 && len(wb.lineHeights) == len(wb.Windows) {
		return
	}

	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	if wb.dirtyIndex >= 0 {
		wb.rebuildOneWindowLineHeight(wb.dirtyIndex)
	} else if wb.dirtyIndex == fullRebuild {
		wb.rebuildAllLineHeights()
	}
	wb.dirtyIndex = -1
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
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped
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
		w.cachedInnerContent = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
		w.lastWrapped = w.Wrapped
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
		(w.IsDiffWindow() && w.Wrapped == w.lastWrapped || !w.IsDiffWindow() && len(w.Content) == w.lastContentLen)

	if cacheValid {
		if isCursor {
			return wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent)
		}
		return w.cachedRender
	}

	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)

	if isCursor {
		styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
		w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
		w.cachedInnerContent = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
		w.lastWrapped = w.Wrapped
		return styled
	}

	styled := w.Style.Width(wb.width).Render(innerContent)
	w.cachedRender = styled
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped
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

	if wb.dirtyIndex != -1 {
		if wb.dirtyIndex == fullRebuild {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.dirtyIndex)
		}
		wb.dirtyIndex = -1
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
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped
	return styled
}

// isCacheValid checks if a window's cache is valid
func (wb *WindowBuffer) isCacheValid(w *Window) bool {
	if w.cachedWidth != wb.width {
		return false
	}
	if w.IsDiffWindow() {
		return w.Wrapped == w.lastWrapped
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
				w.cachedInnerContent = innerContent
				w.cachedWidth = wb.width
				w.lastContentLen = len(w.Content)
				w.lastWrapped = w.Wrapped
				sb.WriteString(styled)
			}
		} else {
			if w.cachedInnerContent != "" && wb.isCacheValid(w) {
				sb.WriteString(wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent))
			} else {
				innerWidth := max(0, wb.width-4)
				innerContent := wb.renderWindowContent(w, innerWidth)
				styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
				w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
				w.cachedInnerContent = innerContent
				w.cachedWidth = wb.width
				w.lastContentLen = len(w.Content)
				w.lastWrapped = w.Wrapped
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
		fullContent = wb.renderWriteFileContent(w.WriteFile, w.Status)
	case w.IsDiffWindow():
		fullContent = wb.renderDiffContent(w.Diff, w.Status)
	default:
		fullContent = wb.renderGenericContent(w, innerWidth)
	}

	// Apply folding if needed
	if w.Wrapped {
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
		content = wb.renderStatusIndicator(w.Status) + content
	}

	lines := w.getOrBuildLines(content, innerWidth)
	return strings.Join(lines, "\n")
}

// renderStatusIndicator returns the status indicator string for a tool window
func (wb *WindowBuffer) renderStatusIndicator(status string) string {
	switch status {
	case statusSuccess:
		return lipgloss.NewStyle().
			Foreground(wb.styles.ColorSuccess).
			Render("• ")
	case statusError:
		return lipgloss.NewStyle().
			Foreground(wb.styles.ColorError).
			Render("• ")
	case statusPending:
		return lipgloss.NewStyle().
			Foreground(wb.styles.ColorDim).
			Render("• ")
	default:
		return lipgloss.NewStyle().
			Foreground(wb.styles.ColorDim).
			Render("· ")
	}
}

// ============================================================================
// Diff Rendering
// ============================================================================

// renderWriteFileContent renders a write_file container
func (wb *WindowBuffer) renderWriteFileContent(wf *WriteFileContainer, status string) string {
	lines := make([]string, 0, 2)

	header := wb.renderStatusIndicator(status) + wb.styles.Tool.Render("write_file: ") + wb.styles.ToolContent.Render(wf.Path)
	lines = append(lines, header)

	// Render content lines with Text style
	contentLines := strings.Split(wf.Content, "\n")
	for _, line := range contentLines {
		lines = append(lines, wb.styles.Text.Render(expandTabs(line)))
	}

	return strings.Join(lines, "\n")
}

// renderDiffContent renders a diff container in unified diff style
func (wb *WindowBuffer) renderDiffContent(diff *DiffContainer, status string) string {
	lines := make([]string, 0, 1+len(diff.Lines))

	header := wb.renderStatusIndicator(status) + wb.styles.Tool.Render("edit_file: ") + wb.styles.ToolContent.Render(diff.Path)
	lines = append(lines, header)

	for _, pair := range diff.Lines {
		oldPart := strings.ReplaceAll(expandTabs(pair.Old), "\n", "\\n")
		newPart := strings.ReplaceAll(expandTabs(pair.New), "\n", "\\n")

		oldEmpty := pair.Old == ""
		newEmpty := pair.New == ""
		isSame := pair.Old == pair.New

		switch {
		case isSame:
			lines = append(lines, wb.styles.Text.Render("  "+oldPart))
		case oldEmpty:
			lines = append(lines, wb.styles.DiffAdd.Render("+ "+newPart))
		case newEmpty:
			lines = append(lines, wb.styles.DiffRemove.Render("- "+oldPart))
		default:
			lines = append(lines, wb.styles.DiffRemove.Render("- "+oldPart))
			lines = append(lines, wb.styles.DiffAdd.Render("+ "+newPart))
		}
	}

	return strings.Join(lines, "\n")
}

// expandTabs converts tabs to spaces, treating tabs as 8-space width
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		} else if r == '\n' {
			result.WriteRune(r)
			col = 0
		} else {
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
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
