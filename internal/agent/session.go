package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
	"gopkg.in/yaml.v3"
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
}

// Session manages conversation state and task execution.
type Session struct {
	Messages      []fantasy.Message
	Agent         fantasy.Agent
	BaseURL       string
	ModelName     string
	SessionFile   string
	TotalSpent    fantasy.Usage
	ContextTokens int64
	ContextLimit  int64
	Input         stream.Input
	Output        stream.Output
	ModelManager  *ModelManager

	taskQueue         []Task
	taskAvailable     chan struct{}
	inProgress        bool
	cancelCurrent     func()
	skipAutoSummarize bool // Prevent auto-summarize loops
	mu                sync.Mutex
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
func LoadOrNewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			return RestoreFromSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, data, sessionFile, contextLimit), sessionFile
		}
	}
	return NewSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, sessionFile, contextLimit), sessionFile
}

// NewSession creates a fresh session.
func NewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string, contextLimit int64) *Session {
	s := &Session{
		BaseURL:       baseURL,
		ModelName:     modelName,
		SessionFile:   sessionFile,
		ContextLimit:  contextLimit,
		Input:         input,
		Output:        output,
		ModelManager:  NewModelManager(),
		taskQueue:     make([]Task, 0),
		taskAvailable: make(chan struct{}, 1),
	}
	s.initAgent(model, baseTools, systemPrompt)
	go s.readFromInput()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, data *SessionData, sessionFile string, contextLimit int64) *Session {
	s := &Session{
		Messages:      data.Messages,
		BaseURL:       baseURL,
		ModelName:     modelName,
		SessionFile:   sessionFile,
		TotalSpent:    data.TotalSpent,
		ContextTokens: data.ContextTokens,
		ContextLimit:  contextLimit,
		Input:         input,
		Output:        output,
		ModelManager:  NewModelManager(),
		taskQueue:     make([]Task, 0),
		taskAvailable: make(chan struct{}, 1),
	}
	s.initAgent(model, baseTools, systemPrompt)
	go s.readFromInput()

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
}

// SwitchModel switches the session to use a new model
func (s *Session) SwitchModel(model fantasy.LanguageModel, baseURL, modelName string, baseTools []fantasy.AgentTool, systemPrompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.BaseURL = baseURL
	s.ModelName = modelName
	s.initAgent(model, baseTools, systemPrompt)
}

// SwitchModel switches the session to use a new model

func (s *Session) readFromInput() {
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return // Input closed
		}
		if tag != stream.TagUserText {
			s.writeError(fmt.Sprintf("Invalid input tag: %c (only %c is allowed)", tag, stream.TagUserText))
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			s.submitCommand(value[1:])
		} else {
			s.submitTask(UserPrompt(value))
		}
	}
}

// ============================================================================
// Task Queue
// ============================================================================

func (s *Session) submitTask(task Task) {
	s.mu.Lock()
	queueLen := len(s.taskQueue)
	// Check queue capacity (max 10 tasks)
	if queueLen >= 10 {
		s.mu.Unlock()
		s.writeNotify("Busy. Cannot queue, try again shortly.")
		return
	}
	s.taskQueue = append(s.taskQueue, task)
	s.signalTaskAvailable()
	s.mu.Unlock()

	if s.inProgress {
		s.writeNotify("Queued. Previous task in progress. Will run after completion.")
		s.sendSystemInfo()
	} else {
		go s.runTaskQueue()
	}
}

// prependTasks inserts tasks at the front of the queue (for auto-summarize)
func (s *Session) prependTasks(tasks ...Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskQueue = append(tasks, s.taskQueue...)
	s.signalTaskAvailable()
}

// signalTaskAvailable notifies the task runner that a new task is available
func (s *Session) signalTaskAvailable() {
	select {
	case s.taskAvailable <- struct{}{}:
	default:
	}
}

func (s *Session) runTaskQueue() {
	s.inProgress = true
	s.sendSystemInfo()
	defer func() {
		s.inProgress = false
		s.sendSystemInfo()
	}()

	for {
		s.mu.Lock()
		if len(s.taskQueue) == 0 {
			s.mu.Unlock()
			// Wait for new task or shutdown
			select {
			case <-s.taskAvailable:
				continue
			case <-time.After(100 * time.Millisecond):
				return // Queue empty, exit
			}
		}
		task := s.taskQueue[0]
		s.taskQueue = s.taskQueue[1:]
		s.mu.Unlock()

		s.runTask(task)
	}
}

func (s *Session) runTask(task Task) {
	s.sendSystemInfo()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelCurrent = cancel
	defer func() { s.cancelCurrent = nil }()

	switch t := task.(type) {
	case UserPrompt:
		s.signalPromptStart(string(t))
		s.handleUserPrompt(ctx, string(t))
	case CommandPrompt:
		s.signalCommandStart(t.Command)
		s.handleCommandSync(ctx, t.Command)
	}

	if ctx.Err() == context.Canceled {
		s.appendCancelMessage()
	}
}

func (s *Session) appendCancelMessage() {
	if len(s.Messages) == 0 {
		return
	}
	if s.Messages[len(s.Messages)-1].Role == fantasy.MessageRoleUser {
		s.Messages = append(s.Messages, fantasy.Message{
			Role:    fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{fantasy.TextPart{Text: "The user canceled."}},
		})
	}
}

// ============================================================================
// Prompt Handling
// ============================================================================

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	// Auto-summarize when context usage reaches 80% of the limit
	// Skip if we've already attempted auto-summarize (prevents loops)
	if !s.skipAutoSummarize && s.ContextLimit > 0 && s.ContextTokens > 0 {
		threshold := s.ContextLimit * 80 / 100
		if s.ContextTokens >= threshold {
			s.writeNotify(fmt.Sprintf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...", s.ContextTokens, s.ContextLimit, float64(s.ContextTokens)*100/float64(s.ContextLimit)))
			// Set flag to prevent auto-summarize loop
			s.skipAutoSummarize = true
			// Put current task back to front, then insert summarize command before it
			s.prependTasks(CommandPrompt{Command: "summarize"}, UserPrompt(prompt))
			return
		}
	}

	// Clear the flag - we're either processing normally or after auto-summarize
	s.skipAutoSummarize = false

	s.Messages = append(s.Messages, fantasy.NewUserMessage(prompt))
	history := s.Messages[:len(s.Messages)-1]

	msg, _, err := s.processPrompt(ctx, prompt, history)
	if err != nil {
		s.writeError(err.Error())
		return
	}
	if msg.Role != "" {
		s.Messages = append(s.Messages, msg)
	}
}

var promptCount uint64

func (s *Session) processPrompt(ctx context.Context, prompt string, history []fantasy.Message) (fantasy.Message, fantasy.Usage, error) {
	call := fantasy.AgentStreamCall{Prompt: prompt}
	promptId := promptCount
	promptCount++

	var stepCount int = 0

	if len(history) > 0 {
		call.Messages = history
	}

	/// the final ID is [:promptId-stepCount-id:]
	assembleId := func(id string) string {
		return "[:" + strconv.FormatUint(promptId, 10) + "-" + strconv.FormatInt(int64(stepCount), 10) + "-" + id + ":]"
	}

	call.OnStepStart = func(step int) error {
		stepCount = step
		return nil
	}
	call.OnStepFinish = func(stepResult fantasy.StepResult) error {
		s.trackUsage(stepResult.Usage)
		return nil
	}

	// The `id` in the callback is not reliable, it does not work for some providers.
	// Here we only need to distinguish the delta type, so we give numbers directly.
	call.OnTextDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagAssistantText, assembleId("t")+text)
		s.Output.Flush()
		return nil
	}
	call.OnReasoningDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagReasoning, assembleId("r")+text)
		s.Output.Flush()
		return nil
	}
	call.OnToolCall = func(tc fantasy.ToolCallContent) error {
		s.writeToolCall(tc.ToolName, tc.Input, tc.ToolCallID)
		s.Output.Flush()
		return nil
	}

	result, err := s.Agent.Stream(ctx, call)
	if err != nil {
		return fantasy.Message{}, fantasy.Usage{}, err
	}
	s.Output.Flush()

	return s.extractAssistantMessage(result), result.TotalUsage, nil
}

func (s *Session) extractAssistantMessage(result *fantasy.AgentResult) fantasy.Message {
	if result == nil || len(result.Steps) == 0 {
		return fantasy.Message{}
	}
	for _, msg := range result.Steps[len(result.Steps)-1].Messages {
		if msg.Role == fantasy.MessageRoleAssistant {
			return msg
		}
	}
	return fantasy.Message{}
}

func (s *Session) trackUsage(usage fantasy.Usage) {
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	s.TotalSpent.TotalTokens += usage.TotalTokens
	s.TotalSpent.ReasoningTokens += usage.ReasoningTokens
	// ContextTokens tracks the total context size (input tokens sent to API)
	// This is what counts toward provider context limits
	s.ContextTokens = usage.InputTokens
	s.sendSystemInfo()
}

// ============================================================================
// Command Handling
// ============================================================================

func (s *Session) submitCommand(cmd string) {
	if cmd == "summarize" {
		s.submitTask(CommandPrompt{Command: cmd})
	} else {
		s.handleCommandSync(context.Background(), cmd)
	}
}

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
	case "model_get_all":
		s.handleModelGetAll()
	case "model_set":
		s.handleModelSet(parts[1:])
	case "model_load":
		s.handleModelLoad()
	default:
		s.writeError(fmt.Sprintf("unknown cmd <%s>", cmd))
	}
}

func (s *Session) cancelTask() {
	if s.inProgress && s.cancelCurrent != nil {
		s.cancelCurrent()
		return
	}
	s.writeError("nothing to cancel")
}

func (s *Session) summarize(ctx context.Context) {
	prompt := "Please summarize the conversation above in a concise manner. Return ONLY the summary, no introductions or explanations."
	msg, usage, err := s.processPrompt(ctx, prompt, s.Messages)
	if err != nil {
		s.writeError(err.Error())
		return
	}
	s.Messages = []fantasy.Message{msg}
	// After summarization, the context is just the summary message.
	// Estimate the new context size based on output tokens (the generated summary).
	// This is an approximation, but more accurate than keeping the old (large) value.
	if usage.OutputTokens > 0 {
		s.ContextTokens = usage.OutputTokens
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

func (s *Session) handleModelGetAll() {
	if s.ModelManager == nil {
		s.writeError("model manager not initialized")
		return
	}
	// sendSystemInfo now includes model list and active ID
	s.sendSystemInfo()
}

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

	// Send system info with model list to adaptor via TagSystem
	s.sendSystemInfo()
}

// ============================================================================
// Output Helpers
// ============================================================================

func (s *Session) signalPromptStart(prompt string) {
	s.writeGapped(stream.TagUserText, prompt)
}

func (s *Session) signalCommandStart(cmd string) {
	s.writeGapped(stream.TagUserText, ":"+cmd)
}

func (s *Session) writeError(msg string) {
	s.writeGapped(stream.TagError, msg)
}

func (s *Session) writeNotify(msg string) {
	s.writeGapped(stream.TagNotify, msg)
}

func (s *Session) writeGapped(tag byte, msg string) {
	if s.Output == nil {
		return
	}
	stream.WriteTLV(s.Output, tag, msg)
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	if value := formatToolCall(toolName, input); value != "" {
		stream.WriteTLV(s.Output, stream.TagTool, "[:"+id+":]"+value)
	}
}

func (s *Session) sendSystemInfo() {
	if s.Output == nil {
		return
	}
	var models []ModelInfo
	var activeID string
	if s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
	}
	s.mu.Lock()
	queueCount := len(s.taskQueue)
	inProgress := s.inProgress
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens: s.ContextTokens,
		ContextLimit:  s.ContextLimit,
		TotalTokens:   s.TotalSpent.TotalTokens,
		QueueCount:    queueCount,
		InProgress:    inProgress,
		Models:        models,
		ActiveModelID: activeID,
	}
	data, _ := json.Marshal(info)
	stream.WriteTLV(s.Output, stream.TagSystem, string(data))
	s.Output.Flush()
}

// sendSystemInfoWithModel sends system info including full model config (for model switching)
func (s *Session) sendSystemInfoWithModel(model *ModelConfig) {
	if s.Output == nil {
		return
	}
	var models []ModelInfo
	var activeID string
	if s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
	}
	s.mu.Lock()
	queueCount := len(s.taskQueue)
	inProgress := s.inProgress
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens:     s.ContextTokens,
		ContextLimit:      s.ContextLimit,
		TotalTokens:       s.TotalSpent.TotalTokens,
		QueueCount:        queueCount,
		InProgress:        inProgress,
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: model,
	}
	data, _ := json.Marshal(info)
	stream.WriteTLV(s.Output, stream.TagSystem, string(data))
	s.Output.Flush()
}

// ============================================================================
// Tool Call Formatting
// ============================================================================

func formatToolCall(toolName, input string) string {
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}

	switch toolName {
	case "posix_shell":
		if cmd, ok := fields["command"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, escapeNewlines(cmd))
		}
	case "activate_skill":
		if name, ok := fields["name"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, name)
		}
	case "read_file":
		args := []string{}
		if path, ok := fields["path"].(string); ok {
			args = append(args, path)
		}
		if startLine, ok := fields["start_line"].(string); ok && startLine != "" {
			args = append(args, startLine)
		}
		if endLine, ok := fields["end_line"].(string); ok && endLine != "" {
			args = append(args, endLine)
		}
		if len(args) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(args, ", "))
		}
	case "write_file":
		args := []string{}
		if path, ok := fields["path"].(string); ok {
			args = append(args, path)
		}
		if content, ok := fields["content"].(string); ok {
			truncated := truncateString(content, 50)
			args = append(args, truncated)
		}
		if len(args) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(args, ", "))
		}
	case "edit_file":
		path, _ := fields["path"].(string)
		oldStr, _ := fields["old_string"].(string)
		newStr, _ := fields["new_string"].(string)

		var lines []string
		lines = append(lines, fmt.Sprintf("%s: %s", toolName, path))

		oldLines := strings.Split(oldStr, "\n")
		newLines := strings.Split(newStr, "\n")

		// Pair up old and new lines
		maxLines := max(len(oldLines), len(newLines))

		// Use null byte as separator for raw data - terminal will format with adaptive width
		for i := range maxLines {
			var oldPart, newPart string
			if i < len(oldLines) {
				oldPart = strings.ReplaceAll(oldLines[i], "\n", "\\n")
			}
			if i < len(newLines) {
				newPart = strings.ReplaceAll(newLines[i], "\n", "\\n")
			}
			// Format: \x00old_content\x00new_content
			lines = append(lines, fmt.Sprintf("\x00%s\x00%s", oldPart, newPart))
		}

		return strings.Join(lines, "\n")
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// ============================================================================
// Persistence
// ============================================================================

func LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	return parseSessionMarkdown(data)
}

func LoadLatestSession() (*SessionData, string, error) {
	dir, err := GetSessionsDir()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get sessions directory: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var files []os.FileInfo
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
			if info, err := entry.Info(); err == nil {
				files = append(files, info)
			}
		}
	}

	if len(files) == 0 {
		return nil, "", nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().After(files[j].ModTime())
	})

	latestPath := filepath.Join(dir, files[0].Name())
	data, err := LoadSession(latestPath)
	if err != nil {
		return nil, "", err
	}
	return data, latestPath, nil
}

func (s *Session) saveSessionToFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs := s.Messages
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == fantasy.MessageRoleUser {
		msgs = msgs[:len(msgs)-1]
	}

	data := SessionData{
		BaseURL:       s.BaseURL,
		ModelName:     s.ModelName,
		Messages:      msgs,
		TotalSpent:    s.TotalSpent,
		ContextTokens: s.ContextTokens,
		UpdatedAt:     time.Now(),
	}

	raw, err := formatSessionMarkdown(&data)
	if err != nil {
		return fmt.Errorf("failed to format session data: %w", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}
	return nil
}

func GetSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".alayacore", "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func GenerateSessionFilename() string {
	return time.Now().Format("2006-01-02-150405") + "-1.md"
}

// ============================================================================
// Message Display (for session restore)
// ============================================================================

func (s *Session) displayMessages() {
	if s.Output == nil {
		return
	}
	for _, msg := range s.Messages {
		switch msg.Role {
		case fantasy.MessageRoleUser:
			s.displayUserMessage(msg)
		case fantasy.MessageRoleAssistant:
			s.displayAssistantMessage(msg)
		case fantasy.MessageRoleTool:
			s.displayToolMessage(msg)
		}
	}
}

func (s *Session) displayUserMessage(msg fantasy.Message) {
	var text string
	for _, part := range msg.Content {
		if tp, ok := part.(fantasy.TextPart); ok {
			text += tp.Text
		}
	}
	if text != "" {
		s.signalPromptStart(text)
	}
}

func (s *Session) displayAssistantMessage(msg fantasy.Message) {
	for _, part := range msg.Content {
		switch p := part.(type) {
		case fantasy.TextPart:
			stream.WriteTLV(s.Output, stream.TagAssistantText, p.Text)
			s.Output.Flush()
		case fantasy.ReasoningPart:
			stream.WriteTLV(s.Output, stream.TagReasoning, p.Text)
			s.Output.Flush()
		}
	}
}

func (s *Session) displayToolMessage(msg fantasy.Message) {
	for _, part := range msg.Content {
		if tc, ok := part.(fantasy.ToolCallPart); ok {
			if info := formatToolCall(tc.ToolName, tc.Input); info != "" {
				stream.WriteTLV(s.Output, stream.TagTool, info)
				s.Output.Flush()
			}
		}
	}
}

// ============================================================================
// Path Utilities
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

// ============================================================================
// Markdown Session Format
// ============================================================================

const (
	msgSep = "\x00" // NUL character as message separator
)

// formatSessionMarkdown converts SessionData to markdown format with NUL separators.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder

	// Write YAML frontmatter
	meta := SessionMeta{
		BaseURL:       data.BaseURL,
		ModelName:     data.ModelName,
		TotalTokens:   data.TotalSpent.TotalTokens,
		ContextTokens: data.ContextTokens,
		CreatedAt:     data.CreatedAt,
		UpdatedAt:     data.UpdatedAt,
	}

	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")

	// Write messages - each message is a separate NUL-separated entry
	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.TextPart:
				buf.WriteString(msgSep)
				buf.WriteString("msg:")
				buf.WriteString(string(msg.Role))
				buf.WriteByte('\n')
				buf.WriteString(p.Text)
				if !strings.HasSuffix(p.Text, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ReasoningPart:
				buf.WriteString(msgSep)
				buf.WriteString("msg:reasoning\n")
				buf.WriteString(p.Text)
				if !strings.HasSuffix(p.Text, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ToolCallPart:
				buf.WriteString(msgSep)
				buf.WriteString("tool_call\n")
				fmt.Fprintf(&buf, "id: %s\n", p.ToolCallID)
				fmt.Fprintf(&buf, "name: %s\n", p.ToolName)
				buf.WriteString("input:\n")
				buf.WriteString(indentString(p.Input, "  "))
				if !strings.HasSuffix(p.Input, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ToolResultPart:
				buf.WriteString(msgSep)
				buf.WriteString("tool_result\n")
				fmt.Fprintf(&buf, "id: %s\n", p.ToolCallID)
				buf.WriteString("output:\n")
				outputStr := formatToolResultOutput(p.Output)
				buf.WriteString(indentString(outputStr, "  "))
				if !strings.HasSuffix(outputStr, "\n") {
					buf.WriteByte('\n')
				}
			}
		}
	}

	return []byte(buf.String()), nil
}

// parseSessionMarkdown parses markdown format with NUL separators to SessionData.
func parseSessionMarkdown(data []byte) (*SessionData, error) {
	content := string(data)

	// Split frontmatter and body
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("session file missing YAML frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter := content[4 : endIdx+4]
	body := content[endIdx+8:]

	// Parse metadata
	var meta SessionMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	sd := &SessionData{
		BaseURL:       meta.BaseURL,
		ModelName:     meta.ModelName,
		TotalSpent:    fantasy.Usage{TotalTokens: meta.TotalTokens},
		ContextTokens: meta.ContextTokens,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
	}

	// Parse messages
	if body != "" {
		msgs, err := parseMessages(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
	}

	return sd, nil
}

// parseMessages parses NUL-separated message content.
func parseMessages(body string) ([]fantasy.Message, error) {
	var messages []fantasy.Message
	var currentMsg *fantasy.Message

	// Split by NUL character
	parts := strings.Split(body, msgSep)

	for _, part := range parts {
		if part == "" {
			continue
		}

		// First line is role/type
		lines := strings.SplitN(part, "\n", 2)
		role := lines[0]
		content := ""
		if len(lines) > 1 {
			content = lines[1]
		}

		var part fantasy.MessagePart
		var msgRole fantasy.MessageRole
		newMessage := false

		// Check for "msg:" prefix which indicates a new message
		if strings.HasPrefix(role, "msg:") {
			newMessage = true
			role = strings.TrimPrefix(role, "msg:")
		}

		switch role {
		case string(fantasy.MessageRoleUser):
			msgRole = fantasy.MessageRoleUser
			part = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleAssistant):
			msgRole = fantasy.MessageRoleAssistant
			part = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleTool):
			msgRole = fantasy.MessageRoleTool
			part = parseToolResultContent(content)
		case "reasoning":
			msgRole = fantasy.MessageRoleAssistant
			part = fantasy.ReasoningPart{Text: strings.TrimSuffix(content, "\n")}
		case "tool_call":
			msgRole = fantasy.MessageRoleAssistant
			part = parseToolCallContent(content)
		case "tool_result":
			msgRole = fantasy.MessageRoleTool
			part = parseToolResultContent(content)
		default:
			continue
		}

		// Create new message or append to current
		// Start a new message if: explicitly requested, no current message, or role mismatch
		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if newMessage || currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &fantasy.Message{
				Role:    msgRole,
				Content: []fantasy.MessagePart{part},
			}
		} else {
			currentMsg.Content = append(currentMsg.Content, part)
		}
	}

	if currentMsg != nil {
		messages = append(messages, *currentMsg)
	}

	return messages, nil
}

// parseToolCallContent parses tool_call section content.
func parseToolCallContent(content string) fantasy.ToolCallPart {
	tc := fantasy.ToolCallPart{}
	lines := strings.Split(content, "\n")

	var inputLines []string
	inInput := false

	for _, line := range lines {
		if inInput {
			inputLines = append(inputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tc.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "name: ") {
			tc.ToolName = strings.TrimPrefix(line, "name: ")
		} else if line == "input:" {
			inInput = true
		}
	}

	tc.Input = strings.Join(inputLines, "\n")
	return tc
}

// parseToolResultContent parses tool_result section content.
func parseToolResultContent(content string) fantasy.ToolResultPart {
	tr := fantasy.ToolResultPart{}
	lines := strings.Split(content, "\n")

	var outputLines []string
	inOutput := false

	for _, line := range lines {
		if inOutput {
			outputLines = append(outputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tr.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if line == "output:" {
			inOutput = true
		}
	}

	output := strings.Join(outputLines, "\n")
	tr.Output = fantasy.ToolResultOutputContentText{Text: output}
	return tr
}

// formatToolResultOutput converts ToolResultOutputContent to string.
func formatToolResultOutput(output fantasy.ToolResultOutputContent) string {
	if text, ok := output.(fantasy.ToolResultOutputContentText); ok {
		return text.Text
	}
	if m, ok := output.(fantasy.ToolResultOutputContentMedia); ok {
		data, _ := json.Marshal(m)
		return string(data)
	}
	if e, ok := output.(fantasy.ToolResultOutputContentError); ok {
		return e.Error.Error()
	}
	return fmt.Sprintf("%v", output)
}

// indentString indents each line of a string.
func indentString(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}
