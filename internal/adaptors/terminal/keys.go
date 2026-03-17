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

	// 2. Queue manager takes precedence when open
	if m.queueManager.IsOpen() {
		return m.handleQueueManagerKeys(msg)
	}

	// 3. Confirmation dialogs block normal input
	if cmd, handled := m.handleConfirmDialog(msg); handled {
		return m, cmd
	}

	// 4. Tab toggles focus between display and input
	if msg.String() == KeyTab {
		m.toggleFocus()
		return m, nil
	}

	// 5. Display-specific keys when display is focused
	if m.focusedWindow == "display" {
		if handled := m.handleDisplayKeys(msg); handled {
			return m, nil
		}
	}

	// 6. Global shortcuts (work from any context)
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// 7. Default: pass to input
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
		_ = m.streamInput.EmitTLV(stream.TagTextUser, ":model_load") //nolint:errcheck // best-effort input
	}

	// Restore focus when model selector closes
	if !m.modelSelector.IsOpen() {
		m.restoreFocusAfterSelector()
	}

	return m, cmd
}

// handleQueueManagerKeys handles input when queue manager is open.
func (m *Terminal) handleQueueManagerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle 'd' key for delete
	if msg.String() == KeyD {
		selectedItem := m.queueManager.GetSelectedItem()
		if selectedItem != nil {
			// Send delete command to session
			_ = m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_del "+selectedItem.QueueID) //nolint:errcheck // best-effort input
			// Request updated queue list
			_ = m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_get_all") //nolint:errcheck // best-effort input
		}
		return m, nil
	}

	cmd := m.queueManager.HandleKeyMsg(msg)

	// Restore focus when queue manager closes
	if !m.queueManager.IsOpen() {
		m.restoreFocusAfterQueueManager()
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
	case KeyY, "Y":
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return tea.Quit, true
	case KeyN, "N", KeyEsc, KeyCtrlC:
		m.confirmDialog = false
		m.input.SetValue("")
		return nil, true
	}
	return nil, true
}

// handleCancelConfirm handles the cancel confirmation dialog.
func (m *Terminal) handleCancelConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyY, "Y":
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return m.submitCommand("cancel", m.cancelFromCommand), true
	case KeyN, "N", KeyEsc, KeyCtrlC:
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return nil, true
	}
	return nil, true
}

// handleDisplayKeys handles key events when display window is focused.
//
//nolint:gocyclo // key handling requires many key cases
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) bool {
	keyStr := msg.String()

	// Window cursor navigation
	switch keyStr {
	case KeyJ, KeyDown:
		if m.display.MoveWindowCursorDown() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return true

	case KeyK, KeyUp:
		if m.display.MoveWindowCursorUp() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return true

	case KeyShiftJ:
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		return true

	case KeyShiftK:
		m.display.MarkUserScrolled()
		m.display.ScrollUp(1)
		return true

	case KeyShiftH:
		if m.display.MoveWindowCursorToTop() {
			m.display.updateContent()
		}
		return true

	case KeyShiftL:
		if m.display.MoveWindowCursorToBottom() {
			m.display.updateContent()
		}
		return true

	case KeyShiftM:
		if m.display.MoveWindowCursorToCenter() {
			m.display.updateContent()
		}
		return true

	case KeyG:
		m.display.GotoBottom()
		m.display.SetCursorToLastWindow()
		m.display.updateContent()
		return true

	case Keyg:
		m.display.GotoTop()
		m.display.SetWindowCursor(0)
		m.display.updateContent()
		return true

	case KeyColon:
		// Switch to input with ":" prefix for command mode
		m.focusedWindow = focusInput
		m.input.Focus()
		m.input.SetValue(":")
		m.input.CursorEnd()
		return true

	case KeySpace:
		if m.display.ToggleWindowWrap() {
			m.display.updateContent()
		}
		return true
	}

	return false
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyCtrlG:
		m.cancelConfirmDialog = true
		m.cancelFromCommand = false
		return nil, true

	case KeyCtrlC:
		if m.focusedWindow == focusInput {
			m.input.SetValue("")
			m.input.editorContent = ""
		}
		return nil, true

	case KeyCtrlU:
		// Reserved for future use
		return nil, true

	case KeyCtrlS:
		return m.submitCommand("save", false), true

	case KeyCtrlO:
		return m.input.OpenEditor(), true

	case KeyCtrlL:
		m.openModelSelector()
		return nil, true

	case KeyCtrlQ:
		m.openQueueManager()
		return nil, true

	case KeyEnter:
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
