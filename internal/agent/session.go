package agent

// Session manages conversation state and task execution.
//
// Dependency flow:
//
//	model.conf --(ModelManager)--> available models
//	      ^                               |
//	      |                               v
//	runtime.conf --(RuntimeManager)--> active model name
//	      |                               |
//	      +--------(Session)--------------+
//
// Session is responsible for:
//   - Reading TLV input and turning it into tasks (prompts/commands)
//   - Queueing and running tasks with cancellation support
//   - Streaming model output and system status back over TLV
//   - Delegating model listing/switching to ModelManager + RuntimeManager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	debugpkg "github.com/alayacore/alayacore/internal/debug"
	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Types
// ============================================================================

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

// UserPrompt is a user text input task
type UserPrompt struct {
	Text    string
	queueID string
}

func (UserPrompt) isTask() {}

func (u UserPrompt) GetQueueID() string { return u.queueID }

// CommandPrompt is a command task
type CommandPrompt struct {
	Command string
	queueID string
}

func (CommandPrompt) isTask() {}

func (c CommandPrompt) GetQueueID() string { return c.queueID }

// QueueItemInfo holds serializable queue item data for clients.
type QueueItemInfo struct {
	QueueID   string `json:"queue_id"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// SystemInfo holds session state for clients.
type SystemInfo struct {
	ContextTokens     int64           `json:"context"`
	ContextLimit      int64           `json:"context_limit"`
	TotalTokens       int64           `json:"total"`
	QueueItems        []QueueItemInfo `json:"queue_items,omitempty"`
	InProgress        bool            `json:"in_progress"`
	CurrentStep       int             `json:"current_step,omitempty"`
	MaxSteps          int             `json:"max_steps,omitempty"`
	Models            []ModelInfo     `json:"models,omitempty"`
	ActiveModelID     int             `json:"active_model_id,omitempty"`
	ActiveModelConfig *ModelConfig    `json:"active_model_config,omitempty"`
	ActiveModelName   string          `json:"active_model_name,omitempty"`
	HasModels         bool            `json:"has_models"`
	ModelConfigPath   string          `json:"model_config_path,omitempty"`
}

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt time.Time `config:"created_at"`
	UpdatedAt time.Time `config:"updated_at"`
}

// SessionData is the persisted form of a Session.
type SessionData struct {
	SessionMeta
	Messages  []llm.Message
	TLVChunks []TLVChunk // Parsed TLV for direct display (avoids reconstruction)
}

// TLVChunk represents a single TLV message for display.
type TLVChunk struct {
	Tag   string
	Value string
}

// ============================================================================
// Session Struct
// ============================================================================

// Session manages conversation state and task execution.
type Session struct {
	Messages             []llm.Message
	Agent                *llm.Agent
	Provider             llm.Provider
	SessionFile          string
	CreatedAt            time.Time
	TotalSpent           llm.Usage
	ContextTokens        int64
	ContextLimit         int64
	Input                stream.Input
	Output               stream.Output
	ModelManager         *ModelManager
	RuntimeManager       *RuntimeManager
	baseTools            []llm.Tool
	systemPrompt         string
	extraSystemPrompt    string
	debugAPI             bool
	autoSummarizeEnabled bool
	compactEnabled       bool
	autoSaveEnabled      bool
	maxSteps             int
	proxyURL             string

	taskQueue     []QueueItem
	cond          *sync.Cond         // signals when taskQueue becomes non-empty or pausedOnError clears
	sessionCtx    context.Context    // canceled when input is exhausted
	sessionCancel context.CancelFunc // idempotent cancel
	runnerDone    chan struct{}      // closed when taskRunner exits
	inProgress    bool
	pausedOnError bool           // set when a provider/network error occurs; blocks taskRunner until user acts
	taskWg        sync.WaitGroup // tracks in-flight runTask
	cancelCurrent func()
	nextPromptID  uint64
	nextQueueID   uint64
	currentStep   int
	mu            sync.Mutex
}

// WaitDone blocks until the taskRunner has finished processing all queued tasks
// and exited. This should be called after closing the input.
func (s *Session) WaitDone() {
	<-s.runnerDone
}

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
func LoadOrNewSession(baseTools []llm.Tool, systemPrompt string, extraSystemPrompt string, maxSteps int, input stream.Input, output stream.Output, sessionFile string, modelConfigPath, runtimeConfigPath string, debugAPI bool, autoSummarize bool, autoSave bool, noCompact bool, proxyURL string) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			return RestoreFromSession(baseTools, systemPrompt, extraSystemPrompt, maxSteps, input, output, data, sessionFile, modelConfigPath, runtimeConfigPath, debugAPI, autoSummarize, autoSave, noCompact, proxyURL), sessionFile
		}
	}
	return NewSession(baseTools, systemPrompt, extraSystemPrompt, maxSteps, input, output, sessionFile, modelConfigPath, runtimeConfigPath, debugAPI, autoSummarize, autoSave, noCompact, proxyURL), sessionFile
}

// NewSession creates a fresh session.
func NewSession(baseTools []llm.Tool, systemPrompt string, extraSystemPrompt string, maxSteps int, input stream.Input, output stream.Output, sessionFile string, modelConfigPath, runtimeConfigPath string, debugAPI bool, autoSummarize bool, autoSave bool, noCompact bool, proxyURL string) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		SessionFile:          sessionFile,
		CreatedAt:            time.Now(),
		Input:                input,
		Output:               output,
		ModelManager:         NewModelManager(modelConfigPath),
		RuntimeManager:       NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:            baseTools,
		systemPrompt:         systemPrompt,
		extraSystemPrompt:    extraSystemPrompt,
		debugAPI:             debugAPI,
		autoSummarizeEnabled: autoSummarize,
		compactEnabled:       !noCompact,
		autoSaveEnabled:      autoSave,
		proxyURL:             proxyURL,
		maxSteps:             maxSteps,
		taskQueue:            make([]QueueItem, 0),
		sessionCtx:           sessionCtx,
		sessionCancel:        sessionCancel,
		runnerDone:           make(chan struct{}),
	}
	s.initModelManager()
	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(baseTools []llm.Tool, systemPrompt string, extraSystemPrompt string, maxSteps int, input stream.Input, output stream.Output, data *SessionData, sessionFile string, modelConfigPath, runtimeConfigPath string, debugAPI bool, autoSummarize bool, autoSave bool, noCompact bool, proxyURL string) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		Messages:             data.Messages,
		SessionFile:          sessionFile,
		CreatedAt:            data.CreatedAt,
		Input:                input,
		Output:               output,
		ModelManager:         NewModelManager(modelConfigPath),
		RuntimeManager:       NewRuntimeManager(runtimeConfigPath, modelConfigPath),
		baseTools:            baseTools,
		systemPrompt:         systemPrompt,
		extraSystemPrompt:    extraSystemPrompt,
		debugAPI:             debugAPI,
		autoSummarizeEnabled: autoSummarize,
		compactEnabled:       !noCompact,
		autoSaveEnabled:      autoSave,
		proxyURL:             proxyURL,
		maxSteps:             maxSteps,
		taskQueue:            make([]QueueItem, 0),
		sessionCtx:           sessionCtx,
		sessionCancel:        sessionCancel,
		runnerDone:           make(chan struct{}),
	}
	s.initModelManager()
	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()

	// Send TLV chunks directly to output (avoids reconstruction)
	for _, chunk := range data.TLVChunks {
		//nolint:errcheck // Best effort write, errors ignored
		_ = stream.WriteTLV(s.Output, chunk.Tag, chunk.Value)
	}
	if len(data.TLVChunks) > 0 {
		s.Output.Flush()
	}
	return s
}

// ============================================================================
// Model Management
// ============================================================================

// SwitchModel switches the session to use a new model.
//
// DEADLOCK GOTCHA: Don't hold mutex while calling methods that may need the same mutex.
// Pattern: lock → update fields → unlock → call methods.
func (s *Session) SwitchModel(modelConfig *ModelConfig) error {
	if err := s.initAgentFromConfig(modelConfig); err != nil {
		return err
	}
	s.applyModelContextLimit(modelConfig)
	s.sendSystemInfo()
	return nil
}

func (s *Session) initModelManager() {
	if s.ModelManager == nil || s.RuntimeManager == nil {
		return
	}

	activeModelName := s.RuntimeManager.GetActiveModel()
	if activeModelName != "" {
		if err := s.ModelManager.SetActiveByName(activeModelName); err == nil {
			return
		}
	}
	s.ModelManager.SetActiveToFirst()
}

// GetRuntimeManager returns the runtime manager for the session
func (s *Session) GetRuntimeManager() *RuntimeManager {
	return s.RuntimeManager
}

func (s *Session) ensureAgentInitialized() string {
	s.mu.Lock()
	if s.Agent != nil && s.Provider != nil {
		s.mu.Unlock()
		return ""
	}
	s.mu.Unlock()

	if s.ModelManager == nil {
		return "Model manager not initialized"
	}

	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		return "No model configured. Please add a model to ~/.alayacore/model.conf"
	}

	provider, err := createProviderFromConfig(activeModel, s.debugAPI, s.proxyURL)
	if err != nil {
		return "Failed to create provider: " + err.Error()
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             s.baseTools,
		SystemPrompt:      s.systemPrompt,
		ExtraSystemPrompt: s.extraSystemPrompt,
		MaxSteps:          s.maxSteps,
	})

	s.mu.Lock()
	s.Agent = agent
	s.Provider = provider
	s.mu.Unlock()

	s.applyModelContextLimit(activeModel)
	return ""
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, err := createProviderFromConfig(modelConfig, s.debugAPI, s.proxyURL)
	if err != nil {
		return err
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             s.baseTools,
		SystemPrompt:      s.systemPrompt,
		ExtraSystemPrompt: s.extraSystemPrompt,
		MaxSteps:          s.maxSteps,
	})

	s.mu.Lock()
	s.Agent = agent
	s.Provider = provider
	s.mu.Unlock()

	return nil
}

func (s *Session) applyModelContextLimit(model *ModelConfig) {
	if model == nil || model.ContextLimit <= 0 {
		return
	}
	s.mu.Lock()
	s.ContextLimit = int64(model.ContextLimit)
	s.mu.Unlock()
}

func createProviderFromConfig(config *ModelConfig, debugAPI bool, proxyURL string) (llm.Provider, error) {
	var client *http.Client
	var err error
	if proxyURL != "" {
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
		}
	} else if debugAPI {
		client = debugpkg.NewHTTPClient()
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:        config.ProtocolType,
		APIKey:      config.APIKey,
		BaseURL:     config.BaseURL,
		Model:       config.ModelName,
		HTTPClient:  client,
		PromptCache: config.PromptCache,
		MaxTokens:   config.MaxTokens,
	})
}

// ============================================================================
// Input Processing
// ============================================================================

// isCommandImmediate returns true if the command should be handled immediately
// without queuing. Immediate commands are those that control task execution
// (cancel, continue) or query/modify session state (model_load, taskqueue operations).
func isCommandImmediate(cmd string) bool {
	// Extract the command name (first word) for commands that accept arguments.
	name := cmd
	if idx := strings.IndexByte(cmd, ' '); idx >= 0 {
		name = cmd[:idx]
	}
	switch name {
	case commandNameCancel, commandNameCancelAll, commandNameModelLoad, commandNameTaskQueueGetAll:
		return true
	}
	return strings.HasPrefix(cmd, commandNameTaskQueueDel+" ") || strings.HasPrefix(cmd, commandNameModelSet+" ")
}

func (s *Session) readFromInput() {
	defer func() {
		s.sessionCancel()
		s.cond.Signal()
	}()
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.NewSessionErrorf("input", "Invalid input tag: %s", tag).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			if isCommandImmediate(cmd) {
				s.handleCommand(context.Background(), cmd)
			} else {
				s.submitDeferredCommand(cmd)
			}
		} else {
			s.submitTask(UserPrompt{Text: value})
		}
	}
}

// ============================================================================
// Task Queue
// ============================================================================

func (s *Session) submitTask(task Task) {
	s.mu.Lock()
	queueEmpty := len(s.taskQueue) == 0
	// Clear paused-on-error only if queue was empty (new task will run immediately).
	// This must happen before enqueueTask signals the condition variable so
	// taskRunner sees consistent state when it wakes.
	if queueEmpty {
		s.pausedOnError = false
	}
	s.mu.Unlock()

	s.enqueueTask(task, false)
}

// submitDeferredCommand enqueues a deferred command at the front of the task queue.
// Deferred commands (e.g. :continue, :summarize) can only run when no task is
// currently in progress. They are placed at the front so they run ahead of
// any accumulated user prompts.
func (s *Session) submitDeferredCommand(cmd string) {
	s.mu.Lock()
	if s.inProgress && !s.pausedOnError {
		s.mu.Unlock()
		s.writeError("Cannot run command while a task is running. Please wait or cancel first.")
		return
	}
	s.mu.Unlock()

	s.enqueueTask(CommandPrompt{Command: cmd}, true)
}

// enqueueTask adds a task to the queue. When front is true, the task is
// placed at the front so it runs before previously queued items.
func (s *Session) enqueueTask(task Task, front bool) {
	s.mu.Lock()

	s.nextQueueID++
	queueID := fmt.Sprintf("Q%d", s.nextQueueID)

	switch t := task.(type) {
	case UserPrompt:
		t.queueID = queueID
		task = t
	case CommandPrompt:
		t.queueID = queueID
		task = t
	}

	item := QueueItem{
		Task:      task,
		QueueID:   queueID,
		CreatedAt: time.Now(),
	}

	if front {
		s.taskQueue = append([]QueueItem{item}, s.taskQueue...)
	} else {
		s.taskQueue = append(s.taskQueue, item)
	}
	s.cond.Signal()
	s.mu.Unlock()
	s.sendSystemInfo()
}

func (s *Session) taskRunner() {
	defer close(s.runnerDone)
	for {
		task, ok := s.waitForNextTask()
		if !ok {
			return
		}
		s.runTask(task)
		if !s.hasQueuedTasks() {
			s.setInProgress(false)
		}
	}
}

func (s *Session) waitForNextTask() (QueueItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if !s.hasRunnableItemLocked() {
			if s.sessionCtx.Err() != nil {
				return QueueItem{}, false
			}
			s.cond.Wait()
			continue
		}
		item := s.taskQueue[0]
		s.taskQueue = s.taskQueue[1:]
		s.inProgress = true
		return item, true
	}
}

// hasRunnableItemLocked reports whether the front of the task queue can be
// dequeued right now.  Commands are always runnable; other tasks require
// pausedOnError to be clear.
//
// Must be called with s.mu held.
func (s *Session) hasRunnableItemLocked() bool {
	if len(s.taskQueue) == 0 {
		return false
	}
	_, isCommand := s.taskQueue[0].Task.(CommandPrompt)
	return isCommand || !s.pausedOnError
}

func (s *Session) hasQueuedTasks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taskQueue) > 0
}

func (s *Session) setInProgress(v bool) {
	s.mu.Lock()
	changed := s.inProgress != v
	s.inProgress = v
	s.mu.Unlock()
	if changed {
		s.sendSystemInfo()
	}
}

func (s *Session) runTask(item QueueItem) {
	s.taskWg.Add(1)
	defer s.taskWg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancelCurrent = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancelCurrent = nil
		s.mu.Unlock()
	}()

	// Echo user prompts before any work so output ordering is correct even if
	// the task is canceled during initialization.
	if prompt, ok := item.Task.(UserPrompt); ok {
		s.signalPromptStart(prompt.Text)
	}

	s.sendSystemInfo()

	errMsg := s.ensureAgentInitialized()
	if errMsg != "" {
		s.writeError(errMsg)
		s.sendSystemInfo()
		return
	}

	s.mu.Lock()
	s.currentStep = 0
	s.mu.Unlock()

	switch t := item.Task.(type) {
	case UserPrompt:
		s.handleUserPrompt(ctx, t.Text)
	case CommandPrompt:
		s.handleCommand(ctx, t.Command)
	}

	if ctx.Err() == context.Canceled {
		s.appendCancelMessage()
	}

	s.autoSaveIfEnabled()
}

// autoSaveIfEnabled saves the session to file if auto-save is enabled and a session file is set.
func (s *Session) autoSaveIfEnabled() {
	if !s.autoSaveEnabled || s.SessionFile == "" {
		return
	}
	if err := s.saveSessionToFile(s.SessionFile); err != nil {
		s.writeNotifyf("Auto-save failed: %v", err)
	}
}

func (s *Session) appendCancelMessage() {
	if len(s.Messages) == 0 {
		return
	}
	if s.Messages[len(s.Messages)-1].Role == llm.RoleUser {
		s.Messages = append(s.Messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "The user canceled."}},
		})
	}
}

// GetQueueItems returns all queued items
func (s *Session) GetQueueItems() []QueueItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]QueueItem, len(s.taskQueue))
	copy(items, s.taskQueue)
	return items
}

// DeleteQueueItem removes a queue item by ID
func (s *Session) DeleteQueueItem(queueID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, item := range s.taskQueue {
		if item.QueueID == queueID {
			s.taskQueue = append(s.taskQueue[:i], s.taskQueue[i+1:]...)
			return true
		}
	}
	return false
}

// ============================================================================
// Prompt Processing
// ============================================================================

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx)
	}

	s.Messages = append(s.Messages, llm.NewUserMessage(prompt))

	_, err := s.processPrompt(ctx, s.Messages)

	s.Messages = cleanIncompleteToolCalls(s.Messages)
	s.compactHistory()

	if err != nil {
		s.writeError(err.Error())
		s.mu.Lock()
		s.pausedOnError = true
		s.mu.Unlock()
		s.sendSystemInfo()
		return
	}
}

func (s *Session) shouldAutoSummarize() bool {
	return s.autoSummarizeEnabled && s.ContextLimit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= s.ContextLimit*65/100
}

func (s *Session) doAutoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens) * 100 / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, s.ContextLimit, usage)
	s.summarize(ctx)
}

func (s *Session) processPrompt(ctx context.Context, history []llm.Message) (int64, error) {
	promptID := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	assembleID := func(id string) string {
		return "[:" + strconv.FormatUint(promptID, 10) + "-" + strconv.FormatInt(int64(stepCount), 10) + "-" + id + ":]"
	}

	_, err := s.Agent.Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, assembleID("t")+delta)
			s.Output.Flush()
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, assembleID("r")+delta)
			s.Output.Flush()
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			s.writeToolCall(toolName, string(input), toolCallID)
			s.Output.Flush()
			return nil
		},
		OnToolResult: func(toolCallID string, output llm.ToolResultOutput) error {
			status := "success"
			if textOutput, ok := output.(llm.ToolResultOutputText); ok {
				s.writeToolOutput(toolCallID, textOutput.Text)
			} else if errOutput, ok := output.(llm.ToolResultOutputError); ok {
				status = "error"
				s.writeToolOutput(toolCallID, errOutput.Error)
			}
			s.writeToolResult(toolCallID, status)
			return nil
		},
		OnStepStart: func(step int) error {
			stepCount = step
			s.mu.Lock()
			s.currentStep = step
			s.mu.Unlock()
			s.sendSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			s.trackUsage(usage)
			if len(messages) > 0 {
				s.Messages = append(s.Messages, messages...)
			}
			outputTokens += usage.OutputTokens
			return nil
		},
	})

	s.Output.Flush()

	if err != nil {
		return 0, err
	}

	return outputTokens, nil
}

// ============================================================================
// Output Helpers
// ============================================================================

func (s *Session) signalPromptStart(prompt string) {
	s.writeGapped(stream.TagTextUser, prompt)
}

func (s *Session) writeError(msg string) {
	s.writeGapped(stream.TagSystemError, msg)
}

func (s *Session) writeNotify(msg string) {
	s.writeGapped(stream.TagSystemNotify, msg)
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

func (s *Session) writeGapped(tag string, msg string) {
	if s.Output == nil {
		return
	}
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, tag, msg)
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	// Send tool call as JSON via FC tag
	tc := toolCallData{
		ID:    id,
		Name:  toolName,
		Input: input,
	}
	jsonData, _ := json.Marshal(tc) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionCall, string(jsonData))
	s.Output.Flush()
	s.writeToolResult(id, "pending")
}

func (s *Session) writeToolOutput(toolCallID string, output string) {
	// Send tool result as JSON via FR tag
	tr := toolResultData{
		ID:     toolCallID,
		Output: output,
	}
	jsonData, _ := json.Marshal(tr) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionResult, string(jsonData))
	s.Output.Flush()
}

func (s *Session) writeToolResult(toolCallID string, status string) {
	if s.Output == nil {
		return
	}
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagFunctionState, "[:"+toolCallID+":]"+status)
	s.Output.Flush()
}

func (s *Session) trackUsage(usage llm.Usage) {
	s.mu.Lock()
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	// Only overwrite ContextTokens if the provider reported a non-zero value.
	// OpenAI-compatible providers (e.g. GLM-5.1) may omit the usage field from
	// SSE chunks entirely. Go's json.Unmarshal leaves absent fields at their
	// zero values, so Usage arrives as {InputTokens: 0, OutputTokens: 0, ...}.
	// Without this guard, ContextTokens would be reset to 0.
	newContext := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
	if newContext > 0 {
		s.ContextTokens = newContext
	}
	s.mu.Unlock()
	s.sendSystemInfo()
}

func (s *Session) sendSystemInfo() {
	s.sendSystemInfoInternal(nil)
}

func (s *Session) sendSystemInfoInternal(activeModelConfig *ModelConfig) {
	if s.Output == nil {
		return
	}

	var models []ModelInfo
	var activeID int
	var activeModelName string
	var modelConfigPath string
	var hasModels bool

	if s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
		if activeModelConfig != nil {
			activeModelName = activeModelConfig.Name
		} else if activeModel := s.ModelManager.GetActive(); activeModel != nil {
			activeModelName = activeModel.Name
		}
		modelConfigPath = s.ModelManager.GetFilePath()
		hasModels = s.ModelManager.HasModels()
	}

	s.mu.Lock()
	queueItems := make([]QueueItemInfo, len(s.taskQueue))
	for i, item := range s.taskQueue {
		var itemType, content string
		switch t := item.Task.(type) {
		case UserPrompt:
			itemType = "prompt"
			content = t.Text
		case CommandPrompt:
			itemType = "command"
			content = t.Command
		}
		queueItems[i] = QueueItemInfo{
			QueueID:   item.QueueID,
			Type:      itemType,
			Content:   content,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
	}
	inProgress := s.inProgress
	contextTokens := s.ContextTokens
	contextLimit := s.ContextLimit
	totalTokens := s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens
	currentStep := s.currentStep
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens:     contextTokens,
		ContextLimit:      contextLimit,
		TotalTokens:       totalTokens,
		QueueItems:        queueItems,
		InProgress:        inProgress,
		CurrentStep:       currentStep,
		MaxSteps:          s.maxSteps,
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: activeModelConfig,
		ActiveModelName:   activeModelName,
		HasModels:         hasModels,
		ModelConfigPath:   modelConfigPath,
	}
	data, _ := json.Marshal(info) //nolint:errcheck // Best effort marshal, errors ignored
	//nolint:errcheck // Best effort write, errors ignored
	_ = stream.WriteTLV(s.Output, stream.TagSystemData, string(data))
	s.Output.Flush()
}

// cleanIncompleteToolCalls removes incomplete tool calls from messages.
func cleanIncompleteToolCalls(messages []llm.Message) []llm.Message {
	unmatchedCalls := make(map[string]bool)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case llm.ToolCallPart:
				unmatchedCalls[p.ToolCallID] = true
			case llm.ToolResultPart:
				delete(unmatchedCalls, p.ToolCallID)
			}
		}
	}

	if len(unmatchedCalls) == 0 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		hasUnmatchedCall := false
		for _, part := range msg.Content {
			if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
				hasUnmatchedCall = true
				break
			}
		}

		if hasUnmatchedCall {
			filteredParts := make([]llm.ContentPart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
					continue
				}
				filteredParts = append(filteredParts, part)
			}

			if len(filteredParts) > 0 {
				messages[i].Content = filteredParts
				return messages[:i+1]
			}
			messages = messages[:i]
			continue
		}

		return messages[:i+1]
	}

	return messages
}

// compactHistory truncates old tool result outputs to save context tokens.
// Only tool results from the most recent steps are kept in full; older ones
// are truncated to a summary. This prevents unbounded context growth in
// long agent sessions where each step's tool I/O accumulates.
//
// activate_skill results are never truncated — the LLM needs full skill
// instructions to follow them correctly in subsequent steps.
func (s *Session) compactHistory() {
	if !s.compactEnabled {
		return
	}
	const (
		recentSteps = 6   // Keep last N messages (3 steps: prompt/tool-call/tool-result) intact
		maxOldLen   = 500 // Truncate old tool results to this many characters
	)
	msgs := s.Messages
	if len(msgs) <= recentSteps {
		return
	}
	// Build a set of tool call IDs for activate_skill so their results are preserved.
	skillCallIDs := s.collectSkillCallIDs(msgs)

	for i := 0; i < len(msgs)-recentSteps; i++ {
		if msgs[i].Role != llm.RoleTool {
			continue
		}
		for j, part := range msgs[i].Content {
			tr, ok := part.(llm.ToolResultPart)
			if !ok {
				continue
			}
			// Never truncate activate_skill results — the LLM needs full instructions
			if skillCallIDs[tr.ToolCallID] {
				continue
			}
			textOut, ok := tr.Output.(llm.ToolResultOutputText)
			if !ok || len(textOut.Text) <= maxOldLen {
				continue
			}
			truncated := textOut.Text[:maxOldLen]
			// Cut at last newline to avoid partial lines
			if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
				truncated = truncated[:idx]
			}
			truncated += "\n... [truncated for context efficiency]"
			msgs[i].Content[j] = llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: tr.ToolCallID,
				Output:     llm.ToolResultOutputText{Type: "text", Text: truncated},
			}
		}
	}
}

// collectSkillCallIDs scans messages for tool calls to activate_skill and returns
// their tool call IDs. This allows compactHistory to preserve their results.
func (s *Session) collectSkillCallIDs(msgs []llm.Message) map[string]bool {
	ids := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, part := range msg.Content {
			tc, ok := part.(llm.ToolCallPart)
			if !ok || tc.ToolName != "activate_skill" {
				continue
			}
			// Parse the input to confirm it's a valid activate_skill call
			var input struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(tc.Input, &input); err == nil && input.Name != "" {
				ids[tc.ToolCallID] = true
			}
		}
	}
	return ids
}

// ============================================================================
// Path Helpers
// ============================================================================

func expandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	usr, err := user.Current()
	if err != nil {
		return path
	}
	if path == "~" {
		return usr.HomeDir
	}
	return filepath.Join(usr.HomeDir, path[1:])
}
