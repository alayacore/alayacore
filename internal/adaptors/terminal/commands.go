package terminal

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
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
	m.streamInput.EmitTLV(stream.TagTextUser, prompt)
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
	m.streamInput.EmitTLV(stream.TagTextUser, ":"+command)
	if clearInput {
		m.input.SetValue("")
	}
	return scheduleTick()
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() tea.Cmd {
	return tea.Tick(SubmitTickDelay, func(t time.Time) tea.Msg {
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
		m.streamInput.EmitTLV(stream.TagTextUser, ":model_set "+selectedModel.ID)
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

// applyModelSwitch applies a model switch from a model_set response.
// This is the only place where the adaptor calls session.SwitchModel() directly.
// This is necessary because provider/model creation requires proxy and debug settings
// that are only available to the adaptor, not the session.
//
// Flow:
//  1. Terminal sends :model_set <id> via TLV (TagTextUser)
//  2. Session handles command and sends TagSystemData with ActiveModelConfig
//  3. Adaptor creates provider/model objects and calls SwitchModel
//  4. Session state is updated with new model
func (m *Terminal) applyModelSwitch(model *agentpkg.ModelConfig) {
	if model == nil || m.appConfig == nil {
		return
	}

	// Create new provider and model
	provider, err := app.CreateProvider(
		model.ProtocolType,
		model.APIKey,
		model.BaseURL,
		m.appConfig.Cfg.DebugAPI,
		m.appConfig.Cfg.Proxy,
	)
	if err != nil {
		m.out.WriteNotify("Failed to create provider: " + err.Error())
		return
	}

	newModel, err := provider.LanguageModel(context.Background(), model.ModelName)
	if err != nil {
		m.out.WriteNotify("Failed to create language model: " + err.Error())
		return
	}

	// Switch the session to the new model
	m.session.SwitchModel(
		newModel,
		model.BaseURL,
		model.ModelName,
		m.appConfig.AgentTools,
		m.appConfig.SystemPrompt,
	)

	// Show notification
	m.out.WriteNotify("Switched to model: " + model.Name + " (" + model.ModelName + ")")
}
