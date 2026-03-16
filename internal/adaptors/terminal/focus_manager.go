package terminal

import (
	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// Window focus constants
const (
	focusDisplay = "display"
	focusInput   = "input"
)

// FocusManager handles focus state management for the terminal UI.
// This includes switching between display/input, managing overlay focus,
// and handling application focus/blur events.

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	if m.focusedWindow == focusDisplay {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.focusedWindow = focusInput
	m.display.SetDisplayFocused(false)
	m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.focusedWindow = focusDisplay
	m.display.SetDisplayFocused(true)
	m.input.Blur()
	// Initialize cursor to last window if not set
	if m.display.GetWindowCursor() < 0 {
		m.display.SetCursorToLastWindow()
	}
}

// openModelSelector opens the model selector UI.
func (m *Terminal) openModelSelector() {
	m.modelSelector.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocusAfterSelector restores focus after model selector closes.
func (m *Terminal) restoreFocusAfterSelector() {
	if m.focusedWindow == focusDisplay {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()
}

// openQueueManager opens the queue manager UI.
func (m *Terminal) openQueueManager() {
	// Request queue items from session
	m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_get_all")
	m.queueManager.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocusAfterQueueManager restores focus after queue manager closes.
func (m *Terminal) restoreFocusAfterQueueManager() {
	if m.focusedWindow == focusDisplay {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true

	// If model selector is open, don't restore focus to main input
	// The model selector maintains its own focus state
	if m.modelSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	// Restore focus to the previously focused window
	if m.focusedWindow == "display" {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()

	return m, nil
}

// hasEditorPrefix checks if the value has an editor content prefix.
func hasEditorPrefix(value string) bool {
	return len(value) > 0 && value[0] == '['
}
