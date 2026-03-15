package terminal

import (
	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// KeyHandler manages keyboard input routing and handling.
// It provides a clean separation between the main Terminal model
// and the key handling logic.

// handleKeyMsg routes keyboard input to the appropriate handler.
func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 1. Model selector takes precedence when open
	if m.modelSelector.IsOpen() {
		return m.handleModelSelectorKeys(msg)
	}

	// 2. Confirmation dialogs block normal input
	if cmd, handled := m.handleConfirmDialog(msg); handled {
		return m, cmd
	}

	// 3. Tab toggles focus between display and input
	if msg.String() == "tab" {
		m.toggleFocus()
		return m, nil
	}

	// 4. Display-specific keys when display is focused
	if m.focusedWindow == "display" {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	// 5. Global shortcuts (work from any context)
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// 6. Default: pass to input
	return m.handleInputKeys(msg)
}

// handleModelSelectorKeys handles input when model selector is open.
func (m *Terminal) handleModelSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.modelSelector.HandleKeyMsg(msg)

	// Check if a model was selected
	if m.modelSelector.ConsumeModelSelected() {
		m.switchToSelectedModel()
	}

	// Check if user wants to open model file
	if m.modelSelector.ConsumeOpenModelFile() {
		return m, tea.Batch(cmd, m.openModelConfigFile())
	}

	// Check if user wants to reload models
	if m.modelSelector.ConsumeReloadModels() {
		m.streamInput.EmitTLV(stream.TagTextUser, ":model_load")
	}

	// Restore focus when model selector closes
	if !m.modelSelector.IsOpen() {
		m.restoreFocusAfterSelector()
	}

	return m, cmd
}

// handleConfirmDialog handles quit and cancel confirmation dialogs.
func (m *Terminal) handleConfirmDialog(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.confirmDialog {
		return m.handleQuitConfirm(msg)
	}

	if m.cancelConfirmDialog {
		return m.handleCancelConfirm(msg)
	}

	return nil, false
}

// handleQuitConfirm handles the quit confirmation dialog.
func (m *Terminal) handleQuitConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "y", "Y":
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return tea.Quit, true
	case "n", "N", "esc", "ctrl+c":
		m.confirmDialog = false
		m.input.SetValue("")
		return nil, true
	}
	return nil, true
}

// handleCancelConfirm handles the cancel confirmation dialog.
func (m *Terminal) handleCancelConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "y", "Y":
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return m.submitCommand("cancel", m.cancelFromCommand), true
	case "n", "N", "esc", "ctrl+c":
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return nil, true
	}
	return nil, true
}

// handleDisplayKeys handles key events when display window is focused.
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	keyStr := msg.String()

	// Window cursor navigation
	switch keyStr {
	case "j":
		if m.display.MoveWindowCursorDown() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return nil, true

	case "k":
		if m.display.MoveWindowCursorUp() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return nil, true

	case "J":
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		return nil, true

	case "K":
		m.display.MarkUserScrolled()
		m.display.ScrollUp(1)
		return nil, true

	case "H":
		if m.display.MoveWindowCursorToTop() {
			m.display.updateContent()
		}
		return nil, true

	case "L":
		if m.display.MoveWindowCursorToBottom() {
			m.display.updateContent()
		}
		return nil, true

	case "M":
		if m.display.MoveWindowCursorToCenter() {
			m.display.updateContent()
		}
		return nil, true

	case "G":
		m.display.GotoBottom()
		m.display.SetCursorToLastWindow()
		m.display.updateContent()
		return nil, true

	case "g":
		m.display.GotoTop()
		m.display.SetWindowCursor(0)
		m.display.updateContent()
		return nil, true

	case ":":
		// Switch to input with ":" prefix for command mode
		m.focusedWindow = "input"
		m.input.Focus()
		m.input.SetValue(":")
		m.input.CursorEnd()
		return nil, true

	case "space":
		if m.display.ToggleWindowWrap() {
			m.display.updateContent()
		}
		return nil, true
	}

	return nil, false
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+g":
		m.cancelConfirmDialog = true
		m.cancelFromCommand = false
		return nil, true

	case "ctrl+c":
		if m.focusedWindow == "input" {
			m.input.SetValue("")
			m.input.editorContent = ""
		}
		return nil, true

	case "ctrl+u":
		// Reserved for future use
		return nil, true

	case "ctrl+s":
		return m.submitCommand("save", false), true

	case "ctrl+o":
		return m.input.OpenEditor(), true

	case "ctrl+l":
		m.openModelSelector()
		return nil, true

	case "enter":
		return m.handleSubmit(), true
	}

	return nil, false
}

// handleInputKeys handles keys when input is focused (default behavior).
func (m *Terminal) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	oldValue := m.input.Value()
	m.input.updateFromMsg(msg)
	newValue := m.input.Value()

	// Clear editor content if user manually edits the input
	if m.input.editorContent != "" && oldValue != newValue && !hasEditorPrefix(oldValue) {
		m.input.editorContent = ""
	}

	return m, nil
}

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	if m.focusedWindow == "display" {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.focusedWindow = "input"
	m.display.SetDisplayFocused(false)
	m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.focusedWindow = "display"
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
	if m.focusedWindow == "display" {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()
}

// hasEditorPrefix checks if the value has an editor content prefix.
func hasEditorPrefix(value string) bool {
	return len(value) > 0 && value[0] == '['
}
