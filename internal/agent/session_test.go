package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/wallacegibbon/alayacore/internal/stream"
	"github.com/wallacegibbon/alayacore/internal/todo"
)

func TestGetSessionsDir(t *testing.T) {
	sessionsDir, err := GetSessionsDir()
	if err != nil {
		t.Fatalf("GetSessionsDir failed: %v", err)
	}

	if sessionsDir == "" {
		t.Fatal("GetSessionsDir returned empty string")
	}

	// Verify directory exists or can be created
	if stat, err := os.Stat(sessionsDir); err != nil {
		t.Fatalf("Sessions directory stat failed: %v", err)
	} else if !stat.IsDir() {
		t.Fatal("Sessions path is not a directory")
	}
}

func TestGenerateSessionFilename(t *testing.T) {
	filename := GenerateSessionFilename()

	if filename == "" {
		t.Fatal("GenerateSessionFilename returned empty string")
	}

	// Check format ends with -1.md
	if !strings.HasSuffix(filename, "-1.md") {
		t.Fatalf("Invalid filename format: %s (expected to end with -1.md)", filename)
	}

	// Parse to verify it's a valid date (prefix before the -1.md suffix)
	baseFilename := strings.TrimSuffix(filename, "-1.md")
	parsed, err := time.Parse("2006-01-02-150405", baseFilename)
	if err != nil {
		t.Fatalf("Failed to parse filename as date: %v", err)
	}

	// Verify it's recent (within last minute)
	if time.Since(parsed) > time.Minute {
		t.Fatalf("Generated filename is not recent: %s", filename)
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-session.md")

	// Create test session data
	sessionData := &SessionData{
		BaseURL:       "https://api.test.com/v1",
		ModelName:     "test-model",
		Messages:      []fantasy.Message{},
		TotalSpent:    fantasy.Usage{TotalTokens: 100},
		ContextTokens: 50,
	}

	// Create a minimal session for testing
	session := &Session{
		Messages:      sessionData.Messages,
		BaseURL:       sessionData.BaseURL,
		ModelName:     sessionData.ModelName,
		TotalSpent:    sessionData.TotalSpent,
		ContextTokens: sessionData.ContextTokens,
		Input:         &stream.NopInput{},
		Output:        &stream.NopOutput{},
		taskQueue:     make(chan Task, 10),
	}

	// Save session
	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("Session file was not created")
	}

	// Load session
	loadedData, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify data
	if loadedData.BaseURL != sessionData.BaseURL {
		t.Errorf("BaseURL mismatch: got %s, want %s", loadedData.BaseURL, sessionData.BaseURL)
	}

	if loadedData.ModelName != sessionData.ModelName {
		t.Errorf("ModelName mismatch: got %s, want %s", loadedData.ModelName, sessionData.ModelName)
	}

	if loadedData.TotalSpent.TotalTokens != sessionData.TotalSpent.TotalTokens {
		t.Errorf("TotalTokens mismatch: got %d, want %d", loadedData.TotalSpent.TotalTokens, sessionData.TotalSpent.TotalTokens)
	}

	if loadedData.ContextTokens != sessionData.ContextTokens {
		t.Errorf("ContextTokens mismatch: got %d, want %d", loadedData.ContextTokens, sessionData.ContextTokens)
	}
}

func TestLoadLatestSession_EmptyDir(t *testing.T) {
	// Test the function doesn't crash
	// This will use ~/.alayacore/sessions which may or may not exist
	_, _, err := LoadLatestSession()
	// Should not crash, may return nil or error
	if err != nil {
		// This is expected if ~/.alayacore/sessions doesn't exist
		t.Logf("LoadLatestSession returned error (expected for empty state): %v", err)
	}
}

func TestLoadLatestSession_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple session files
	now := time.Now()
	for i := range 3 {
		filename := filepath.Join(tmpDir, now.Add(time.Duration(i)*time.Minute).Format("2006-01-02-1504.md"))
		data := &SessionData{
			BaseURL:   "https://api.test.com",
			ModelName: "test-model",
		}

		// Create a minimal session
		session := &Session{
			BaseURL:   data.BaseURL,
			ModelName: data.ModelName,
			Input:     &stream.NopInput{},
			Output:    &stream.NopOutput{},
			taskQueue: make(chan Task, 10),
		}

		if err := session.saveSessionToFile(filename); err != nil {
			t.Fatalf("Failed to save session %d: %v", i, err)
		}
	}

	// Note: LoadLatestSession uses GetSessionsDir which returns ~/.alayacore/sessions
	// We can't test with our tmpDir without refactoring
	// This test just verifies the mechanism exists
	t.Skip("LoadLatestSession test skipped - uses hardcoded path")
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for model since we're just testing session creation
	var model fantasy.LanguageModel = nil
	baseTools := []fantasy.AgentTool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile := LoadOrNewSession(model, baseTools, systemPrompt, "https://api.test.com", "test-model", &stream.NopInput{}, &stream.NopOutput{}, "", 0)
	if session == nil {
		t.Fatal("LoadOrNewSession returned nil session")
	}
	if sessionFile != "" {
		t.Fatalf("LoadOrNewSession should return empty session file when not specified, got: %s", sessionFile)
	}

	// Verify SessionFile is empty in the session object
	if session.SessionFile != "" {
		t.Errorf("Session SessionFile should be empty when not specified, got: %s", session.SessionFile)
	}

	// Test manual save to a specific file
	testFile := "/tmp/test-session.md"
	if err := session.saveSessionToFile(testFile); err != nil {
		t.Errorf("Failed to save session: %v", err)
	}
	defer os.Remove(testFile) // Clean up test file

	// Verify session is properly initialized
	if session.Agent == nil {
		t.Error("Session agent not set correctly")
	}
	if session.BaseURL != "https://api.test.com" {
		t.Errorf("Session BaseURL not set correctly: got %s", session.BaseURL)
	}
	if session.ModelName != "test-model" {
		t.Errorf("Session ModelName not set correctly: got %s", session.ModelName)
	}
}

func Test_displayMessages(t *testing.T) {
	// Create a mock output to capture displayed messages
	mockOutput := &mockOutput{}

	// Create a session with some messages
	session := &Session{
		Output: mockOutput,
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello world"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hi there!"}},
			},
		},
		taskQueue: make(chan Task, 10),
	}

	// Display messages should not panic
	session.displayMessages()

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("displayMessages did not write any output")
	}
}

// mockOutput is a simple mock for testing output
type mockOutput struct {
	writeCount int
	data       []byte
}

func (m *mockOutput) Write(p []byte) (n int, err error) {
	m.writeCount++
	m.data = append(m.data, p...)
	return len(p), nil
}

func (m *mockOutput) WriteString(s string) (int, error) {
	return m.Write([]byte(s))
}

func (m *mockOutput) Flush() error {
	return nil
}

func TestGenerateSystemReminder(t *testing.T) {
	session := &Session{
		Todos: []todo.TodoItem{
			{Content: "Test task 1", Status: "pending"},
			{Content: "Test task 2", Status: "in_progress"},
			{Content: "Test task 3", Status: "completed"},
		},
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make(chan Task, 10),
	}

	reminder := session.generateSystemReminder()

	// Verify basic structure
	if reminder == "" {
		t.Fatal("generateSystemReminder returned empty string")
	}

	// Check for XML tags
	if !strings.Contains(reminder, "<system_reminder>") || !strings.Contains(reminder, "</system_reminder>") {
		t.Errorf("Reminder missing XML tags: %s", reminder)
	}

	// Check that all todos are included
	if !strings.Contains(reminder, "Test task 1") {
		t.Error("Reminder should include pending todo")
	}
	if !strings.Contains(reminder, "Test task 2") {
		t.Error("Reminder should include in_progress todo")
	}
	if !strings.Contains(reminder, "Test task 3") {
		t.Error("Reminder should include completed todos")
	}

	// Check that context tokens are NOT included
	if strings.Contains(reminder, "context") || strings.Contains(reminder, "token") {
		t.Error("Reminder should not include context usage information")
	}
}

func TestGenerateSystemReminder_Empty(t *testing.T) {
	session := &Session{
		Todos:     []todo.TodoItem{},
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make(chan Task, 10),
	}

	reminder := session.generateSystemReminder()

	// Should return empty string when there are no todos
	if reminder != "" {
		t.Errorf("generateSystemReminder should return empty string, got: %s", reminder)
	}
}

func TestSaveAndLoadSession_WithMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-messages.md")

	// Create session with messages
	session := &Session{
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello, world!"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hi there!"}},
			},
			{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: "Let me help you."},
					fantasy.ReasoningPart{Text: "User needs help..."},
				},
			},
		},
		BaseURL:       "https://api.test.com/v1",
		ModelName:     "test-model",
		TotalSpent:    fantasy.Usage{TotalTokens: 250, InputTokens: 100, OutputTokens: 150},
		ContextTokens: 200,
		Todos: []todo.TodoItem{
			{Content: "Task 1", Status: "pending", ActiveForm: "Doing task 1"},
		},
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make(chan Task, 10),
	}

	// Save
	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify metadata
	if loaded.BaseURL != session.BaseURL {
		t.Errorf("BaseURL mismatch: got %s, want %s", loaded.BaseURL, session.BaseURL)
	}
	if loaded.ModelName != session.ModelName {
		t.Errorf("ModelName mismatch: got %s, want %s", loaded.ModelName, session.ModelName)
	}
	if loaded.TotalSpent.TotalTokens != session.TotalSpent.TotalTokens {
		t.Errorf("TotalTokens mismatch: got %d, want %d", loaded.TotalSpent.TotalTokens, session.TotalSpent.TotalTokens)
	}
	if loaded.ContextTokens != session.ContextTokens {
		t.Errorf("ContextTokens mismatch: got %d, want %d", loaded.ContextTokens, session.ContextTokens)
	}

	// Verify todos
	if len(loaded.Todos) != len(session.Todos) {
		t.Fatalf("Todos count mismatch: got %d, want %d", len(loaded.Todos), len(session.Todos))
	}
	if loaded.Todos[0].Content != session.Todos[0].Content {
		t.Errorf("Todo content mismatch: got %s, want %s", loaded.Todos[0].Content, session.Todos[0].Content)
	}

	// Verify messages - note: reasoning becomes separate message in file format
	// Original: 3 messages (user, assistant, assistant+reasoning)
	// Stored: 4 messages (user, assistant text, assistant text, assistant reasoning)
	if len(loaded.Messages) != 4 {
		t.Fatalf("Message count mismatch: got %d, want 4", len(loaded.Messages))
	}

	// Check first user message
	if loaded.Messages[0].Role != fantasy.MessageRoleUser {
		t.Errorf("First message role mismatch: got %s", loaded.Messages[0].Role)
	}
	if len(loaded.Messages[0].Content) != 1 {
		t.Fatalf("First message content parts: got %d", len(loaded.Messages[0].Content))
	}
	if tp, ok := loaded.Messages[0].Content[0].(fantasy.TextPart); !ok || tp.Text != "Hello, world!" {
		t.Errorf("First message content mismatch: got %v", loaded.Messages[0].Content[0])
	}

	// Check second message (assistant text "Hi there!")
	if loaded.Messages[1].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Second message role mismatch: got %s", loaded.Messages[1].Role)
	}

	// Check third message (assistant text "Let me help you.")
	if loaded.Messages[2].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Third message role mismatch: got %s", loaded.Messages[2].Role)
	}
	if tp, ok := loaded.Messages[2].Content[0].(fantasy.TextPart); !ok || tp.Text != "Let me help you." {
		t.Errorf("Third message content mismatch: got %v", loaded.Messages[2].Content[0])
	}

	// Check fourth message (reasoning)
	if loaded.Messages[3].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Fourth message role mismatch: got %s", loaded.Messages[3].Role)
	}
	if _, ok := loaded.Messages[3].Content[0].(fantasy.ReasoningPart); !ok {
		t.Errorf("Fourth message should be ReasoningPart")
	}
}

func TestMarkdownFormat_HumanReadable(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "readable.md")

	session := &Session{
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello!\nHow are you?"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "I'm doing well, thanks!"}},
			},
		},
		BaseURL:    "https://api.example.com/v1",
		ModelName:  "gpt-4",
		TotalSpent: fantasy.Usage{TotalTokens: 100},
		Input:      &stream.NopInput{},
		Output:     &stream.NopOutput{},
		taskQueue:  make(chan Task, 10),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Read raw file content
	raw, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(raw)

	// Verify YAML frontmatter is human-readable
	if !strings.Contains(content, "base_url:") {
		t.Error("Missing base_url in frontmatter")
	}
	if !strings.Contains(content, "model_name:") {
		t.Error("Missing model_name in frontmatter")
	}

	// Verify message content is preserved (after NUL separators)
	if !strings.Contains(content, "Hello!") {
		t.Error("Missing user message content")
	}
	if !strings.Contains(content, "I'm doing well") {
		t.Error("Missing assistant message content")
	}
}

func TestReasoningOnlyMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "reasoning-only.md")

	// Session with assistant message that only has reasoning (no text)
	session := &Session{
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "What is lisp?"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "The user is asking about Lisp. I should explain it."}},
			},
		},
		BaseURL:   "https://api.example.com/v1",
		ModelName: "gpt-4",
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make(chan Task, 10),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if len(loaded.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(loaded.Messages))
	}

	// Check first message
	if loaded.Messages[0].Role != fantasy.MessageRoleUser {
		t.Errorf("First message should be user, got %s", loaded.Messages[0].Role)
	}

	// Check second message (reasoning only)
	if loaded.Messages[1].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Second message should be assistant, got %s", loaded.Messages[1].Role)
	}
	if len(loaded.Messages[1].Content) != 1 {
		t.Fatalf("Second message should have 1 part, got %d", len(loaded.Messages[1].Content))
	}
	if rp, ok := loaded.Messages[1].Content[0].(fantasy.ReasoningPart); !ok {
		t.Errorf("Second message part should be ReasoningPart")
	} else if !strings.Contains(rp.Text, "asking about Lisp") {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}
}

func TestTextAndReasoningInSameMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "text-and-reasoning.md")

	// Session with assistant message that has both reasoning and text
	// Note: In the file format, these become separate messages
	session := &Session{
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "What is lisp?"}},
			},
			{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.ReasoningPart{Text: "Let me explain Lisp."},
					fantasy.TextPart{Text: "Lisp is a family of programming languages."},
				},
			},
		},
		BaseURL:   "https://api.example.com/v1",
		ModelName: "gpt-4",
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make(chan Task, 10),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Reasoning and text are stored as separate messages in the file format
	if len(loaded.Messages) != 3 {
		t.Fatalf("Expected 3 messages (user, reasoning, assistant text), got %d", len(loaded.Messages))
	}

	// Check first message is user
	if loaded.Messages[0].Role != fantasy.MessageRoleUser {
		t.Errorf("First message should be user, got %s", loaded.Messages[0].Role)
	}

	// Check second message is reasoning (stored as assistant with ReasoningPart)
	if loaded.Messages[1].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Second message should be assistant, got %s", loaded.Messages[1].Role)
	}
	if len(loaded.Messages[1].Content) != 1 {
		t.Fatalf("Second message should have 1 part, got %d", len(loaded.Messages[1].Content))
	}
	if _, ok := loaded.Messages[1].Content[0].(fantasy.ReasoningPart); !ok {
		t.Errorf("Second message part should be ReasoningPart, got %T", loaded.Messages[1].Content[0])
	}

	// Check third message is assistant text
	if loaded.Messages[2].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Third message should be assistant, got %s", loaded.Messages[2].Role)
	}
	if len(loaded.Messages[2].Content) != 1 {
		t.Fatalf("Third message should have 1 part, got %d", len(loaded.Messages[2].Content))
	}
	if _, ok := loaded.Messages[2].Content[0].(fantasy.TextPart); !ok {
		t.Errorf("Third message part should be TextPart, got %T", loaded.Messages[2].Content[0])
	}
}
