package agent

// Session I/O: command handling, prompt processing.

import (
	"context"
	"strconv"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Command Handling
// ============================================================================

func (s *Session) handleCommandSync(ctx context.Context, cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return
	}

	if s.dispatchCommand(ctx, cmd) {
		return
	}

	s.writeError(domainerrors.NewSessionErrorf("command", "unknown cmd <%s>", parts[0]).Error())
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
	s.writeError(domainerrors.ErrNothingToCancel.Error())
}

func (s *Session) cancelAllTasks() {
	// Clear the task queue first (while holding lock)
	s.mu.Lock()
	queueLen := len(s.taskQueue)
	s.taskQueue = make([]QueueItem, 0)
	inProgress := s.inProgress
	cancelCurrent := s.cancelCurrent
	s.mu.Unlock()

	// Then cancel current task (if running)
	currentCanceled := false
	if inProgress && cancelCurrent != nil {
		cancelCurrent()
		currentCanceled = true
		// Wait for runTask to finish so its output (errors, etc.)
		// appears before our summary notification.
		<-s.taskDone
	}

	// Send notification
	switch {
	case currentCanceled && queueLen > 0:
		s.writeNotifyf("Canceled current task and cleared %d queued tasks", queueLen)
	case currentCanceled:
		s.writeNotify("Canceled current task")
	case queueLen > 0:
		s.writeNotifyf("Cleared %d queued tasks", queueLen)
	default:
		s.writeError(domainerrors.ErrNothingToCancel.Error())
		return
	}

	s.sendSystemInfo()
}

func (s *Session) summarize(ctx context.Context) {
	prompt := `Summarize the conversation for continuation. The resuming instance has no prior context.

Provide:
1. **Task** — Original request and success criteria
2. **Done** — Completed items with specifics (file paths, function names, values)
3. **State** — Files created/modified/deleted, key decisions and rationale
4. **Blocked** — Unresolved errors, failing tests, open questions
5. **Next** — Ordered actions to resume

Rules:
- Prefer exact identifiers, file paths, and code snippets over prose descriptions
- Include error messages verbatim
- Skip completed exploration; only preserve findings that affect next steps`

	s.Messages = append(s.Messages, llm.NewUserMessage(prompt))

	beforeCount := len(s.Messages)

	outputTokens, err := s.processPrompt(ctx, s.Messages)
	if err != nil {
		s.writeError(err.Error())
		return
	}

	var lastAssistantMsg llm.Message
	for i := beforeCount; i < len(s.Messages); i++ {
		if s.Messages[i].Role == llm.RoleAssistant {
			lastAssistantMsg = s.Messages[i]
		}
	}

	s.Messages = []llm.Message{lastAssistantMsg}
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
			s.writeError(domainerrors.ErrNoSessionFile.Error())
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
		s.writeError(domainerrors.Wrapf("save", err, "failed to save session").Error())
	} else {
		s.writeNotifyf("Session saved to %s", path)
	}
}

func (s *Session) handleModelSet(args []string) {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_set <id>")
		return
	}

	s.mu.Lock()
	inProgress := s.inProgress
	s.mu.Unlock()
	if inProgress {
		s.writeError("Cannot switch model while a task is running. Please wait or cancel the current task.")
		return
	}

	modelIDStr := args[0]
	modelID, err := strconv.Atoi(modelIDStr)
	if err != nil {
		s.writeError(domainerrors.NewSessionErrorf("model_set", "invalid model ID: %s", modelIDStr).Error())
		return
	}
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(domainerrors.NewSessionErrorf("model_set", "model not found: %d", modelID).Error())
		return
	}

	if err := s.ModelManager.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	if s.RuntimeManager != nil {
		//nolint:errcheck // Best effort save, errors ignored
		_ = s.RuntimeManager.SetActiveModel(model.Name)
	}

	if err := s.SwitchModel(model); err != nil {
		s.writeError("Failed to switch model: " + err.Error())
		return
	}

	s.writeNotifyf("Switched to model: %s (%s)", model.Name, model.ModelName)
}

func (s *Session) handleModelLoad() {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	path := s.ModelManager.GetFilePath()
	if path == "" {
		s.writeError(domainerrors.ErrNoModelFilePath.Error())
		return
	}

	if err := s.ModelManager.LoadFromFile(path); err != nil {
		s.writeError(domainerrors.Wrapf("model_load", err, "failed to load models").Error())
		return
	}

	s.initModelManager()
	s.sendSystemInfo()
	s.writeNotify("Models reloaded from configuration file")
}

func (s *Session) handleTaskQueueGetAll() {
	s.sendSystemInfo()
}

func (s *Session) handleTaskQueueDel(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :taskqueue_del <queue_id>")
		return
	}

	queueID := args[0]
	if s.DeleteQueueItem(queueID) {
		s.sendSystemInfo()
	} else {
		s.writeError(domainerrors.NewSessionErrorf("taskqueue_del", "queue item %s not found", queueID).Error())
	}
}
