package terminal

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

// Window represents a single display window with border and content.
type Window struct {
	ID      string         // stream ID or generated unique ID
	Tag     byte           // TLV tag that created this window
	Content string         // accumulated content (styled)
	Style   lipgloss.Style // border style (dimmed)
}

// WindowBuffer holds a sequence of windows in order of creation.
type WindowBuffer struct {
	mu      sync.Mutex
	Windows []*Window
	// mapping from ID to window index for fast lookup
	idIndex map[string]int
	// width of windows (same as input box width)
	width int
	// border style template
	borderStyle lipgloss.Style
}

// NewWindowBuffer creates a new window buffer with given width.
func NewWindowBuffer(width int) *WindowBuffer {
	// Dimmed border: rounded border with subtle color
	dimmedBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6c7086")).
		Padding(0, 1)
	return &WindowBuffer{
		Windows:     []*Window{},
		idIndex:     make(map[string]int),
		width:       width,
		borderStyle: dimmedBorder,
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
func (wb *WindowBuffer) AppendOrUpdate(id string, tag byte, content string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		// Append to existing window
		window := wb.Windows[idx]
		window.Content += content
		return
	}
	// Create new window
	window := &Window{
		ID:      id,
		Tag:     tag,
		Content: content,
		Style:   wb.borderStyle,
	}
	wb.Windows = append(wb.Windows, window)
	wb.idIndex[id] = len(wb.Windows) - 1
}

// GetAll returns the concatenated rendered windows as a single string.
// Each window is rendered with its border and padded to the current width.
func (wb *WindowBuffer) GetAll() string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	var sb strings.Builder
	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		// Wrap content to fit inside border (width - 2 for borders?).
		// The border style has Padding(0,1) which adds 1 space left/right.
		// The border itself takes 2 columns left+right? Actually border characters count as 1 column.
		// We'll let lipgloss handle sizing by setting width on the style.
		// We need to set width of the inner content area: wb.width - 2 (border) - 2 (padding)???
		// For simplicity, we apply the border style with width wb.width.
		// lipgloss will automatically fit content within available space.
		innerWidth := max(0, wb.width-4) // width - 2 (border) - 2 (padding)
		// Ensure content does not exceed inner width? lipgloss.Wrap will handle.
		wrapped := lipgloss.Wrap(w.Content, innerWidth, " ")
		styled := w.Style.Width(wb.width).Render(wrapped)
		sb.WriteString(styled)
	}
	return sb.String()
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.Windows = nil
	wb.idIndex = make(map[string]int)
}
