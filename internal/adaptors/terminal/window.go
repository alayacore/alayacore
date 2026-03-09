package terminal

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/coreclaw/internal/stream"
)

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
	lastContentLen int    // length of content when last rendered (for quick change detection)
	cachedRender   string // cached rendered output
	cachedWidth    int    // width used for cached render
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
	dirty        bool    // true if content has changed and needs re-render
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
		wb.dirty = true
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
		// Append to existing window
		window := wb.Windows[idx]
		window.Content += content
		wb.dirty = true
		return
	}
	// Create new window - reasoning windows are wrapped by default
	window := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Style:   wb.borderStyle,
		Wrapped: tag == stream.TagReasoning,
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
	wb.dirty = true
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
	wb.dirty = true
}

// IsDiffWindow returns true if the window is a diff window
func (w *Window) IsDiffWindow() bool {
	return w.Diff != nil
}

// GetAll returns the concatenated rendered windows as a single string.
// Each window is rendered with its border and padded to the current width.
// If cursorIndex >= 0, that window is highlighted with cursor border style.
// If a window is in Wrapped mode, it shows only the last 3 lines of content.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Check if we need to re-render
	needsRender := wb.dirty
	if !needsRender {
		// Check if any window content has changed (quick length check)
		for _, w := range wb.Windows {
			if w.IsDiffWindow() {
				// Diff windows need re-render if width changed
				if w.cachedWidth != wb.width {
					needsRender = true
					break
				}
			} else if len(w.Content) != w.lastContentLen {
				needsRender = true
				break
			}
		}
	}

	if needsRender {
		wb.rebuildCache()
		wb.dirty = false
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
		innerWidth := max(0, wb.width-4)
		contentToRender := wb.renderWindowContent(w, innerWidth)

		styled := w.Style.Width(wb.width).Render(contentToRender)
		sb.WriteString(styled)

		// Track line height and cache rendered content
		lineCount := strings.Count(styled, "\n") + 1
		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount
		w.cachedRender = styled
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
	}
	wb.cachedRender = sb.String()
}

// renderWithCursor renders all windows with cursor highlighting on the specified window
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Use cached render for non-cursor windows (if still valid)
		if i != cursorIndex && w.cachedRender != "" && w.cachedWidth == wb.width {
			if w.IsDiffWindow() || len(w.Content) == w.lastContentLen {
				sb.WriteString(w.cachedRender)
				continue
			}
		}

		// Render cursor window (or uncached window) with appropriate style
		innerWidth := max(0, wb.width-4)
		contentToRender := wb.renderWindowContent(w, innerWidth)

		style := w.Style
		if i == cursorIndex {
			style = wb.cursorStyle
		}
		styled := style.Width(wb.width).Render(contentToRender)
		sb.WriteString(styled)
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
		// In wrapped mode, show first line, wrap indicator, and last line
		wrappedContent := lipgloss.Wrap(w.Content, innerWidth, " ")

		// Check if content spans more than 3 lines (needs truncation)
		lines := strings.Split(wrappedContent, "\n")
		if len(lines) > 3 {
			// Show: first line, wrap indicator, last line
			firstLine := lines[0]
			lastLine := lines[len(lines)-1]

			// Create dashed wrap indicator with text on left, dashes on right
			wrapText := "Wrapped - Space to expand "
			dashChar := "┈" // Light quadruple dash horizontal (U+2508)
			textWidth := lipgloss.Width(wrapText)
			remainingWidth := innerWidth - textWidth

			var wrapIndicator string
			if remainingWidth > 0 {
				dashes := strings.Repeat(dashChar, remainingWidth)
				wrapIndicator = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#585b70")).
					Render(wrapText + dashes)
			} else {
				// Text is too long, just show it without dashes
				wrapIndicator = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#585b70")).
					Render(wrapText)
			}
			return firstLine + "\n" + wrapIndicator + "\n" + lastLine
		}
		// Content fits in 3 lines or less, just show wrapped content
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

		// Check if content is the same (before truncation)
		isSame := pair.Old == pair.New

		// Truncate if needed (use rune count for proper Unicode handling)
		oldRunes := []rune(oldPart)
		newRunes := []rune(newPart)
		if len(oldRunes) > sideWidth {
			oldPart = string(oldRunes[:sideWidth-3]) + "..."
		}
		if len(newRunes) > sideWidth {
			newPart = string(newRunes[:sideWidth-3]) + "..."
		}

		// Pad old part to fixed width (use rune count)
		paddedOld := oldPart + strings.Repeat(" ", max(0, sideWidth-len([]rune(oldPart))))

		if isSame {
			// Dimmed style for unchanged content
			left := wb.styles.DiffSame.Render("= " + paddedOld)
			sep := wb.styles.DiffSep.Render("|")
			right := wb.styles.DiffSame.Render("= " + newPart)
			lines = append(lines, left+" "+sep+" "+right)
		} else {
			// Colored style for changed content
			left := wb.styles.DiffRemove.Render("- " + paddedOld)
			sep := wb.styles.DiffSep.Render("|")
			right := wb.styles.DiffAdd.Render("+ " + newPart)
			lines = append(lines, left+" "+sep+" "+right)
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
	wb.dirty = true
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
	wb.dirty = true
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
