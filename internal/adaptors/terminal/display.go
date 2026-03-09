package terminal

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/coreclaw/internal/adaptors/common"
)

// DisplayMsg represents messages specific to the display component
type DisplayMsg struct {
	Type    string
	Content string
}

// DisplayModel handles the main content viewport
type DisplayModel struct {
	viewport            viewport.Model
	userScrolledAway    bool
	showingWelcome      bool
	welcomeText         string
	windowBuffer        *WindowBuffer
	styles              *Styles
	width               int
	height              int
	windowCursor        int  // index of the currently selected window (-1 means no selection)
	userMovedCursorAway bool // true if user manually moved cursor away from last window
	displayFocused      bool // true when display has focus (for showing cursor highlight)
}

// NewDisplayModel creates a new display model
func NewDisplayModel(windowBuffer *WindowBuffer, styles *Styles) DisplayModel {
	coloredWelcome := colorizeWelcomeText(common.WelcomeText)
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SetContent(coloredWelcome)

	return DisplayModel{
		viewport:            vp,
		showingWelcome:      true,
		welcomeText:         coloredWelcome,
		windowBuffer:        windowBuffer,
		styles:              styles,
		width:               80,
		height:              20,
		windowCursor:        -1,
		userMovedCursorAway: false,
		displayFocused:      false,
	}
}

// Init initializes the display
func (m DisplayModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the display
func (m DisplayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.viewport.SetWidth(max(0, msg.Width))
		m.centerWelcomeText()
	case DisplayMsg:
		switch msg.Type {
		case "content_update":
			m.updateContent()
		case "scroll_down":
			m.ScrollDown(1)
		case "scroll_up":
			m.viewport.ScrollUp(1)
			m.userScrolledAway = true
		case "goto_bottom":
			m.viewport.GotoBottom()
			m.userScrolledAway = false
		case "goto_top":
			m.viewport.GotoTop()
			m.userScrolledAway = true
		case "scroll_half_down":
			m.ScrollDown(m.viewport.Height() / 2)
		case "scroll_half_up":
			m.viewport.ScrollUp(m.viewport.Height() / 2)
			m.userScrolledAway = true
		}
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
	newContent := m.windowBuffer.GetAll(cursorIndex)

	if m.showingWelcome {
		if newContent != "" && newContent != m.welcomeText {
			m.showingWelcome = false
		} else {
			return
		}
	}

	m.viewport.SetContent(newContent)
	if !m.userScrolledAway {
		m.viewport.GotoBottom()
	}
}

// ScrollDown scrolls down by lines and updates userScrolledAway.
func (m *DisplayModel) ScrollDown(lines int) {
	m.viewport.ScrollDown(lines)
	m.userScrolledAway = !m.viewport.AtBottom()
}

// centerWelcomeText centers the welcome text in the viewport
func (m *DisplayModel) centerWelcomeText() {
	width := m.viewport.Width()
	height := m.viewport.Height()
	if width == 0 || height == 0 {
		return
	}

	if !m.showingWelcome {
		return
	}

	lines := strings.Split(m.welcomeText, "\n")
	maxWidth := 0
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}

	lineCount := len(lines)
	topPadding := max(0, (height-lineCount)/2)

	centeredLines := make([]string, 0, len(lines)+topPadding)
	if maxWidth < width {
		padding := (width - maxWidth) / 2
		for _, line := range lines {
			centeredLines = append(centeredLines, strings.Repeat(" ", padding)+line)
		}
	} else {
		centeredLines = append(centeredLines, lines...)
	}

	for range topPadding {
		centeredLines = append([]string{""}, centeredLines...)
	}

	m.viewport.SetContent(strings.Join(centeredLines, "\n"))
}

// ClearWelcome clears the welcome screen
func (m *DisplayModel) ClearWelcome() {
	m.showingWelcome = false
}

// IsShowingWelcome returns whether welcome is being shown
func (m DisplayModel) IsShowingWelcome() bool {
	return m.showingWelcome
}

// AtBottom returns whether viewport is at bottom
func (m DisplayModel) AtBottom() bool {
	return m.viewport.AtBottom()
}

// ScrollUp scrolls up by lines
func (m *DisplayModel) ScrollUp(lines int) {
	m.viewport.ScrollUp(lines)
	m.userScrolledAway = true
}

// GotoBottom goes to bottom
func (m *DisplayModel) GotoBottom() {
	m.viewport.GotoBottom()
	m.userScrolledAway = false
}

// GotoTop goes to top
func (m *DisplayModel) GotoTop() {
	m.viewport.GotoTop()
	m.userScrolledAway = true
}

// UpdateHeightForTodos adjusts height based on todo visibility
func (m *DisplayModel) UpdateHeightForTodos(totalHeight int, todoCount int) {
	height := totalHeight - 4 // Subtract input (3) + status (1)
	if todoCount > 0 {
		height -= (1 + todoCount + 2) // header + items + borders
	}

	newHeight := max(0, height)
	oldHeight := m.viewport.Height()

	if oldHeight != newHeight {
		rawContent := m.windowBuffer.GetAll(m.windowCursor)
		totalLines := max(1, strings.Count(rawContent, "\n")+1)

		topLine := m.viewport.YOffset()
		var newTopLine int

		if m.userScrolledAway {
			newTopLine = topLine
		} else {
			bottomLine := topLine + oldHeight - 1
			newTopLine = bottomLine - newHeight + 1
		}

		maxTopLine := max(0, totalLines-newHeight)
		if newTopLine > maxTopLine {
			newTopLine = maxTopLine
		}
		if newTopLine < 0 {
			newTopLine = 0
		}

		m.viewport.SetHeight(newHeight)
		m.viewport.SetYOffset(newTopLine)
	} else {
		m.viewport.SetHeight(newHeight)
	}

	m.updateContent()
}

var _ tea.Model = (*DisplayModel)(nil)

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
	// Update userMovedCursorAway based on whether we're at the last window
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
		if m.windowCursor == windowCount-1 {
			m.userMovedCursorAway = false // reached last window, resume auto-follow
		} else {
			m.userMovedCursorAway = true
		}
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
	m.userMovedCursorAway = true // moving up always means moving away from last
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
		m.userScrolledAway = true
		return
	}

	// If window end is below viewport, scroll down to show it fully
	if endLine > viewportBottom {
		newTop := endLine - viewportHeight
		m.viewport.SetYOffset(newTop)
		m.userScrolledAway = true
	}
}

// SetCursorToLastWindow sets the cursor to the last window.
func (m *DisplayModel) SetCursorToLastWindow() {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		m.windowCursor = -1
	} else {
		m.windowCursor = windowCount - 1
	}
}

// UserMovedCursorAway returns true if the user has manually moved the cursor away from the last window.
func (m *DisplayModel) UserMovedCursorAway() bool {
	return m.userMovedCursorAway
}

// ToggleWindowWrap toggles the wrap state of the currently selected window.
// Returns true if a window was toggled, false if no window is selected.
func (m *DisplayModel) ToggleWindowWrap() bool {
	if m.windowCursor < 0 {
		return false
	}
	return m.windowBuffer.ToggleWrap(m.windowCursor)
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
			m.userMovedCursorAway = true
			return true
		}
		// If this window is above viewport bottom (last visible window)
		if endLine <= viewportBottom {
			m.windowCursor = i
			m.userMovedCursorAway = true
			return true
		}
	}
	return false
}

// MoveWindowCursorToCenter moves the window cursor to the window at the center of the visible screen.
// Returns true if the cursor moved, false otherwise.
func (m *DisplayModel) MoveWindowCursorToCenter() bool {
	windowCount := m.windowBuffer.GetWindowCount()
	if windowCount == 0 {
		return false
	}

	viewportCenter := m.viewport.YOffset() + m.viewport.Height()/2

	// Find the window that contains the center line of the viewport
	for i := 0; i < windowCount; i++ {
		startLine := m.windowBuffer.GetWindowStartLine(i)
		endLine := m.windowBuffer.GetWindowEndLine(i)
		// If this window contains the center line
		if startLine <= viewportCenter && endLine > viewportCenter {
			m.windowCursor = i
			m.userMovedCursorAway = true
			return true
		}
		// If past the center, pick the previous window (or this one if first)
		if startLine > viewportCenter {
			m.windowCursor = max(0, i-1)
			m.userMovedCursorAway = true
			return true
		}
	}
	// If center is past all windows, select the last one
	m.windowCursor = windowCount - 1
	m.userMovedCursorAway = true
	return true
}
