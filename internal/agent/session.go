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

type Task interface{ isTask() }

type UserPrompt string

func (UserPrompt) isTask() {}

type CommandPrompt struct{ Command string }

func (CommandPrompt) isTask() {}

type SystemInfo struct {
	ContextTokens int64 `json:"context"`
	TotalTokens   int64 `json:"total"`
	QueueCount    int   `json:"queue"`
	InProgress    bool  `json:"in_progress"`
}

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

func NewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string) *Session {
	session := &Session{
		BaseURL:     baseURL,
		ModelName:   modelName,
		SessionFile: sessionFile,
		Input:       input,
		Output:      output,
		taskQueue:   make(chan Task, 10),
	}
	session.initAgent(model, baseTools, systemPrompt)
	go session.readFromInput()
	return session
}

func RestoreFromSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionData *SessionData, sessionFile string) *Session {
	session := &Session{
		Messages:      sessionData.Messages,
		BaseURL:       baseURL,
		ModelName:     modelName,
		SessionFile:   sessionFile,
		TotalSpent:    sessionData.TotalSpent,
		ContextTokens: sessionData.ContextTokens,
		Todos:         sessionData.Todos,
		Input:         input,
		Output:        output,
		taskQueue:     make(chan Task, 10),
	}
	session.initAgent(model, baseTools, systemPrompt)
	go session.readFromInput()

	// Display loaded messages if session has any
	if len(session.Messages) > 0 {
		session.displayMessages()
		session.Output.Flush()
	}

	return session
}

func (s *Session) initAgent(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt string) {
	todoReadTool := tools.NewTodoReadTool(s)
	todoWriteTool := tools.NewTodoWriteTool(s)
	allTools := append(baseTools, todoReadTool, todoWriteTool)
	s.Agent = fantasy.NewAgent(model, fantasy.WithTools(allTools...), fantasy.WithSystemPrompt(systemPrompt))
}

func LoadOrNewSession(model fantasy.LanguageModel, baseTools []fantasy.AgentTool, systemPrompt, baseURL, modelName string, input stream.Input, output stream.Output, sessionFile string) (*Session, string) {
	sessionFile = expandPath(sessionFile)
	var sessionData *SessionData
	if sessionFile != "" {
		if data, err := LoadSession(sessionFile); err == nil {
			sessionData = data
		}
	}
	if sessionData != nil {
		return RestoreFromSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, sessionData, sessionFile), sessionFile
	}
	return NewSession(model, baseTools, systemPrompt, baseURL, modelName, input, output, sessionFile), sessionFile
}

func (s *Session) submitTask(task Task) {
	if s.queueTask(task) {
		if s.inProgress {
			s.writeNotify("[Queued] Previous task in progress. Will run after completion.")
			s.sendSystemInfo()
		}
		if !s.inProgress {
			go s.runAsync()
		}
	} else {
		s.writeNotify("[Busy] Cannot queue, try again shortly.")
	}
}

func (s *Session) queueTask(task Task) bool {
	select {
	case s.taskQueue <- task:
		return true
	default:
		return false
	}
}

func (s *Session) getQueuedTask() (Task, bool) {
	select {
	case task, ok := <-s.taskQueue:
		return task, ok
	default:
		return nil, false
	}
}

func (s *Session) runAsync() {
	s.inProgress = true
	s.sendSystemInfo()
	defer func() {
		s.inProgress = false
		s.sendSystemInfo()
	}()
	for {
		task, ok := s.getQueuedTask()
		if !ok {
			break
		}
		s.sendSystemInfo()
		ctx, cancel := context.WithCancel(context.Background())
		s.cancelCurrent = cancel
		switch t := task.(type) {
		case UserPrompt:
			s.signalPromptStart(string(t))
			s.handleUserPrompt(ctx, string(t))
		case CommandPrompt:
			s.signalCommandStart(t.Command)
			s.handleCommandSync(ctx, t.Command)
		}
		if ctx.Err() == context.Canceled {
			s.Messages = append(s.Messages, fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "The user canceled."}},
			})
			s.cancelCurrent = nil
			continue
		}
		s.cancelCurrent = nil
	}
}

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	s.Messages = append(s.Messages, fantasy.NewUserMessage(prompt))
	messagesForAPI := make([]fantasy.Message, len(s.Messages)-1)
	copy(messagesForAPI, s.Messages[:len(s.Messages)-1])
	assistantMsg, usage, err := s.processPrompt(ctx, prompt, messagesForAPI)
	if err != nil {
		s.writeError(err.Error())
		return
	}
	s.trackUsage(usage)
	if assistantMsg.Role != "" {
		s.Messages = append(s.Messages, assistantMsg)
	}
}

func (s *Session) trackUsage(usage fantasy.Usage) {
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	s.TotalSpent.TotalTokens += usage.TotalTokens
	s.TotalSpent.ReasoningTokens += usage.ReasoningTokens
	s.ContextTokens += usage.TotalTokens
	s.sendSystemInfo()
}

func (s *Session) processPrompt(ctx context.Context, prompt string, messages []fantasy.Message) (fantasy.Message, fantasy.Usage, error) {
	streamCall := fantasy.AgentStreamCall{Prompt: prompt}
	if len(messages) > 0 {
		streamCall.Messages = messages
	}
	streamCall.OnTextDelta = func(id, text string) error {
		stream.WriteTLV(s.Output, stream.TagAssistantText, text)
		s.Output.Flush()
		return nil
	}
	streamCall.OnTextEnd = func(id string) error {
		stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		return nil
	}
	streamCall.OnReasoningDelta = func(id, text string) error {
		stream.WriteTLV(s.Output, stream.TagReasoning, text)
		s.Output.Flush()
		return nil
	}
	streamCall.OnReasoningEnd = func(id string, reasoning fantasy.ReasoningContent) error {
		stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		return nil
	}
	streamCall.OnToolCall = func(tc fantasy.ToolCallContent) error {
		s.writeToolCall(tc.ToolName, tc.Input)
		stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		s.Output.Flush()
		return nil
	}
	agentResult, err := s.Agent.Stream(ctx, streamCall)
	if err != nil {
		return fantasy.Message{}, fantasy.Usage{}, err
	}
	s.Output.Flush()
	var assistantMsg fantasy.Message
	if agentResult != nil && len(agentResult.Steps) > 0 {
		lastStep := agentResult.Steps[len(agentResult.Steps)-1]
		for _, msg := range lastStep.Messages {
			if msg.Role == fantasy.MessageRoleAssistant {
				assistantMsg = msg
				break
			}
		}
	}
	return assistantMsg, agentResult.TotalUsage, nil
}

func (s *Session) submitCommand(cmd string) {
	switch cmd {
	case "summarize":
		s.submitTask(CommandPrompt{Command: cmd})
	default:
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
		return
	}
	s.writeError("nothing to cancel")
}

func (s *Session) summarize(ctx context.Context) {
	summarizePrompt := "Please summarize the conversation above in a concise manner. Return ONLY the summary, no introductions or explanations."
	assistantMsg, usage, err := s.processPrompt(ctx, summarizePrompt, s.Messages)
	if err != nil {
		s.writeError(err.Error())
		return
	}
	s.Messages = []fantasy.Message{assistantMsg}
	s.trackUsage(usage)
	s.ContextTokens = usage.OutputTokens
	s.sendSystemInfo()
}

func (s *Session) saveSession(args []string) {
	var filepath string
	if len(args) == 0 {
		if s.SessionFile == "" {
			s.writeError("no session file set and no filename provided")
			return
		}
		filepath = s.SessionFile
	} else if len(args) == 1 {
		filepath = expandPath(args[0])
	} else {
		s.writeError("usage: /save [filename]")
		return
	}
	if err := s.saveSessionToFile(filepath); err != nil {
		s.writeError(fmt.Sprintf("failed to save session: %v", err))
	} else {
		s.writeNotify(fmt.Sprintf("Session saved to %s", filepath))
	}
}

func (s *Session) readFromInput() {
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return
		}
		if tag == stream.TagUserText {
			if len(value) > 0 && value[0] == '/' {
				s.submitCommand(value[1:])
			} else {
				s.submitTask(UserPrompt(value))
			}
		} else {
			s.writeError(fmt.Sprintf("Invalid input tag: %c (only %c is allowed)", tag, stream.TagUserText))
		}
	}
}

func (s *Session) writeGapped(tag byte, msg string) {
	if s.Output != nil {
		stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		stream.WriteTLV(s.Output, tag, msg)
		stream.WriteTLV(s.Output, stream.TagStreamGap, "")
		s.Output.Flush()
	}
}

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

func (s *Session) sendSystemInfo() {
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

func (s *Session) writeToolCall(toolName, input string) {
	if value := formatToolCall(toolName, input); value != "" {
		stream.WriteTLV(s.Output, stream.TagTool, value)
	}
}

func formatToolCall(toolName, input string) string {
	var value string
	switch toolName {
	case "posix_shell":
		if cmd := extractJSONField(input, "command"); cmd != "" {
			value = fmt.Sprintf("%s: %s", toolName, formatCommon(cmd))
		}
	case "activate_skill":
		if name := extractJSONField(input, "name"); name != "" {
			value = fmt.Sprintf("%s: %s", toolName, name)
		}
	case "read_file", "write_file":
		if path := extractJSONField(input, "path"); path != "" {
			value = fmt.Sprintf("%s: %s", toolName, path)
		}
	case "todo_read":
		value = "todo_read: Reading todo list"
	case "todo_write":
		value = "todo_write: Updating todo list"
	}
	return value
}

func extractJSONField(input, field string) string {
	var m map[string]string
	if err := json.Unmarshal([]byte(input), &m); err != nil {
		return ""
	}
	return m[field]
}

func formatCommon(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "\n", "\\n")
	cmd = strings.ReplaceAll(cmd, "\t", "\\t")
	return cmd
}

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

func GetSessionsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sessionsDir := filepath.Join(homeDir, ".coreclaw", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return "", err
	}
	return sessionsDir, nil
}

func GenerateSessionFilename() string {
	return time.Now().Format("2006-01-02-150405-1.json")
}

func LoadLatestSession() (*SessionData, string, error) {
	sessionsDir, err := GetSessionsDir()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get sessions directory: %w", err)
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed to read sessions directory: %w", err)
	}
	if len(entries) == 0 {
		return nil, "", nil
	}
	var sessionFiles []os.FileInfo
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			if info, err := entry.Info(); err == nil {
				sessionFiles = append(sessionFiles, info)
			}
		}
	}
	if len(sessionFiles) == 0 {
		return nil, "", nil
	}
	sort.Slice(sessionFiles, func(i, j int) bool {
		return sessionFiles[i].ModTime().After(sessionFiles[j].ModTime())
	})
	latestPath := filepath.Join(sessionsDir, sessionFiles[0].Name())
	data, err := os.ReadFile(latestPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read session file: %w", err)
	}
	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		return nil, "", fmt.Errorf("failed to parse session data: %w", err)
	}
	return &sessionData, latestPath, nil
}

func LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		return nil, fmt.Errorf("failed to parse session data: %w", err)
	}
	return &sessionData, nil
}

func (s *Session) saveSessionToFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	messagesToSave := s.Messages
	if len(s.Messages) > 0 && s.Messages[len(s.Messages)-1].Role == fantasy.MessageRoleUser {
		messagesToSave = s.Messages[:len(s.Messages)-1]
	}
	sessionData := SessionData{
		BaseURL:       s.BaseURL,
		ModelName:     s.ModelName,
		Messages:      messagesToSave,
		TotalSpent:    s.TotalSpent,
		ContextTokens: s.ContextTokens,
		Todos:         s.Todos,
		UpdatedAt:     time.Now(),
	}
	data, err := json.MarshalIndent(sessionData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}
	return nil
}

func (s *Session) displayMessages() {
	if s.Output == nil {
		return
	}
	for _, msg := range s.Messages {
		switch msg.Role {
		case fantasy.MessageRoleUser:
			var userText string
			for _, part := range msg.Content {
				if textPart, ok := part.(fantasy.TextPart); ok {
					userText += textPart.Text
				}
			}
			if userText != "" {
				s.signalPromptStart(userText)
			}
		case fantasy.MessageRoleAssistant:
			for _, part := range msg.Content {
				if textPart, ok := part.(fantasy.TextPart); ok {
					stream.WriteTLV(s.Output, stream.TagAssistantText, textPart.Text)
					stream.WriteTLV(s.Output, stream.TagStreamGap, "")
					s.Output.Flush()
				} else if reasoningPart, ok := part.(fantasy.ReasoningPart); ok {
					stream.WriteTLV(s.Output, stream.TagReasoning, reasoningPart.Text)
					stream.WriteTLV(s.Output, stream.TagStreamGap, "")
					s.Output.Flush()
				}
			}
		case fantasy.MessageRoleTool:
			for _, part := range msg.Content {
				if toolCall, ok := part.(fantasy.ToolCallPart); ok {
					if toolInfo := formatToolCall(toolCall.ToolName, toolCall.Input); toolInfo != "" {
						stream.WriteTLV(s.Output, stream.TagTool, toolInfo)
						stream.WriteTLV(s.Output, stream.TagStreamGap, "")
						s.Output.Flush()
					}
				}
			}
		}
	}
}

func (s *Session) GetTodos() todo.TodoList { return s.Todos }

func (s *Session) SetTodos(todos todo.TodoList) {
	s.mu.Lock()
	s.Todos = todos
	s.mu.Unlock()
	s.sendTodoList()
}
