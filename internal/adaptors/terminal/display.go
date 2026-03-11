package terminal

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/alayacore/internal/adaptors/common"
)

// DisplayModel holds the viewport over WindowBuffer content.
type DisplayModel struct {
	viewport            viewport.Model
	showingWelcome      bool
	welcomeText         string
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
	vp.SetContent(common.WelcomeText)

	return DisplayModel{
		viewport:            vp,
		showingWelcome:      true,
		welcomeText:         common.WelcomeText,
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.viewport.SetWidth(max(0, msg.Width))
		m.centerWelcomeText()
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

	// Skip update if content hasn't changed
	if newContent == m.lastContent {
		return
	}
	m.lastContent = newContent

	m.viewport.SetContent(newContent)
	if m.shouldFollow() {
		m.viewport.GotoBottom()
	}
}

// ScrollDown scrolls down by lines.
func (m *DisplayModel) ScrollDown(lines int) {
	m.viewport.ScrollDown(lines)
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
}

// GotoBottom goes to bottom
func (m *DisplayModel) GotoBottom() {
	m.viewport.GotoBottom()
}

// GotoTop goes to top
func (m *DisplayModel) GotoTop() {
	m.viewport.GotoTop()
}

// UpdateHeightForTodos adjusts height based on todo visibility
func (m *DisplayModel) UpdateHeightForTodos(totalHeight int, todoCount int) {
	height := totalHeight - LayoutGap
	if todoCount > 0 {
		height -= TodoHeaderRows + todoCount + TodoBorderRows
	}

	newHeight := max(0, height)
	oldHeight := m.viewport.Height()

	if oldHeight != newHeight {
		totalLines := max(1, m.windowBuffer.GetTotalLines())

		topLine := m.viewport.YOffset()
		var newTopLine int

		if m.shouldFollow() {
			bottomLine := topLine + oldHeight - 1
			newTopLine = bottomLine - newHeight + 1
		} else {
			newTopLine = topLine
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
