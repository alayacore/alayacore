package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/wallacegibbon/coreclaw/internal/stream"
	"github.com/wallacegibbon/coreclaw/internal/todo"
	"github.com/wallacegibbon/coreclaw/internal/tools"
)

// Task represents a unit of work for the session.
type Task interface{ isTask() }

type UserPrompt string

func (UserPrompt) isTask() {}

type CommandPrompt struct{ Command string }

func (CommandPrompt) isTask() {}

// SystemInfo holds session state for clients.
type SystemInfo struct {
	ContextTokens int64 `json:"context"`
	TotalTokens   int64 `json:"total"`
	QueueCount    int   `json:"queue"`
	InProgress    bool  `json:"in_progress"`
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
	Todos         todo.TodoList
	Input         stream.Input
	Output        stream.Output

	taskQueue     chan Task
	inProgress    bool
	cancelCurrent func()
	mu            sync.Mutex
}

// SessionData is the persisted form of a Session.
type SessionData struct {
	BaseURL       string            `json:"base_url"`
	ModelName     string            `json:"model_name"`
	Messages      []fantasy.Message `json:"messages"`
	TotalSpent    fantasy.Usage     `json:"total_spent"`
	ContextTokens int64             `json:"context_tokens"`
	Todos         todo.TodoList     `json:"todos"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
func LoadOrNewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			return RestoreFromSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, data, sessionFile), sessionFile
		}
	}
	return NewSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, sessionFile), sessionFile
}

// NewSession creates a fresh session.
func NewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string) *Session {
	s := &Session{
		BaseURL:     baseURL,
		ModelName:   modelName,
		SessionFile: sessionFile,
		Input:       input,
		Output:      output,
		taskQueue:   make(chan Task, 10),
	}
	s.initAgent(model, baseTools, systemPrompt)
	go s.readFromInput()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, data *SessionData, sessionFile string) *Session {
	s := &Session{
		Messages:      data.Messages,
		BaseURL:       baseURL,
		ModelName:     modelName,
		SessionFile:   sessionFile,
		TotalSpent:    data.TotalSpent,
		ContextTokens: data.ContextTokens,
		Todos:         data.Todos,
		Input:         input,
		Output:        output,
		taskQueue:     make(chan Task, 10),
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
	allTools := append(baseTools,
		tools.NewTodoReadTool(s),
		tools.NewTodoWriteTool(s),
	)
	s.Agent = fantasy.NewAgent(model,
		fantasy.WithTools(allTools...),
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithPrepareStep(s.prepareStep),
	)
}

func (s *Session) prepareStep(ctx context.Context, opts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
	result := fantasy.PrepareStepResult{
		Model:    opts.Model,
		Messages: opts.Messages,
	}
	if reminder := s.generateSystemReminder(); reminder != "" {
		result.Messages = append(opts.Messages, fantasy.NewUserMessage(reminder))
	}
	return ctx, result, nil
}

func (s *Session) generateSystemReminder() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Todos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<system_reminder>\nCurrent todos:\n")
	for _, t := range s.Todos {
		fmt.Fprintf(&sb, "- [%s] %s\n", t.Status, t.Content)
	}
	sb.WriteString("</system_reminder>")
	return sb.String()
}

// ============================================================================
// Input Processing
// ============================================================================

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
		if len(value) > 0 && value[0] == '/' {
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
	if !s.tryQueueTask(task) {
		s.writeNotify("[Busy] Cannot queue, try again shortly.")
		return
	}
	if s.inProgress {
		s.writeNotify("[Queued] Previous task in progress. Will run after completion.")
		s.sendSystemInfo()
	} else {
		go s.runTaskQueue()
	}
}

func (s *Session) tryQueueTask(task Task) bool {
	select {
	case s.taskQueue <- task:
		return true
	default:
		return false
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
		select {
		case task := <-s.taskQueue:
			s.runTask(task)
		default:
			return // Queue empty
		}
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
	s.Messages = append(s.Messages, fantasy.NewUserMessage(prompt))
	history := s.Messages[:len(s.Messages)-1]

	msg, usage, err := s.processPrompt(ctx, prompt, history)
	if err != nil {
		s.trackUsage(usage)
		s.writeError(err.Error())
		return
	}
	s.trackUsage(usage)
	if msg.Role != "" {
		s.Messages = append(s.Messages, msg)
	}
}

func (s *Session) processPrompt(ctx context.Context, prompt string, history []fantasy.Message) (fantasy.Message, fantasy.Usage, error) {
	call := fantasy.AgentStreamCall{Prompt: prompt}
	if len(history) > 0 {
		call.Messages = history
	}

	var currentBlock string // tracks current content block id

	call.OnTextStart = func(id string) error {
		if currentBlock != "" {
			stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		}
		currentBlock = id
		return nil
	}
	call.OnTextDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagAssistantText, text)
		s.Output.Flush()
		return nil
	}
	call.OnReasoningStart = func(id string, _ fantasy.ReasoningContent) error {
		if currentBlock != "" {
			stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		}
		currentBlock = id
		return nil
	}
	call.OnReasoningDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagReasoning, text)
		s.Output.Flush()
		return nil
	}
	call.OnToolCall = func(tc fantasy.ToolCallContent) error {
		if currentBlock != "" {
			stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		}
		currentBlock = tc.ToolCallID
		s.writeToolCall(tc.ToolName, tc.Input)
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
	s.ContextTokens += usage.TotalTokens
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
	default:
		s.writeError(fmt.Sprintf("unknown cmd <%s>", cmd))
	}
}

func (s *Session) cancelTask() {
	if s.inProgress && s.cancelCurrent != nil {
		s.cancelCurrent()
		s.SetTodos(todo.TodoList{})
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
	s.trackUsage(usage)
	s.ContextTokens = usage.OutputTokens
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
		s.writeError("usage: /save [filename]")
		return
	}

	if err := s.saveSessionToFile(path); err != nil {
		s.writeError(fmt.Sprintf("failed to save session: %v", err))
	} else {
		s.writeNotify(fmt.Sprintf("Session saved to %s", path))
	}
}

// ============================================================================
// Output Helpers
// ============================================================================

func (s *Session) signalPromptStart(prompt string) {
	s.writeGapped(stream.TagPromptStart, prompt)
}

func (s *Session) signalCommandStart(cmd string) {
	s.writeGapped(stream.TagPromptStart, "/"+cmd)
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
	stream.WriteTLV(s.Output, stream.TagStreamGap, "")
	stream.WriteTLV(s.Output, tag, msg)
	stream.WriteTLV(s.Output, stream.TagStreamGap, "")
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input string) {
	if value := formatToolCall(toolName, input); value != "" {
		stream.WriteTLV(s.Output, stream.TagTool, value)
	}
}

func (s *Session) sendSystemInfo() {
	if s.Output == nil {
		return
	}
	info := SystemInfo{
		ContextTokens: s.ContextTokens,
		TotalTokens:   s.TotalSpent.TotalTokens,
		QueueCount:    len(s.taskQueue),
		InProgress:    s.inProgress,
	}
	data, _ := json.Marshal(info)
	stream.WriteTLV(s.Output, stream.TagSystem, string(data))
	s.Output.Flush()
}

func (s *Session) sendTodoList() {
	s.mu.Lock()
	data, _ := json.Marshal(s.Todos)
	s.mu.Unlock()
	stream.WriteTLV(s.Output, stream.TagTodo, string(data))
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
	case "todo_read":
		return "todo_read: Reading todo list"
	case "todo_write":
		if todos, ok := fields["todos"].(string); ok && todos != "" {
			truncated := truncateString(todos, 50)
			return fmt.Sprintf("%s: %s", toolName, truncated)
		}
		return "todo_write: Updating todo list"
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
// Todo Access
// ============================================================================

func (s *Session) GetTodos() todo.TodoList { return s.Todos }

func (s *Session) SetTodos(todos todo.TodoList) {
	s.mu.Lock()
	s.Todos = todos
	s.mu.Unlock()
	s.sendTodoList()
}

// ============================================================================
// Persistence
// ============================================================================

func LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	var sd SessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("failed to parse session data: %w", err)
	}
	return &sd, nil
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
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
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
		Todos:         s.Todos,
		UpdatedAt:     time.Now(),
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
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
	dir := filepath.Join(home, ".coreclaw", "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func GenerateSessionFilename() string {
	return time.Now().Format("2006-01-02-150405-1.json")
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
	first := true
	for _, part := range msg.Content {
		switch p := part.(type) {
		case fantasy.TextPart:
			if !first {
				stream.WriteTLV(s.Output, stream.TagStreamGap, "")
			}
			first = false
			stream.WriteTLV(s.Output, stream.TagAssistantText, p.Text)
			s.Output.Flush()
		case fantasy.ReasoningPart:
			if !first {
				stream.WriteTLV(s.Output, stream.TagStreamGap, "")
			}
			first = false
			stream.WriteTLV(s.Output, stream.TagReasoning, p.Text)
			s.Output.Flush()
		}
	}
}

func (s *Session) displayToolMessage(msg fantasy.Message) {
	for _, part := range msg.Content {
		if tc, ok := part.(fantasy.ToolCallPart); ok {
			if info := formatToolCall(tc.ToolName, tc.Input); info != "" {
				stream.WriteTLV(s.Output, stream.TagStreamGap, "")
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
