package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
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
	case "taskqueue_get_all":
		s.handleTaskQueueGetAll()
	case "taskqueue_del":
		s.handleTaskQueueDel(parts[1:])
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

	// Create provider and model
	provider, err := app.CreateProvider(
		model.ProtocolType,
		model.APIKey,
		model.BaseURL,
		s.debugAPI,
		s.proxyURL,
	)
	if err != nil {
		s.writeError("Failed to create provider: " + err.Error())
		return
	}

	newModel, err := provider.LanguageModel(context.Background(), model.ModelName)
	if err != nil {
		s.writeError("Failed to create language model: " + err.Error())
		return
	}

	// Switch to the new model
	s.SwitchModel(newModel, model.BaseURL, model.ModelName, s.baseTools, s.systemPrompt)

	// Send notification
	s.writeNotify("Switched to model: " + model.Name + " (" + model.ModelName + ")")
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

	// If an active model is known, switch to it
	if active := s.ModelManager.GetActive(); active != nil {
		// Create provider and model
		provider, err := app.CreateProvider(
			active.ProtocolType,
			active.APIKey,
			active.BaseURL,
			s.debugAPI,
			s.proxyURL,
		)
		if err != nil {
			s.writeError("Failed to create provider: " + err.Error())
			return
		}

		newModel, err := provider.LanguageModel(context.Background(), active.ModelName)
		if err != nil {
			s.writeError("Failed to create language model: " + err.Error())
			return
		}

		// Switch to the model
		s.SwitchModel(newModel, active.BaseURL, active.ModelName, s.baseTools, s.systemPrompt)
		s.writeNotify("Loaded models and switched to: " + active.Name)
		return
	}
	s.sendSystemInfo()
}

// handleTaskQueueGetAll sends all queued items to the adaptor via TagSystemData
func (s *Session) handleTaskQueueGetAll() {
	items := s.GetQueueItems()

	// Create a serializable representation
	type QueueItemData struct {
		QueueID   string `json:"queue_id"`
		Type      string `json:"type"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}

	data := make([]QueueItemData, len(items))
	for i, item := range items {
		var itemType, content string
		switch t := item.Task.(type) {
		case UserPrompt:
			itemType = "prompt"
			content = t.Text
		case CommandPrompt:
			itemType = "command"
			content = t.Command
		}
		data[i] = QueueItemData{
			QueueID:   item.QueueID,
			Type:      itemType,
			Content:   content,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
	}

	// Send via TagSystemData
	jsonData, err := json.Marshal(map[string]interface{}{
		"type":  "taskqueue_list",
		"items": data,
	})
	if err != nil {
		s.writeError(fmt.Sprintf("failed to marshal queue items: %v", err))
		return
	}
	stream.WriteTLV(s.Output, stream.TagSystemData, string(jsonData))
	s.Output.Flush()
}

// handleTaskQueueDel deletes a queue item by ID
func (s *Session) handleTaskQueueDel(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :taskqueue_del <queue_id>")
		return
	}

	queueID := args[0]
	if s.DeleteQueueItem(queueID) {
		// Send system notification about the deletion
		s.sendSystemInfo()
	} else {
		s.writeError(fmt.Sprintf("queue item %s not found", queueID))
	}
}
