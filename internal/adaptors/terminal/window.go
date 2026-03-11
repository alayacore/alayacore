package terminal

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

const fullRebuild = -2 // dirtyIndex value meaning all windows need re-render

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     byte           // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
	Wrapped bool           // true if window is in wrapped (3-row) mode

	// For diff windows - if non-nil, Content is ignored and Diff is rendered instead
	Diff *DiffContainer

	// Cached rendering state
	lastContentLen     int    // length of content when last rendered (for quick change detection)
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
	dirtyIndex   int     // -1 = full rebuild, >=0 = only this window dirty (incremental)
	cachedRender string  // cached full render of all windows
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

// NewWindowBuffer creates a new window buffer with given width.
func NewWindowBuffer(width int) *WindowBuffer {
	// Dimmed border: rounded border with invisible color (matches background)
	dimmedBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#1e1e2e")).
		Padding(0, 1)

	// Highlighted border for cursor
	cursorBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#fab387")).
		Padding(0, 1)

	return &WindowBuffer{
		Windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		borderStyle: dimmedBorder,
		cursorStyle: cursorBorder,
		styles:      DefaultStyles(),
		lineHeights: []int{},
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
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
func (wb *WindowBuffer) AppendOrUpdate(id string, tag byte, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		window := wb.Windows[idx]
		window.Content += content
		wb.markDirty(idx)
		return
	}
	window := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Style:   wb.borderStyle,
		Wrapped: tag == stream.TagReasoning,
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
		ID:    id,
		Tag:   stream.TagTool,
		Style: wb.borderStyle,
		Diff:  diff,
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

// GetAll returns the concatenated rendered windows as a single string.
// Uses incremental rendering when only one window changed.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
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

	// If no cursor or cursor out of range, return cached render
	if cursorIndex < 0 || cursorIndex >= len(wb.Windows) {
		return wb.cachedRender
	}

	// Cursor is active - rebuild with cursor highlighting on just that window
	// We use the cached wrapped content but apply different border style
	return wb.renderWithCursor(cursorIndex)
}

// rebuildCache rebuilds the cached render for all windows (without cursor)
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

// rebuildOneWindow re-renders only the window at idx and updates the full cached string.
func (wb *WindowBuffer) rebuildOneWindow(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	// Ensure lineHeights has right length (new window case)
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Re-render the dirty window (don't use totalLines from renderAndCacheWindow)
	styled := wb.renderAndCacheWindow(idx, w)
	wb.lineHeights[idx] = strings.Count(styled, "\n") + 1

	// Rebuild totalLines from all lineHeights
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}

	// Rebuild cachedRender by concatenating: [before] + [new] + [after]
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

// renderAndCacheWindow renders a window, updates its cache and lineHeights[i], returns styled string.
// Stores cachedInnerContent for cursor border swap (avoid re-calling renderWindowContent).
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
	return styled
}

// renderWithCursor renders all windows with cursor highlighting on the specified window.
// For the cursor window: reuse cachedInnerContent, only swap border style (no lipgloss.Wrap).
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Non-cursor window: use cached render if valid
		if i != cursorIndex {
			if w.cachedRender != "" && w.cachedWidth == wb.width &&
				(w.IsDiffWindow() || len(w.Content) == w.lastContentLen) {
				sb.WriteString(w.cachedRender)
				continue
			}
			// Fallback: re-render and cache
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := w.Style.Width(wb.width).Render(innerContent)
			w.cachedRender = styled
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			sb.WriteString(styled)
			continue
		}

		// Cursor window: border swap - reuse cachedInnerContent, avoid renderWindowContent
		if w.cachedInnerContent != "" && w.cachedWidth == wb.width &&
			(w.IsDiffWindow() || len(w.Content) == w.lastContentLen) {
			sb.WriteString(wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent))
		} else {
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
			w.cachedRender = w.Style.Width(wb.width).Render(innerContent) // cache dimmed for next time
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			sb.WriteString(styled)
		}
	}
	return sb.String()
}

// renderWindowContent renders the content of a window (wrapping, truncation for wrapped mode)
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	// Handle diff windows
	if w.IsDiffWindow() {
		return wb.renderDiffContent(w.Diff, innerWidth)
	}

	if w.Wrapped {
		// In wrapped mode, show up to 5 lines
		wrappedContent := lipgloss.Wrap(w.Content, innerWidth, " ")

		// Check if content spans more than 5 lines (needs truncation)
		lines := strings.Split(wrappedContent, "\n")
		if len(lines) > 5 {
			// Show: first line, dotted separator, last 3 lines (5 lines total)
			firstLine := lines[0]
			lastThreeLines := lines[len(lines)-3:]

			// Create subtle dotted separator across full width
			wrapIndicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#45475a")).
				Render(strings.Repeat("·", innerWidth))

			// Show first line, separator, last 3 lines
			return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
		}
		// Content fits in 5 lines or less, just show wrapped content
		return wrappedContent
	}
	return lipgloss.Wrap(w.Content, innerWidth, " ")
}

// renderDiffContent renders a diff container side by side
func (wb *WindowBuffer) renderDiffContent(diff *DiffContainer, innerWidth int) string {
	var lines []string

	// Add header with file path
	lines = append(lines, wb.styles.Tool.Render("edit_file: ")+wb.styles.ToolContent.Render(diff.Path))

	// Calculate width for each side
	// Line format: "= " + paddedOld + " " + "|" + " " + "+ " + newPart
	// Total: 2 + sideWidth + 3 + 2 + sideWidth = 2*sideWidth + 7
	// We need: 2*sideWidth + 7 <= innerWidth
	// So: sideWidth <= (innerWidth - 7) / 2
	sideWidth := (innerWidth - 7) / 2
	if sideWidth < 10 {
		sideWidth = 10 // minimum width
	}

	for _, pair := range diff.Lines {
		// Escape any literal newlines in content (shouldn't happen, but be safe)
		oldPart := strings.ReplaceAll(expandTabs(pair.Old), "\n", "\\n")
		newPart := strings.ReplaceAll(expandTabs(pair.New), "\n", "\\n")

		// Check if one side is empty (different line counts)
		oldEmpty := pair.Old == ""
		newEmpty := pair.New == ""

		// Check if content is the same (before truncation)
		isSame := pair.Old == pair.New

		// Truncate if needed (use display width for proper Unicode handling)
		oldPart = truncateByWidth(oldPart, sideWidth)
		newPart = truncateByWidth(newPart, sideWidth)

		// Pad old part to fixed width (use display width)
		paddedOld := oldPart + strings.Repeat(" ", max(0, sideWidth-lipgloss.Width(oldPart)))

		var left, right string
		if oldEmpty {
			// Old side is empty (new has more lines) - use spaces, no sign
			left = "  " + paddedOld
			right = wb.styles.DiffAdd.Render("+ " + newPart)
		} else if newEmpty {
			// New side is empty (old has more lines) - use spaces, no sign
			left = wb.styles.DiffRemove.Render("- " + paddedOld)
			right = "  " + newPart
		} else if isSame {
			// Dimmed style for unchanged content
			left = wb.styles.DiffSame.Render("= " + paddedOld)
			right = wb.styles.DiffSame.Render("= " + newPart)
		} else {
			// Colored style for changed content
			left = wb.styles.DiffRemove.Render("- " + paddedOld)
			right = wb.styles.DiffAdd.Render("+ " + newPart)
		}
		sep := wb.styles.DiffSep.Render("|")
		lines = append(lines, left+" "+sep+" "+right)
	}

	return strings.Join(lines, "\n")
}

// expandTabs converts tabs to spaces, treating tabs as 8-space width
func expandTabs(s string) string {
	var result strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			// Calculate spaces needed to reach next 8-space boundary
			next := ((col / 8) + 1) * 8
			spaces := next - col
			result.WriteString(strings.Repeat(" ", spaces))
			col = next
		} else {
			result.WriteRune(r)
			col++
		}
	}
	return result.String()
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

// getLastLines returns the last n lines from an already-wrapped string.
// It finds the nth-to-last newline and returns everything after it.
func getLastLines(wrapped string, n int) string {
	if n <= 0 {
		return ""
	}
	idx := len(wrapped)
	for i := 0; i < n && idx > 0; i++ {
		idx = strings.LastIndex(wrapped[:idx], "\n")
		if idx == -1 {
			return wrapped
		}
	}
	if idx >= 0 && idx < len(wrapped) {
		return wrapped[idx+1:]
	}
	return wrapped
}

// truncateByWidth truncates a string to fit within maxDisplayWidth using lipgloss.Width
// which properly handles wide Unicode characters and ANSI escape sequences
func truncateByWidth(s string, maxDisplayWidth int) string {
	if lipgloss.Width(s) <= maxDisplayWidth {
		return s
	}

	// Binary search or incremental build to find truncation point
	var result strings.Builder

	for _, r := range s {
		test := result.String() + string(r)
		w := lipgloss.Width(test)
		if w > maxDisplayWidth-3 { // Reserve space for "..."
			break
		}
		result.WriteRune(r)
	}

	return result.String() + "..."
}
