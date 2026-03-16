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
		Messages: []fantasy.Message{},
	}

	// Create a minimal session for testing
	session := &Session{
		Messages:  sessionData.Messages,
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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
	if len(loadedData.Messages) != len(sessionData.Messages) {
		t.Errorf("Messages mismatch: got %d, want %d", len(loadedData.Messages), len(sessionData.Messages))
	}
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for model since we're just testing session creation
	var model fantasy.LanguageModel
	baseTools := []fantasy.AgentTool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile := LoadOrNewSession(model, baseTools, systemPrompt, &stream.NopInput{}, &stream.NopOutput{}, "", 0, "", "", false, "")
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

	// Agent is lazily initialized, so it should be nil at startup
	if session.Agent != nil {
		t.Error("Session agent should be nil at startup (lazy initialization)")
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
		taskQueue: make([]QueueItem, 0),
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
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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

	// Verify messages - TLV format preserves message structure
	// Original: 3 messages (user, assistant, assistant with text+reasoning)
	// Stored and loaded: 3 messages (same structure)
	if len(loaded.Messages) != 3 {
		t.Fatalf("Message count mismatch: got %d, want 3", len(loaded.Messages))
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

	// Check third message (assistant with text + reasoning)
	if loaded.Messages[2].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Third message role mismatch: got %s", loaded.Messages[2].Role)
	}
	if len(loaded.Messages[2].Content) != 2 {
		t.Fatalf("Third message should have 2 parts (text + reasoning), got %d", len(loaded.Messages[2].Content))
	}
	if tp, ok := loaded.Messages[2].Content[0].(fantasy.TextPart); !ok || tp.Text != "Let me help you." {
		t.Errorf("Third message text part mismatch: got %v", loaded.Messages[2].Content[0])
	}
	if _, ok := loaded.Messages[2].Content[1].(fantasy.ReasoningPart); !ok {
		t.Errorf("Third message second part should be ReasoningPart")
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
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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
	if !strings.Contains(content, "updated_at:") {
		t.Error("Missing updated_at in frontmatter")
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
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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
		Input:        &stream.NopInput{},
		Output:       output,
		taskQueue:    make([]QueueItem, 0),
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
		if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed when no task is running, but got error")
	}

	// Test 2: model_set should also work when task is running (no restriction)
	output.Messages = nil // Clear previous messages
	session.inProgress = true
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed even when task is running, but got error")
	}

	// Test 3: model_set should work again after task completes
	output.Messages = nil // Clear previous messages
	session.inProgress = false
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed after task completes, but got error")
	}
}

func TestDisplayMessagesWithToolCalls(t *testing.T) {
	// Create a mock output to capture displayed messages
	mockOutput := &mockOutput{}

	// Create a session with tool calls (as they would be loaded from file)
	session := &Session{
		Output: mockOutput,
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "List files"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "I'll list files for you."}},
			},
			{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{
						ToolCallID: "call_123",
						ToolName:   "posix_shell",
						Input:      `{"command": "ls -la"}`,
					},
				},
			},
			{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: "call_123",
						Output:     fantasy.ToolResultOutputContentText{Text: "file1.txt\nfile2.txt"},
					},
				},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Found 2 files!"}},
			},
		},
		taskQueue: make([]QueueItem, 0),
	}

	// Display messages
	session.displayMessages()

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("displayMessages did not write any output")
	}

	// Parse the output data to check what was displayed
	outputStr := string(mockOutput.data)

	// User message should be displayed
	if !strings.Contains(outputStr, "List files") {
		t.Error("User message should be displayed")
	}

	// Assistant messages should be displayed
	if !strings.Contains(outputStr, "I'll list files for you") {
		t.Error("First assistant message should be displayed")
	}
	if !strings.Contains(outputStr, "Found 2 files!") {
		t.Error("Second assistant message should be displayed")
	}

	// Tool call should be displayed
	if !strings.Contains(outputStr, "posix_shell:") {
		t.Error("Tool call should be displayed")
	}

	// Tool result should NOT be displayed (it's in message history but not shown to user)
	// This is the key behavior - tool results are context-only
	// We can verify this by checking that the actual file names are NOT in the displayed output
	if strings.Contains(outputStr, "file1.txt") || strings.Contains(outputStr, "file2.txt") {
		t.Error("Tool result should NOT be displayed to user, it should only exist in message history")
	}
}

func TestCleanIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		messages []fantasy.Message
		wantLen  int // expected number of messages after cleaning
	}{
		{
			name: "complete tool call cycle",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "test_tool", Input: "{}"},
				}},
				{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{ToolCallID: "call-1", Output: fantasy.ToolResultOutputContentText{Text: "result"}},
				}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "complete tool call - Anthropic style (tool result in user message)",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "test_tool", Input: "{}"},
				}},
				// Anthropic puts tool result in user message
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{ToolCallID: "call-1", Output: fantasy.ToolResultOutputContentText{Text: "result"}},
				}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "incomplete tool call - no result",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "test_tool", Input: "{}"},
				}},
				// No tool result message - this happens when API errors mid-cycle
			},
			wantLen: 1, // user kept, assistant removed (empty after filtering tool call)
		},
		{
			name: "incomplete tool call - assistant has text and tool call",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: "Let me help"},
					fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "test_tool", Input: "{}"},
				}},
				// Tool call has no result
			},
			wantLen: 2, // user kept, assistant kept with only text part
		},
		{
			name: "incomplete tool call - Anthropic style (user message with tool result is missing)",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{ToolCallID: "call-1", ToolName: "test_tool", Input: "{}"},
				}},
				// No user message with tool result - incomplete
			},
			wantLen: 1, // only user message kept, assistant removed
		},
		{
			name: "trailing user message preserved",
			messages: []fantasy.Message{
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "First"}}},
				{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Response"}}},
				{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Second (no response)"}}},
			},
			wantLen: 3, // all kept, including trailing user message
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanIncompleteToolCalls(tt.messages)
			if len(got) != tt.wantLen {
				t.Errorf("cleanIncompleteToolCalls() returned %d messages, want %d", len(got), tt.wantLen)
				for i, msg := range got {
					t.Logf("  msg[%d]: role=%s, parts=%d", i, msg.Role, len(msg.Content))
				}
			}
		})
	}
}

// TestTLVFormatRecursionProtection tests that the TLV format correctly handles
// session file content embedded in tool results (the recursion problem).
func TestTLVFormatRecursionProtection(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "recursion-test.md")

	// Create a session that contains what looks like session markers in tool output
	session := &Session{
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Read the session file"}},
			},
			{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.ToolCallPart{
						ToolCallID: "call1",
						ToolName:   "read_file",
						Input:      `{"path": "old-session.md"}`,
					},
				},
			},
			{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: "call1",
						// This output contains text that looks like old session format markers!
						Output: fantasy.ToolResultOutputContentText{Text: "---\nbase_url: https://api.test.com\n---\n\x00msg:user\nFake user message\n\x00msg:assistant\nFake assistant\n"},
					},
				},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Here's the file content..."}},
			},
		},
		Input:     &stream.NopInput{},
		Output:    &stream.NopOutput{},
		taskQueue: make([]QueueItem, 0),
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

	// Verify we still have 4 messages (not more due to false parsing)
	if len(loaded.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d - recursion protection failed!", len(loaded.Messages))
		for i, msg := range loaded.Messages {
			t.Logf("msg[%d]: role=%s, parts=%d", i, msg.Role, len(msg.Content))
		}
		return
	}

	// Verify the tool result still contains the fake markers
	tr, ok := loaded.Messages[2].Content[0].(fantasy.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", loaded.Messages[2].Content[0])
	}
	output, ok := tr.Output.(fantasy.ToolResultOutputContentText)
	if !ok {
		t.Fatalf("expected ToolResultOutputContentText, got %T", tr.Output)
	}
	// The output should contain the fake markers (not stripped or misparsed)
	if !strings.Contains(output.Text, "msg:user") {
		t.Errorf("tool result should contain 'msg:user', got: %q", output.Text)
	}
	if !strings.Contains(output.Text, "Fake user message") {
		t.Errorf("tool result should contain 'Fake user message', got: %q", output.Text)
	}
}
