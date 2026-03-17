package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// CommandHandler processes user commands (text starting with ":").
// Commands allow users to control the application behavior.

// handleSubmit processes the input when Enter is pressed.
func (m *Terminal) handleSubmit() tea.Cmd {
	prompt := m.input.GetPrompt()
	m.input.editorContent = ""

	if prompt == "" {
		return nil
	}

	// Check if it's a command (starts with ":")
	if command, found := strings.CutPrefix(prompt, ":"); found {
		return m.handleCommand(command)
	}

	// Regular prompt - send to agent
	_ = m.streamInput.EmitTLV(stream.TagTextUser, prompt) //nolint:errcheck // best-effort input
	m.input.SetValue("")

	return scheduleTick()
}

// handleCommand processes a command string (without the ":" prefix).
func (m *Terminal) handleCommand(command string) tea.Cmd {
	// Quit command
	if command == "quit" || command == "q" {
		m.confirmDialog = true
		return nil
	}

	// Cancel command
	if command == "cancel" {
		m.cancelConfirmDialog = true
		m.cancelFromCommand = true
		return nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	_ = m.streamInput.EmitTLV(stream.TagTextUser, ":"+command) //nolint:errcheck // best-effort input
	if clearInput {
		m.input.SetValue("")
	}
	return scheduleTick()
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() tea.Cmd {
	return tea.Tick(SubmitTickDelay, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// switchToSelectedModel sends a model_set command to switch to the selected model.
func (m *Terminal) switchToSelectedModel() {
	selectedModel := m.modelSelector.GetActiveModel()
	if selectedModel == nil {
		return
	}

	// Send model_set command to session
	if selectedModel.ID != "" {
		_ = m.streamInput.EmitTLV(stream.TagTextUser, ":model_set "+selectedModel.ID) //nolint:errcheck // best-effort input
	}
}

// openModelConfigFile opens the model config file with $EDITOR.
func (m *Terminal) openModelConfigFile() tea.Cmd {
	path := m.out.GetModelConfigPath()
	if path == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{
				Path: "",
				Err:  fmt.Errorf("no model config file path configured"),
			}
		}
	}

	return m.input.editor.OpenFile(path)
}
