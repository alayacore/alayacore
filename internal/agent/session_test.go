package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// MockOutput captures output messages for testing
type MockOutput struct {
	Messages []string
}

func (m *MockOutput) Write(p []byte) (int, error) {
	m.Messages = append(m.Messages, string(p))
	return len(p), nil
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-session.alaya")

	// Create a minimal session for testing
	session := &Session{
		runState: runState{},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	// Save session
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
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

	// Verify data was loaded
	if loadedData == nil {
		t.Error("Loaded data should not be nil")
	}
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for provider since we're just testing session creation
	baseTools := []llm.Tool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile, err := LoadOrNewSession(SessionConfig{
		BaseTools:    baseTools,
		SystemPrompt: systemPrompt,
		Input:        &nopInput{},
		Output:       &nopOutput{},
	})
	if err != nil {
		t.Fatalf("LoadOrNewSession returned error: %v", err)
	}
	if session == nil {
		t.Fatal("LoadOrNewSession returned nil session")
		return
	}

	if sessionFile != "" {
		t.Fatalf("LoadOrNewSession should return empty session file when not specified, got: %s", sessionFile)
	}

	// Verify SessionFile is empty in the session object
	if session.SessionFile != "" {
		t.Errorf("Session SessionFile should be empty when not specified, got: %s", session.SessionFile)
	}

	// Test manual save to a specific file
	testFile := "/tmp/test-session.alaya"
	if err := session.saveContentToFile(testFile, session.Contents); err != nil {
		t.Errorf("Failed to save session: %v", err)
	}
	defer os.Remove(testFile) // Clean up test file

	// Agent is lazily initialized, so it should be nil at startup
	if session.Agent() != nil {
		t.Error("Session agent should be nil at startup (lazy initialization)")
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

func TestSaveAndLoadSession_WithMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-messages.alaya")

	// Create content parts simulating a realistic agent conversation:
	// user → assistant(reasoning+text+toolcall) → tool(result) → assistant(reasoning+text)
	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{Text: "Hello, world!", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.ReasoningPart{Text: "User needs help...", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "Let me help you.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
		&llm.ToolInputPart{
			ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"/tmp/test.txt"}`),
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
		&llm.ToolOutputPart{
			ID: "call_1", Output: []llm.ContentPart{&llm.TextPart{Text: "file contents"}},
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleTool},
		},
		&llm.ReasoningPart{Text: "Now I have the file...", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "Here is the file.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}

	session := &Session{
		runState: runState{
			Contents: contents,
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	// Save
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify content parts are preserved
	if len(loaded.Contents) != len(contents) {
		t.Fatalf("Content count mismatch: got %d, want %d", len(loaded.Contents), len(contents))
	}

	// Check first content part (user)
	if loaded.Contents[0].GetRole() != llm.RoleUser {
		t.Errorf("First content role mismatch: got %s", loaded.Contents[0].GetRole())
	}
	if tp, ok := loaded.Contents[0].(*llm.TextPart); !ok || tp.Text != "Hello, world!" {
		t.Errorf("First content text mismatch: got %v", loaded.Contents[0])
	}

	// Check assistant parts (indices 1-3: reasoning + text + toolcall)
	if loaded.Contents[1].GetRole() != llm.RoleAssistant {
		t.Errorf("Content[1] role mismatch: got %s", loaded.Contents[1].GetRole())
	}
	if _, ok := loaded.Contents[1].(*llm.ReasoningPart); !ok {
		t.Errorf("Content[1] should be ReasoningPart, got %T", loaded.Contents[1])
	}
	if tp, ok := loaded.Contents[2].(*llm.TextPart); !ok || tp.Text != "Let me help you." {
		t.Errorf("Content[2] text mismatch: got %v", loaded.Contents[2])
	}
	if tc, ok := loaded.Contents[3].(*llm.ToolInputPart); !ok || tc.Name != "read_file" {
		t.Errorf("Content[3] should be ToolInputPart, got %T", loaded.Contents[3])
	}

	// Check tool result (index 4)
	if loaded.Contents[4].GetRole() != llm.RoleTool {
		t.Errorf("Content[4] role mismatch: got %s", loaded.Contents[4].GetRole())
	}

	// Check final assistant parts (indices 5-6: reasoning + text)
	if loaded.Contents[5].GetRole() != llm.RoleAssistant {
		t.Errorf("Content[5] role mismatch: got %s", loaded.Contents[5].GetRole())
	}
	if _, ok := loaded.Contents[5].(*llm.ReasoningPart); !ok {
		t.Errorf("Content[5] should be ReasoningPart, got %T", loaded.Contents[5])
	}
	if tp, ok := loaded.Contents[6].(*llm.TextPart); !ok || tp.Text != "Here is the file." {
		t.Errorf("Content[6] text mismatch: got %v", loaded.Contents[6])
	}

	t.Log("PASS: Content parts preserved during save/load")
}

func TestMarkdownFormat_HumanReadable(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "readable.alaya")

	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{Text: "Hello!\nHow are you?", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.TextPart{Text: "I'm doing well, thanks!", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}
	session := &Session{
		runState: runState{
			Contents: contents,
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
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
	sessionPath := filepath.Join(tmpDir, "reasoning-only.alaya")

	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{Text: "What is lisp?", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.ReasoningPart{Text: "The user is asking about Lisp. I should explain it.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}
	session := &Session{
		runState: runState{
			Contents: contents,
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if len(loaded.Contents) != 2 {
		t.Fatalf("Expected 2 content parts, got %d", len(loaded.Contents))
	}

	// Check first part (user)
	if loaded.Contents[0].GetRole() != llm.RoleUser {
		t.Errorf("First part should be user, got %s", loaded.Contents[0].GetRole())
	}

	// Check second part (reasoning only)
	if loaded.Contents[1].GetRole() != llm.RoleAssistant {
		t.Errorf("Second part should be assistant, got %s", loaded.Contents[1].GetRole())
	}
	if rp, ok := loaded.Contents[1].(*llm.ReasoningPart); !ok {
		t.Errorf("Second part should be ReasoningPart, got %T", loaded.Contents[1])
	} else if !strings.Contains(rp.Text, "asking about Lisp") {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}
}

func TestTextAndReasoningInSameMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "text-and-reasoning.alaya")

	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{Text: "What is lisp?", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.ReasoningPart{Text: "Let me explain Lisp.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "Lisp is a family of programming languages.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}
	session := &Session{
		runState: runState{
			Contents: contents,
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// All 3 content parts must be preserved
	if len(loaded.Contents) != 3 {
		t.Fatalf("Expected 3 content parts (user, reasoning, text), got %d", len(loaded.Contents))
	}

	// Check first part is user
	if loaded.Contents[0].GetRole() != llm.RoleUser {
		t.Errorf("First part should be user, got %s", loaded.Contents[0].GetRole())
	}

	// Check second part is assistant reasoning
	if loaded.Contents[1].GetRole() != llm.RoleAssistant {
		t.Errorf("Second part should be assistant, got %s", loaded.Contents[1].GetRole())
	}
	if rp, ok := loaded.Contents[1].(*llm.ReasoningPart); !ok {
		t.Errorf("Second part should be ReasoningPart, got %T", loaded.Contents[1])
	} else if rp.Text != "Let me explain Lisp." {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}

	// Check third part is assistant text
	if loaded.Contents[2].GetRole() != llm.RoleAssistant {
		t.Errorf("Third part should be assistant, got %s", loaded.Contents[2].GetRole())
	}
	if tp, ok := loaded.Contents[2].(*llm.TextPart); !ok {
		t.Errorf("Third part should be TextPart, got %T", loaded.Contents[2])
	} else if tp.Text != "Lisp is a family of programming languages." {
		t.Errorf("Text mismatch: %s", tp.Text)
	}
}

func TestModelSetWhileTaskRunning(t *testing.T) {
	// Create a mock output to capture messages
	output := &MockOutput{}

	// Create a session with a model manager
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	defer sessionCancel()
	session := &Session{
		runState: runState{},
		sessionConfig: sessionConfig{
			modelService: NewModelService(NewModelManager(""), NewRuntimeManager("")),
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: output,
			},
		},
		sharedState: sharedState{
			sessionCtx: sessionCtx,
		},
	}

	// Add a test model to the manager
	testModel := config.ModelConfig{
		ID:           1,
		Name:         "Test Model",
		ProtocolType: "openai",
		BaseURL:      "https://api.test.com/v1",
		APIKey:       "test-key",
		ModelName:    "test-model",
	}
	session.modelService.manager.models = append(session.modelService.manager.models, testModel)

	// Test 1: model_set should work when no task is running.
	// Dispatch through handleInputMsg (the real entry point) so the
	// CmdIdle policy is enforced.
	output.Messages = nil
	session.handleInputMsg(inputMsg{cmd: "model_set", cmdInput: "1", isCmd: true, cmdID: "t1"})

	if hasCOError(output.Messages) {
		t.Error("model_set should succeed when no task is running, but got error")
	}

	// Test 2: model_set should fail when task is running.
	output.Messages = nil
	session.activeTask = &taskHandle{}
	session.handleInputMsg(inputMsg{cmd: "model_set", cmdInput: "1", isCmd: true, cmdID: "t2"})

	if !hasCOError(output.Messages) {
		t.Error("model_set should fail when task is running, but no error was found")
	}

	// Test 3: model_set should work again after task completes.
	output.Messages = nil
	session.activeTask = nil
	session.handleInputMsg(inputMsg{cmd: "model_set", cmdInput: "1", isCmd: true, cmdID: "t3"})

	if hasCOError(output.Messages) {
		t.Error("model_set should succeed after task completes, but got error")
	}
}

// hasCOError reports whether any captured message contains a CO frame
// with is_error=true (the command-result error marker).
func hasCOError(msgs []string) bool {
	for _, msg := range msgs {
		if strings.Contains(msg, `"is_error":true`) {
			return true
		}
	}
	return false
}

func TestDisplayMessagesWithToolCalls(t *testing.T) {
	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{Text: "List files", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.TextPart{Text: "I'll list files for you.", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
		&llm.ToolInputPart{
			ID: "call_123", Name: "execute_command", Input: json.RawMessage(`{"command": "ls -la"}`),
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
		&llm.ToolOutputPart{
			ID: "call_123", Output: []llm.ContentPart{&llm.TextPart{Text: "file1.txt\nfile2.txt"}},
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleTool},
		},
		&llm.TextPart{Text: "Found 2 files!", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}

	// Create session data with flat ContentParts
	sessionData := &SessionData{
		Contents: contents,
		SessionMeta: SessionMeta{
			MessageVersion: MessageVersion,
			UpdatedAt:      time.Now(),
		},
	}

	// Generate RawTLV using the actual formatting function
	raw, err := formatSessionMarkdown(sessionData)
	if err != nil {
		t.Fatalf("Failed to format session: %v", err)
	}

	// Parse back
	loadedData, err := LoadSessionFromBytes(raw)
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}

	// Verify content was populated
	if len(loadedData.Contents) == 0 {
		t.Error("Content should be populated after loading")
	}

	// Create mock output and send content parts
	mockOutput := &mockOutput{}
	for _, part := range loadedData.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			t.Fatalf("Failed to serialize part: %v", err)
		}
		_ = tlv.WriteTLV(mockOutput, tag, tlv.WrapID(strconv.FormatUint(part.GetHistoryID(), 10), content))
	}

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("Content did not write any output")
	}

	// Parse the output data to check what TLV was sent
	outputStr := string(mockOutput.data)

	// User message should be in TLV
	if !strings.Contains(outputStr, "List files") {
		t.Error("User message should be in TLV")
	}

	// Assistant messages should be in TLV
	if !strings.Contains(outputStr, "I'll list files for you") {
		t.Error("First assistant message should be in TLV")
	}
	if !strings.Contains(outputStr, "Found 2 files!") {
		t.Error("Second assistant message should be in TLV")
	}

	// Tool call should be in TLV as FC tag
	if !strings.Contains(outputStr, "execute_command") {
		t.Error("Tool call should be in TLV")
	}
}

// LoadSessionFromBytes loads a session from raw bytes (for testing)
func LoadSessionFromBytes(data []byte) (*SessionData, error) {
	sd, err := parseSessionData(data)
	if err != nil {
		return nil, err
	}
	return sd, nil
}

func TestCleanIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		contents []llm.ContentPart
		wantLen  int // expected number of content parts after cleaning
	}{
		{
			name: "complete tool call cycle",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "Hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.ToolInputPart{ID: "call-1", Name: "test_tool", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{&llm.TextPart{Text: "result"}}, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}},
				&llm.TextPart{Text: "Done", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "incomplete tool call - no result",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "Hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.ToolInputPart{ID: "call-1", Name: "test_tool", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				// No tool result
			},
			wantLen: 1, // user kept, tool call removed
		},
		{
			name: "incomplete tool call - assistant has text and tool call",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "Hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.TextPart{Text: "Let me help", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.ToolInputPart{ID: "call-1", Name: "test_tool", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				// Tool call has no result
			},
			wantLen: 2, // user kept, text part kept, tool call removed
		},
		{
			name: "trailing user message preserved",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "First", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.TextPart{Text: "Response", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.TextPart{Text: "Second (no response)", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
			},
			wantLen: 3, // all kept, including trailing user message
		},
		{
			name: "complete tool call - multiple tools",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "Hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.ToolInputPart{ID: "call-1", Name: "tool_a", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.ToolInputPart{ID: "call-2", Name: "tool_b", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{&llm.TextPart{Text: "result_a"}}, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}},
				&llm.ToolOutputPart{ID: "call-2", Output: []llm.ContentPart{&llm.TextPart{Text: "result_b"}}, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}},
				&llm.TextPart{Text: "Done", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
			},
			wantLen: 6, // all kept
		},
		{
			name: "complete tool call - no trailing text",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "Hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.ToolInputPart{ID: "call-1", Name: "test_tool", Input: json.RawMessage("{}"), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
				&llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{&llm.TextPart{Text: "result"}}, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}},
				// No trailing assistant text
			},
			wantLen: 3, // ToolInput kept because ToolOutput follows
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanIncompleteToolInputs(tt.contents)
			if len(got) != tt.wantLen {
				t.Errorf("cleanIncompleteToolInputs() returned %d parts, want %d", len(got), tt.wantLen)
				for i, part := range got {
					t.Logf("  part[%d]: role=%s, type=%T", i, part.GetRole(), part)
				}
			}
		})
	}
}

// TestTLVFormatRecursionProtection tests that the TLV format correctly handles
// session file content embedded in tool results (the recursion problem).
func TestTLVFormatRecursionProtection(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "recursion-test.alaya")

	var id uint64
	nextID := func() uint64 { id++; return id }

	// Create a session that contains what looks like session markers in tool output
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "Read the session file", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser}},
		&llm.ToolInputPart{
			ID: "call1", Name: "read_file", Input: json.RawMessage(`{"path": "old-session.alaya"}`),
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
		&llm.ToolOutputPart{
			ID:              "call1",
			Output:          []llm.ContentPart{&llm.TextPart{Text: "---\nbase_url: https://api.test.com\n---\n\x00msg:user\nFake user message\n\x00msg:assistant\nFake assistant\n"}},
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleTool},
		},
		&llm.TextPart{Text: "Here's the file content...", ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant}},
	}
	session := &Session{
		runState: runState{
			Contents: contents,
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: &nopOutput{},
			},
		},
	}

	// Save
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify we still have 4 content parts (not more due to false parsing)
	if len(loaded.Contents) != 4 {
		t.Errorf("expected 4 content parts, got %d - recursion protection failed!", len(loaded.Contents))
		for i, part := range loaded.Contents {
			t.Logf("part[%d]: role=%s, type=%T", i, part.GetRole(), part)
		}
		return
	}

	// Verify the tool result still contains the fake markers
	tr, ok := loaded.Contents[2].(*llm.ToolOutputPart)
	if !ok {
		t.Fatalf("expected ToolOutputPart at index 2, got %T", loaded.Contents[2])
	}
	if len(tr.Output) == 0 {
		t.Fatalf("expected non-empty tool result content")
	}
	textPart, ok := tr.Output[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", tr.Output[0])
	}
	// The output should contain the fake markers (not stripped or misparsed)
	if !strings.Contains(textPart.Text, "msg:user") {
		t.Errorf("tool result should contain 'msg:user', got: %q", textPart.Text)
	}
	if !strings.Contains(textPart.Text, "Fake user message") {
		t.Errorf("tool result should contain 'Fake user message', got: %q", textPart.Text)
	}
}

// TestLoadSessionMissingReasoningLevel verifies that when a session file's
// frontmatter does not contain a reasoning_level key, the reasoning_level defaults
// to 1 (normal) rather than 0 (off).
func TestLoadSessionMissingReasoningLevel(t *testing.T) {
	// Frontmatter without reasoning_level, mimicking an older session file.
	// Must include message_version: 11 for the version check to pass.
	raw := []byte("---\nmessage_version: 11\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	data, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data.ReasoningLevel != 1 {
		t.Errorf("expected ReasoningLevel=1 when reasoning_level is absent from frontmatter, got %d", data.ReasoningLevel)
	}

	// Also verify that an explicit reasoning_level: 0 is preserved.
	raw2 := []byte("---\nmessage_version: 11\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\nreasoning_level: 0\n---\n")

	data2, err := parseSessionData(raw2)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data2.ReasoningLevel != 0 {
		t.Errorf("expected ReasoningLevel=0 when reasoning_level is explicitly 0, got %d", data2.ReasoningLevel)
	}
}

// TestLoadSessionInvalidReasoningLevel verifies that out-of-range reasoning_level
// values in the session file are reset to the default (1) rather than being
// passed through to the provider.
func TestLoadSessionInvalidReasoningLevel(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"negative", "reasoning_level: -1", 1},
		{"too high", "reasoning_level: 3", 1},
		{"large positive", "reasoning_level: 999", 1},
		{"large negative", "reasoning_level: -100", 1},
		{"valid zero", "reasoning_level: 0", 0},
		{"valid one", "reasoning_level: 1", 1},
		{"valid two", "reasoning_level: 2", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte("---\nmessage_version: 11\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n" + tt.value + "\n---\n")

			data, err := parseSessionData(raw)
			if err != nil {
				t.Fatalf("parseSessionData failed: %v", err)
			}

			if data.ReasoningLevel != tt.want {
				t.Errorf("reasoning_level=%s: expected %d, got %d", tt.value, tt.want, data.ReasoningLevel)
			}
		})
	}
}

// TestLoadSessionVersionMismatch verifies that a session file with a missing or
// non-matching version is rejected with ErrSessionVersionMismatch.
func TestLoadSessionVersionMismatch(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{
			name:    "missing version",
			raw:     "---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n",
			wantErr: ErrSessionVersionMismatch,
		},
		{
			name:    "version zero",
			raw:     "---\nmessage_version: 0\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n",
			wantErr: ErrSessionVersionMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSessionData([]byte(tt.raw))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestLoadSessionVersionValid verifies that a session file with a valid version
// loads successfully.
func TestLoadSessionVersionValid(t *testing.T) {
	raw := []byte("---\nmessage_version: 11\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	data, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data.MessageVersion != MessageVersion {
		t.Errorf("expected MessageVersion=%d, got %d", MessageVersion, data.MessageVersion)
	}
}

// TestLoadOrNewSessionVersionMismatch verifies that LoadOrNewSession returns an
// error when the session file has a non-matching version.
func TestLoadOrNewSessionVersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "old-session.alaya")

	// Write a session file with missing version
	if err := os.WriteFile(sessionPath, []byte("---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n"), 0600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	_, _, err := LoadOrNewSession(SessionConfig{
		SessionFile: sessionPath,
		Input:       &nopInput{},
		Output:      &nopOutput{},
	})
	if err == nil {
		t.Fatal("expected error for old session file, got nil")
	}
	if !errors.Is(err, ErrSessionVersionMismatch) {
		t.Errorf("expected ErrSessionVersionMismatch, got %v", err)
	}
}
