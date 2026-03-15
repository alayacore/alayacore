package agent

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
)

// ============================================================================
// Command Handling
// ============================================================================

func (s *Session) handleCommandSync(ctx context.Context, cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError("empty command")
		return
	}

	switch parts[0] {
	case "summarize":
		s.summarize(ctx)
	case "cancel":
		s.cancelTask()
	case "save":
		s.saveSession(parts[1:])
	case "model_set":
		s.handleModelSet(parts[1:])
	case "model_load":
		s.handleModelLoad()
	default:
		s.writeError(fmt.Sprintf("unknown cmd <%s>", cmd))
	}
}

func (s *Session) cancelTask() {
	s.mu.Lock()
	inProgress := s.inProgress
	cancelCurrent := s.cancelCurrent
	s.mu.Unlock()
	if inProgress && cancelCurrent != nil {
		cancelCurrent()
		return
	}
	s.writeError("nothing to cancel")
}

// summarize replaces the conversation history with a concise summary
func (s *Session) summarize(ctx context.Context) {
	prompt := "Please summarize the conversation above in a concise manner. Return ONLY the summary, no introductions or explanations."

	// Remember message count before summarize to find the summary message
	beforeCount := len(s.Messages)

	outputTokens, err := s.processPrompt(ctx, prompt, s.Messages)
	if err != nil {
		s.writeError(err.Error())
		return
	}

	// Find the last assistant message (the summary) from newly added messages
	var lastAssistantMsg fantasy.Message
	for i := beforeCount; i < len(s.Messages); i++ {
		if s.Messages[i].Role == fantasy.MessageRoleAssistant {
			lastAssistantMsg = s.Messages[i]
		}
	}

	s.Messages = []fantasy.Message{lastAssistantMsg}
	// Update context tokens to reflect the new, smaller context (the summary)
	if outputTokens > 0 {
		s.mu.Lock()
		s.ContextTokens = outputTokens
		s.mu.Unlock()
	}
	s.sendSystemInfo()
}

func (s *Session) saveSession(args []string) {
	var path string
	switch len(args) {
	case 0:
		if s.SessionFile == "" {
			s.writeError("no session file set and no filename provided")
			return
		}
		path = s.SessionFile
	case 1:
		path = expandPath(args[0])
	default:
		s.writeError("usage: :save [filename]")
		return
	}

	if err := s.saveSessionToFile(path); err != nil {
		s.writeError(fmt.Sprintf("failed to save session: %v", err))
	} else {
		s.writeNotify(fmt.Sprintf("Session saved to %s", path))
	}
}

// ============================================================================
// Model Commands
// ============================================================================

func (s *Session) handleModelSet(args []string) {
	if s.ModelManager == nil {
		s.writeError("model manager not initialized")
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_set <id>")
		return
	}

	modelID := args[0]
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(fmt.Sprintf("model not found: %s", modelID))
		return
	}

	// Update active ID
	if err := s.ModelManager.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	// Save active model name to runtime manager for persistence
	if s.RuntimeManager != nil {
		_ = s.RuntimeManager.SetActiveModel(model.Name)
	}

	// Update session context limit for this model, if configured.
	s.applyModelContextLimit(model)

	// Send system info with full model config (terminal needs API key to switch)
	s.sendSystemInfoWithModel(model)
}

func (s *Session) handleModelLoad() {
	if s.ModelManager == nil {
		s.writeError("model manager not initialized")
		return
	}

	path := s.ModelManager.GetFilePath()
	if path == "" {
		s.writeError("no model file path configured")
		return
	}

	if err := s.ModelManager.LoadFromFile(path); err != nil {
		s.writeError(fmt.Sprintf("failed to load models: %v", err))
		return
	}

	// Restore active model from runtime config
	s.initModelManager()

	// Send system info with model list to adaptor via TagSystemData.
	// If an active model is known, also apply its context limit and
	// include full config so the adaptor can recreate the provider.
	if active := s.ModelManager.GetActive(); active != nil {
		s.applyModelContextLimit(active)
		s.sendSystemInfoWithModel(active)
		return
	}
	s.sendSystemInfo()
}
