package terminal

import (
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

const fullRebuild = -2 // dirtyIndex value meaning all windows need re-render

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     string         // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
	Wrapped bool           // true if window is in wrapped (3-row) mode

	// For diff windows - if non-nil, Content is ignored and Diff is rendered instead
	Diff *DiffContainer

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
// tag is the TLV tag, content is the styled string (already styled by writeColored).
// Reasoning windows are wrapped by default.
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
	window := &Window{
		ID:        id,
		Tag:       tag,
		Content:   content,
		Style:     wb.borderStyle,
		Wrapped:   tag == stream.TagTextReasoning,
		LineWidth: 0, // Will be computed on first render
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.markDirty(len(wb.Windows) - 1)
}

// AppendDiff adds a diff window with side-by-side old/new content.
// The window will be rendered with adaptive width on each render.
func (wb *WindowBuffer) AppendDiff(id string, path string, lines []DiffLinePair) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Create diff container
	diff := &DiffContainer{
		Path:  path,
		Lines: lines,
	}

	// Create window with diff
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

// markDirty marks a window as needing re-render. If another window is already dirty, triggers full rebuild.
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.dirtyIndex >= 0 && wb.dirtyIndex != idx {
		wb.dirtyIndex = fullRebuild
	} else {
		wb.dirtyIndex = idx
	}
}

// IsDiffWindow returns true if the window is a diff window
func (w *Window) IsDiffWindow() bool {
	return w.Diff != nil
}

// getOrBuildLines returns wrapped lines, using cache if valid or rebuilding if needed.
// This is the core of the incremental wrap optimization.
func (w *Window) getOrBuildLines(content string, width int) []string {
	// Check if cached lines are still valid
	if w.LineWidth == width && len(w.Lines) > 0 {
		return w.Lines
	}

	// Cache miss or width changed - rebuild lines
	w.Lines = wrapLines(content, width)
	w.LineWidth = width
	return w.Lines
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
// Ensures cache is built first when dirty, so the count is accurate.
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
// Returns true if toggled successfully, false if index is invalid.
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
// The toolCallID should match the window ID.
func (wb *WindowBuffer) UpdateToolStatus(toolCallID string, status string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[toolCallID]; ok {
		wb.Windows[idx].Status = status
		// Invalidate line cache since status affects indicator prefix
		wb.Windows[idx].LineWidth = 0
		wb.markDirty(idx)
	}
}
