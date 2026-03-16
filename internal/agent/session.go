package agent

// Session wires together the model, tools, IO streams, and the model/
// runtime managers. Rough dependency flow:
//
//   model.conf --(ModelManager)--> available models (no API keys in JSON)
//         ^                               |
//         |                               v
//   runtime.conf --(RuntimeManager)--> active model name
//         |                               |
//         +--------(Session)--------------+
//
// Session is responsible for:
//   - reading TLV input and turning it into tasks (prompts/commands)
//   - queueing and running tasks with cancellation support
//   - streaming model output and system status back over TLV
//   - delegating model listing/switching to ModelManager + RuntimeManager

import (
	"context"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/app"
	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/stream"
)

// Task represents a unit of work for the session.
type Task interface {
	isTask()
	GetQueueID() string
}

// QueueItem wraps a Task with metadata for queue management
type QueueItem struct {
	Task
	QueueID   string
	CreatedAt time.Time
}

type UserPrompt struct {
	Text    string
	queueID string
}

func (UserPrompt) isTask() {}

func (u UserPrompt) GetQueueID() string {
	return u.queueID
}

type CommandPrompt struct {
	Command string
	queueID string
}

func (CommandPrompt) isTask() {}

func (c CommandPrompt) GetQueueID() string {
	return c.queueID
}

// SystemInfo holds session state for clients.
type SystemInfo struct {
	ContextTokens     int64        `json:"context"`
	ContextLimit      int64        `json:"context_limit"`
	TotalTokens       int64        `json:"total"`
	QueueCount        int          `json:"queue"`
	InProgress        bool         `json:"in_progress"`
	Models            []ModelInfo  `json:"models,omitempty"`
	ActiveModelID     string       `json:"active_model_id,omitempty"`
	ActiveModelConfig *ModelConfig `json:"active_model_config,omitempty"` // Full config (with API key), only when model changes
	ActiveModelName   string       `json:"active_model_name,omitempty"`   // Name of active model
	HasModels         bool         `json:"has_models"`                    // Whether models are configured
	ModelConfigPath   string       `json:"model_config_path,omitempty"`   // Path to model.conf
}

// Session manages conversation state and task execution.
type Session struct {
	Messages       []fantasy.Message
	Agent          fantasy.Agent
	SessionFile    string
	TotalSpent     fantasy.Usage
	ContextTokens  int64
	ContextLimit   int64
	Input          stream.Input
	Output         stream.Output
	ModelManager   *ModelManager
	RuntimeManager *RuntimeManager
	baseTools      []fantasy.AgentTool
	systemPrompt   string
	debugAPI       bool
	proxyURL       string

	taskQueue     []QueueItem
	taskAvailable chan struct{}
	done          chan struct{}
	inProgress    bool
	cancelCurrent func()
	nextPromptID  uint64
	nextQueueID   uint64
	mu            sync.Mutex
}

// SessionMeta is the YAML frontmatter metadata.
type SessionMeta struct {
	UpdatedAt time.Time `yaml:"updated_at"`
}

// SessionData is the persisted form of a Session.
type SessionData struct {
	Messages  []fantasy.Message
	UpdatedAt time.Time
}

// LoadOrNewSession loads a session from file or creates a new one.
func LoadOrNewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string, debugAPI bool, proxyURL string) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			return RestoreFromSession(model, baseTools, systemPrompt, input, output, data, sessionFile, contextLimit, modelConfigPath, runtimeConfigPath, debugAPI, proxyURL), sessionFile
		}
	}
	return NewSession(model, baseTools, systemPrompt, input, output, sessionFile, contextLimit, modelConfigPath, runtimeConfigPath, debugAPI, proxyURL), sessionFile
}

// NewSession creates a fresh session.
func NewSession(_ fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string, debugAPI bool, proxyURL string) *Session {
	s := &Session{
		SessionFile:    sessionFile,
		ContextLimit:   contextLimit,
		Input:          input,
		Output:         output,
		ModelManager:   NewModelManager(modelConfigPath),
		RuntimeManager: NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:      baseTools,
		systemPrompt:   systemPrompt,
		debugAPI:       debugAPI,
		proxyURL:       proxyURL,
		taskQueue:      make([]QueueItem, 0),
		taskAvailable:  make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	s.initModelManager()
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(_ fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt string, input stream.Input, output stream.Output, data *SessionData, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string, debugAPI bool, proxyURL string) *Session {
	s := &Session{
		Messages:       data.Messages,
		SessionFile:    sessionFile,
		ContextLimit:   contextLimit,
		Input:          input,
		Output:         output,
		ModelManager:   NewModelManager(modelConfigPath),
		RuntimeManager: NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:      baseTools,
		systemPrompt:   systemPrompt,
		debugAPI:       debugAPI,
		proxyURL:       proxyURL,
		taskQueue:      make([]QueueItem, 0),
		taskAvailable:  make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	s.initModelManager()
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()

	if len(s.Messages) > 0 {
		s.displayMessages()
		s.Output.Flush()
	}
	return s
}

// ensureAgentInitialized lazily initializes the agent if not already done.
// Returns an error message if initialization fails, or empty string on success.
func (s *Session) ensureAgentInitialized() string {
	s.mu.Lock()
	// Already initialized
	if s.Agent != nil {
		s.mu.Unlock()
		return ""
	}
	s.mu.Unlock()

	// Get the active model from ModelManager (no lock needed for this)
	if s.ModelManager == nil {
		return "Model manager not initialized"
	}

	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		return "No model configured. Please add a model to ~/.alayacore/model.conf"
	}

	// Create provider and model
	provider, err := app.CreateProvider(
		activeModel.ProtocolType,
		activeModel.APIKey,
		activeModel.BaseURL,
		s.debugAPI,
		s.proxyURL,
	)
	if err != nil {
		return "Failed to create provider: " + err.Error()
	}

	model, err := provider.LanguageModel(context.Background(), activeModel.ModelName)
	if err != nil {
		return "Failed to create language model: " + err.Error()
	}

	s.mu.Lock()
	s.Agent = fantasy.NewAgent(model,
		fantasy.WithTools(s.baseTools...),
		fantasy.WithSystemPrompt(s.systemPrompt),
	)
	s.mu.Unlock()

	s.applyModelContextLimit(activeModel)
	return ""
}

// initAgentFromModel creates an agent from a specific model (used by SwitchModel).
func (s *Session) initAgentFromModel(model fantasy.LanguageModel) {
	s.Agent = fantasy.NewAgent(model,
		fantasy.WithTools(s.baseTools...),
		fantasy.WithSystemPrompt(s.systemPrompt),
	)
}

// applyModelContextLimit updates the session's ContextLimit from a model config
// when the model defines a positive context limit. A zero limit means "no
// override" and keeps the existing session-level limit (e.g. from CLI).
func (s *Session) applyModelContextLimit(model *ModelConfig) {
	if model == nil || model.ContextLimit <= 0 {
		return
	}
	s.mu.Lock()
	s.ContextLimit = int64(model.ContextLimit)
	s.mu.Unlock()
}

// initModelManager initializes the ModelManager by setting the active model from runtime config.
// Falls back to the first model if runtime.conf has no active model set.
func (s *Session) initModelManager() {
	if s.ModelManager == nil || s.RuntimeManager == nil {
		return
	}

	// Load active model name from runtime.conf
	activeModelName := s.RuntimeManager.GetActiveModel()
	if activeModelName != "" {
		// Set active model by name in ModelManager
		if err := s.ModelManager.SetActiveByName(activeModelName); err == nil {
			return // Successfully set from runtime.conf
		}
	}

	// Fallback to first model in the list
	s.ModelManager.SetActiveToFirst()
}

// SwitchModel switches the session to use a new model
func (s *Session) SwitchModel(model fantasy.LanguageModel, modelConfig *ModelConfig) {
	s.initAgentFromModel(model)
	if modelConfig != nil {
		s.applyModelContextLimit(modelConfig)
	}
	s.sendSystemInfo()
}

func (s *Session) readFromInput() {
	defer func() {
		// Signal background goroutines to stop.
		// This is safe even if taskRunner is currently blocked waiting for work.
		s.mu.Lock()
		select {
		case <-s.done:
			// already closed
		default:
			close(s.done)
		}
		s.mu.Unlock()
		s.signalTaskAvailable()
	}()
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return // Input closed
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.NewSessionErrorf("input", "Invalid input tag: %s (only %s is allowed)", tag, stream.TagTextUser).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			// These commands are immediate, not queued
			if cmd == "cancel" || cmd == "taskqueue_get_all" || strings.HasPrefix(cmd, "taskqueue_del ") {
				s.handleCommandSync(context.Background(), cmd)
			} else {
				s.submitTask(CommandPrompt{Command: cmd})
			}
		} else {
			s.submitTask(UserPrompt{Text: value})
		}
	}
}
