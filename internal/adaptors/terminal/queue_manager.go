package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// QueueItem represents a queued task for display
type QueueItem struct {
	QueueID   string    `json:"queue_id"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// QueueManagerState represents the current state of the queue manager
type QueueManagerState int

const (
	QueueManagerClosed QueueManagerState = iota
	QueueManagerList
)

// QueueManager manages the task queue UI
type QueueManager struct {
	state       QueueManagerState
	items       []QueueItem
	selectedIdx int
	scrollIdx   int
	width       int
	height      int
	styles      *Styles
}

// NewQueueManager creates a new queue manager
func NewQueueManager(styles *Styles) *QueueManager {
	return &QueueManager{
		state:  QueueManagerClosed,
		items:  []QueueItem{},
		styles: styles,
		width:  60,
		height: 20,
	}
}

// --- State Management ---

func (qm *QueueManager) IsOpen() bool             { return qm.state != QueueManagerClosed }
func (qm *QueueManager) State() QueueManagerState { return qm.state }
func (qm *QueueManager) SetItems(items []QueueItem) {
	qm.items = items
	qm.clampSelection()
}

func (qm *QueueManager) Open() {
	qm.state = QueueManagerList
	qm.selectedIdx = 0
	qm.scrollIdx = 0
	qm.clampSelection()
}

func (qm *QueueManager) Close() {
	qm.state = QueueManagerClosed
}

// --- Selection Management ---

func (qm *QueueManager) GetSelectedItem() *QueueItem {
	if len(qm.items) == 0 || qm.selectedIdx >= len(qm.items) {
		return nil
	}
	return &qm.items[qm.selectedIdx]
}

func (qm *QueueManager) clampSelection() {
	if len(qm.items) == 0 {
		qm.selectedIdx = 0
		qm.scrollIdx = 0
		return
	}
	if qm.selectedIdx >= len(qm.items) {
		qm.selectedIdx = len(qm.items) - 1
	}
	if qm.selectedIdx < 0 {
		qm.selectedIdx = 0
	}
	// Ensure scrollIdx is valid
	if qm.scrollIdx < 0 {
		qm.scrollIdx = 0
	}
	if qm.scrollIdx > qm.selectedIdx {
		qm.scrollIdx = qm.selectedIdx
	}
}

// --- Size Management ---

func (qm *QueueManager) SetSize(width, height int) {
	qm.width = width
	qm.height = height
}

// --- Input Handling ---

// HandleKeyMsg processes keyboard input and returns a tea.Cmd
func (qm *QueueManager) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		qm.Close()
		return nil

	case "j", "down":
		if len(qm.items) > 0 && qm.selectedIdx < len(qm.items)-1 {
			qm.selectedIdx++
			qm.updateScrollForHeight(8)
		}
		return nil

	case "k", "up":
		if qm.selectedIdx > 0 {
			qm.selectedIdx--
			qm.updateScrollForHeight(8)
		}
		return nil

	case "d":
		// Delete is handled by parent
		return nil
	}

	return nil
}

// --- Rendering ---

func (qm *QueueManager) View() string {
	if qm.state == QueueManagerClosed {
		return ""
	}

	listHeight := 8 // 8 content rows inside border
	maxItems := 8   // All rows for items

	// Build content
	var lines []string

	if len(qm.items) == 0 {
		emptyStyle := qm.styles.System
		lines = append(lines, emptyStyle.Render("  No queued tasks"))
	} else {
		qm.updateScrollForHeight(maxItems)

		endIdx := qm.scrollIdx + maxItems
		if endIdx > len(qm.items) {
			endIdx = len(qm.items)
		}

		for i := qm.scrollIdx; i < endIdx; i++ {
			item := qm.items[i]
			lines = append(lines, qm.renderItem(item, i == qm.selectedIdx))
		}
	}

	// Pad lines to fill the list height
	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	// Wrap in border with same style as input box
	content := strings.Join(lines, "\n")
	borderedBox := qm.styles.RenderBorderedBox(content, qm.width, ColorAccent, listHeight)

	// Help text outside the bordered box
	helpText := qm.styles.System.Render("j/k: navigate │ d: delete │ q/esc: close")
	return borderedBox + "\n" + helpText
}

func (qm *QueueManager) updateScrollForHeight(height int) {
	// Scroll down if selection is below visible area
	if qm.selectedIdx >= qm.scrollIdx+height {
		qm.scrollIdx = qm.selectedIdx - height + 1
	}

	// Scroll up if selection is above visible area
	if qm.selectedIdx < qm.scrollIdx {
		qm.scrollIdx = qm.selectedIdx
	}
}

func (qm *QueueManager) renderItem(item QueueItem, selected bool) string {
	// Calculate available width for content
	// Inner width is qm.width - 4, account for "> Q123 [P] " = ~12 characters overhead
	maxWidth := qm.width - 20
	if maxWidth < 10 {
		maxWidth = 10
	}

	content := item.Content
	// Escape newlines and tabs for single-line display
	content = strings.ReplaceAll(content, "\n", "\\n")
	content = strings.ReplaceAll(content, "\t", "\\t")
	if len(content) > maxWidth {
		content = content[:maxWidth-3] + "..."
	}

	// Format: "ID | Type | Content"
	typeStr := item.Type
	if typeStr == "prompt" {
		typeStr = "P"
	} else {
		typeStr = "C"
	}

	line := fmt.Sprintf("%s [%s] %s", item.QueueID, typeStr, content)

	if selected {
		return qm.styles.Prompt.Render("> " + line)
	}
	return "  " + qm.styles.System.Render(line)
}

// RenderOverlay renders the queue manager as an overlay on top of base content
func (qm *QueueManager) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if qm.state == QueueManagerClosed {
		return baseContent
	}

	box := qm.View()
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x := max(0, (screenWidth-boxWidth)/2)

	// Position above the input box (input box is ~3 lines, status bar is 1 line)
	inputAreaHeight := 4 // input box (3 lines) + status bar (1 line)
	y := max(0, screenHeight-boxHeight-inputAreaHeight)

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return c.Render()
}
