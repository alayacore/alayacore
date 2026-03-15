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
	"fmt"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
)

// Task represents a unit of work for the session.
type Task interface{ isTask() }

type UserPrompt string

func (UserPrompt) isTask() {}

type CommandPrompt struct{ Command string }

func (CommandPrompt) isTask() {}

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
	BaseURL        string
	ModelName      string
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

	taskQueue     []Task
	taskAvailable chan struct{}
	done          chan struct{}
	inProgress    bool
	cancelCurrent func()
	nextPromptID  uint64
	mu            sync.Mutex
}

// SessionMeta is the YAML frontmatter metadata.
type SessionMeta struct {
	BaseURL       string    `yaml:"base_url"`
	ModelName     string    `yaml:"model_name"`
	TotalTokens   int64     `yaml:"total_tokens"`
	ContextTokens int64     `yaml:"context_tokens"`
	CreatedAt     time.Time `yaml:"created_at"`
	UpdatedAt     time.Time `yaml:"updated_at"`
}

// SessionData is the persisted form of a Session.
type SessionData struct {
	BaseURL       string
	ModelName     string
	Messages      []fantasy.Message
	TotalSpent    fantasy.Usage
	ContextTokens int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
func LoadOrNewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			return RestoreFromSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, data, sessionFile, contextLimit, modelConfigPath, runtimeConfigPath), sessionFile
		}
	}
	return NewSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, sessionFile, contextLimit, modelConfigPath, runtimeConfigPath), sessionFile
}

// NewSession creates a fresh session.
func NewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string) *Session {
	s := &Session{
		BaseURL:        baseURL,
		ModelName:      modelName,
		SessionFile:    sessionFile,
		ContextLimit:   contextLimit,
		Input:          input,
		Output:         output,
		ModelManager:   NewModelManager(modelConfigPath),
		RuntimeManager: NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:      baseTools,
		systemPrompt:   systemPrompt,
		taskQueue:      make([]Task, 0),
		taskAvailable:  make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	s.initAgent(model, baseTools, systemPrompt)
	go s.readFromInput()
	go s.taskRunner()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, data *SessionData, sessionFile string, contextLimit int64, modelConfigPath, runtimeConfigPath string) *Session {
	s := &Session{
		Messages:       data.Messages,
		BaseURL:        baseURL,
		ModelName:      modelName,
		SessionFile:    sessionFile,
		TotalSpent:     data.TotalSpent,
		ContextTokens:  data.ContextTokens,
		ContextLimit:   contextLimit,
		Input:          input,
		Output:         output,
		ModelManager:   NewModelManager(modelConfigPath),
		RuntimeManager: NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:      baseTools,
		systemPrompt:   systemPrompt,
		taskQueue:      make([]Task, 0),
		taskAvailable:  make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	s.initAgent(model, baseTools, systemPrompt)
	go s.readFromInput()
	go s.taskRunner()

	if len(s.Messages) > 0 {
		s.displayMessages()
		s.Output.Flush()
	}
	return s
}

func (s *Session) initAgent(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt string) {
	s.Agent = fantasy.NewAgent(model,
		fantasy.WithTools(baseTools...),
		fantasy.WithSystemPrompt(systemPrompt),
	)
	// Initialize model manager with active model from runtime config
	s.initModelManager()
	// Send initial system info to adaptor
	// If model is nil and there's an active model config, send full config for adaptor to switch
	if model == nil && s.ModelManager != nil {
		if activeModel := s.ModelManager.GetActive(); activeModel != nil {
			s.applyModelContextLimit(activeModel)
			s.sendSystemInfoWithModel(activeModel)
			return
		}
	}
	s.sendSystemInfo()
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

// initModelManager initializes the ModelManager by setting the active model from runtime config
// This is called during session initialization and after loading models
func (s *Session) initModelManager() {
	if s.ModelManager == nil || s.RuntimeManager == nil {
		return
	}

	// Load active model name from runtime.conf
	activeModelName := s.RuntimeManager.GetActiveModel()
	if activeModelName != "" {
		// Set active model by name in ModelManager
		_ = s.ModelManager.SetActiveByName(activeModelName)
	}
}

// SwitchModel switches the session to use a new model
func (s *Session) SwitchModel(model fantasy.LanguageModel, baseURL, modelName string, baseTools []fantasy.AgentTool, systemPrompt string) {
	s.mu.Lock()
	s.BaseURL = baseURL
	s.ModelName = modelName
	s.mu.Unlock()

	s.initAgent(model, baseTools, systemPrompt)
}

// SwitchModel switches the session to use a new model

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
			s.writeError(fmt.Sprintf("Invalid input tag: %s (only %s is allowed)", tag, stream.TagTextUser))
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			// :cancel is immediate, other commands are queued
			if string(value[1:]) == "cancel" {
				s.handleCommandSync(context.Background(), "cancel")
			} else {
				s.submitTask(CommandPrompt{Command: value[1:]})
			}
		} else {
			s.submitTask(UserPrompt(value))
		}
	}
}
