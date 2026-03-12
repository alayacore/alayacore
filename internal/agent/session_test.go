package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
)

// MockOutput captures output messages for testing
type MockOutput struct {
	Messages []string
}

func (m *MockOutput) Write(p []byte) (int, error) {
	m.Messages = append(m.Messages, string(p))
	return len(p), nil
}

func (m *MockOutput) WriteString(s string) (int, error) {
	m.Messages = append(m.Messages, s)
	return len(s), nil
}

func (m *MockOutput) Flush() error {
	return nil
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
		taskQueue:     make([]Task, 0),
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

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for model since we're just testing session creation
	var model fantasy.LanguageModel = nil
	baseTools := []fantasy.AgentTool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile := LoadOrNewSession(model, baseTools, systemPrompt, "https://api.test.com", "test-model", &stream.NopInput{}, &stream.NopOutput{}, "", 0, "")
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
		taskQueue: make([]Task, 0),
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
		Input:         &stream.NopInput{},
		Output:        &stream.NopOutput{},
		taskQueue:     make([]Task, 0),
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
		taskQueue:  make([]Task, 0),
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
		taskQueue: make([]Task, 0),
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
		taskQueue: make([]Task, 0),
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

func TestModelSetWhileTaskRunning(t *testing.T) {
	// Create a mock output to capture messages
	output := &MockOutput{}

	// Create a session with a model manager
	session := &Session{
		Messages:     []fantasy.Message{},
		BaseURL:      "https://api.test.com/v1",
		ModelName:    "test-model",
		Input:        &stream.NopInput{},
		Output:       output,
		taskQueue:    make([]Task, 0),
		ModelManager: NewModelManager(""),
	}

	// Add a test model to the manager
	testModel := ModelConfig{
		ID:           "test-model-1",
		Name:         "Test Model",
		ProtocolType: "openai",
		BaseURL:      "https://api.test.com/v1",
		APIKey:       "test-key",
		ModelName:    "test-model",
	}
	session.ModelManager.models = append(session.ModelManager.models, testModel)

	// Test 1: model_set should work when no task is running
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError := false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "cannot switch model while task is running") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed when no task is running, but got error")
	}

	// Test 2: model_set should fail when task is running
	output.Messages = nil // Clear previous messages
	session.inProgress = true
	session.handleModelSet([]string{"test-model-1"})

	// Check that the error message was written
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "cannot switch model while task is running") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("model_set should fail when task is running, but no error was returned")
	}

	// Test 3: model_set should work again after task completes
	output.Messages = nil // Clear previous messages
	session.inProgress = false
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "cannot switch model while task is running") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed after task completes, but got error")
	}
}
