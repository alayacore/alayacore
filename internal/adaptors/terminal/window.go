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
}

// WindowBuffer holds a sequence of windows in order of creation.
type WindowBuffer struct {
	mu          sync.Mutex
	Windows     []*Window
	idIndex     map[string]int
	width       int
	borderStyle lipgloss.Style
	cursorStyle lipgloss.Style
	lineHeights []int // cached line heights for each window (after rendering)
	totalLines  int   // total lines across all windows
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
		lineHeights: []int{},
	}
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) SetWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.width = width
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
}

// GetAll returns the concatenated rendered windows as a single string.
// Each window is rendered with its border and padded to the current width.
// If cursorIndex >= 0, that window is highlighted with cursor border style.
// If a window is in Wrapped mode, it shows only the last 3 lines of content.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	var sb strings.Builder
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		innerWidth := max(0, wb.width-4)

		var contentToRender string
		if w.Wrapped {
			// In wrapped mode, show only last 3 lines
			wrappedContent := lipgloss.Wrap(w.Content, innerWidth, " ")
			totalLines := strings.Count(wrappedContent, "\n") + 1

			// Only show wrap indicator if window has more than 3 lines
			if totalLines > 3 {
				contentToRender = wb.getLastLines(w.Content, innerWidth, 3)
				wrapIndicator := lipgloss.NewStyle().
					Background(lipgloss.Color("#45475a")).
					Render(" Wrapped - Space to expand ")
				if contentToRender != "" {
					contentToRender = wrapIndicator + "\n" + contentToRender
				} else {
					contentToRender = wrapIndicator
				}
			} else {
				// Window has 3 or fewer lines, just show content without indicator
				contentToRender = wrappedContent
			}
		} else {
			contentToRender = lipgloss.Wrap(w.Content, innerWidth, " ")
		}

		// Determine style based on cursor state
		style := w.Style
		if i == cursorIndex {
			style = wb.cursorStyle
		}
		styled := style.Width(wb.width).Render(contentToRender)
		sb.WriteString(styled)

		// Track line height for this window
		lineCount := strings.Count(styled, "\n") + 1
		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount
	}
	return sb.String()
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
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
	return true
}

// getLastLines returns the last n lines of content after wrapping.
// It wraps the content first, then extracts the last n lines.
func (wb *WindowBuffer) getLastLines(content string, innerWidth, n int) string {
	wrapped := lipgloss.Wrap(content, innerWidth, " ")
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= n {
		return wrapped
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
