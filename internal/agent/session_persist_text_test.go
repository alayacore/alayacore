package agent

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestSessionSavePreservesTextWithToolCalls verifies that text content parts
// are preserved when saving/loading sessions with tool calls.
func TestSessionSavePreservesTextWithToolCalls(t *testing.T) {
	// Build flat ContentParts with roles
	var id uint64
	nextID := func() uint64 { id++; return id }

	contents := []llm.ContentPart{
		&llm.TextPart{
			Text:            "What's the weather?",
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleUser},
		},
		&llm.TextPart{
			Text:            "Let me check that for you.",
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
		&llm.ToolInputPart{
			ID:              "call_123",
			Name:            "get_weather",
			Input:           []byte(`{"location":"SF"}`),
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
		&llm.ToolOutputPart{
			ID:              "call_123",
			Output:          []llm.ContentPart{&llm.TextPart{Text: "Sunny, 72F"}},
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleTool},
		},
		&llm.TextPart{
			Text:            "The weather in SF is sunny and 72F.",
			ContentPartMeta: llm.ContentPartMeta{HistoryID: nextID(), Role: llm.RoleAssistant},
		},
	}

	data := &sessionData{
		sessionMeta: sessionMeta{
			MessageVersion: messageVersion,
		},
		Contents: contents,
	}

	// Format to markdown (TLV format)
	raw, err := formatSessionMarkdown(data)
	if err != nil {
		t.Fatalf("Failed to format session: %v", err)
	}

	t.Logf("Serialized session size: %d bytes", len(raw))

	// Parse back
	loaded, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("Failed to parse session: %v", err)
	}

	// Verify content parts are preserved
	if len(loaded.Contents) != len(contents) {
		t.Fatalf("Content part count mismatch: got %d, want %d", len(loaded.Contents), len(contents))
	}

	// Check first assistant part (index 1) - text
	if tp, ok := loaded.Contents[1].(*llm.TextPart); ok {
		if tp.Text != "Let me check that for you." {
			t.Errorf("Assistant text mismatch: %q", tp.Text)
		}
	} else {
		t.Error("Content[1] is not a TextPart")
	}

	// Check assistant tool call (index 2)
	if tc, ok := loaded.Contents[2].(*llm.ToolInputPart); ok {
		if tc.Name != "get_weather" {
			t.Errorf("Tool name mismatch: %s", tc.Name)
		}
	} else {
		t.Error("Content[2] is not a ToolInputPart")
	}

	// Check tool result (index 3)
	if tr, ok := loaded.Contents[3].(*llm.ToolOutputPart); ok {
		if tr.ID != "call_123" {
			t.Errorf("Tool result ID mismatch: %s", tr.ID)
		}
	} else {
		t.Error("Content[3] is not a ToolOutputPart")
	}

	// Check final assistant text (index 4)
	if tp, ok := loaded.Contents[4].(*llm.TextPart); ok {
		if tp.Text != "The weather in SF is sunny and 72F." {
			t.Errorf("Final assistant text mismatch: %q", tp.Text)
		}
	} else {
		t.Error("Content[4] is not a TextPart")
	}

	t.Log("PASS: Content parts preserved during save/load with tool calls")
}
