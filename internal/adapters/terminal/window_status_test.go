package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestHandleToolInputEvent(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Send a "call" type event (creates the window with Name set = start frame)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool123",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: git status"),
	}, 0)

	// Verify window was created
	if wb.WindowCount() != 1 {
		t.Fatalf("Expected 1 window, got %d", wb.WindowCount())
	}

	// Status defaults to pending (inferred from window creation)
	if wb.WindowAt(0).RawStatus() != ToolStatusPending {
		t.Errorf("Expected ToolStatusPending, got %v", wb.WindowAt(0).RawStatus())
	}

	// Send a result
	wb.HandleToolOutput("tool123", "output text", false, 0)

	// Check status was updated
	if wb.WindowAt(0).RawStatus() != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.WindowAt(0).RawStatus())
	}

	// Send a result with error
	wb.HandleToolOutput("tool123", "error output", true, 0)

	// Check status was updated
	if wb.WindowAt(0).RawStatus() != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.WindowAt(0).RawStatus())
	}

	// Try to update non-existent window (should not crash)
	wb.HandleToolOutput("nonexistent", "output", false, 0)
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window (Name set = start frame)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool123",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: git status"),
	}, 0)

	// Test rendering with pending status (default on creation)
	w := wb.WindowAt(0)
	content := wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed filled dot (•) for pending status
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Send result with success
	wb.HandleToolOutput("tool123", "output", false, 0)

	// Test rendering with success status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Send result with error
	wb.HandleToolOutput("tool123", "error output", true, 0)

	// Test rendering with error status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestOutputWriterToolCallStartThenFull(t *testing.T) {
	// End-to-end test: write TagAssistantF TLVs through the actual
	// outputWriter pipeline (Write → processBuffer → writeColored → HandleToolInputEvent).
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	makeStartFD := func(id, name string) []byte {
		fd, _ := json.Marshal(protocol.ToolInputData{
			ID:   id,
			Name: name,
		})
		return encodeTestTLV(tlv.TagAssistantF, tlv.WrapID("1", string(fd)))
	}

	makeInputFD := func(id, input string) []byte {
		fd, _ := json.Marshal(protocol.ToolInputData{
			ID:    id,
			Input: json.RawMessage(input),
		})
		return encodeTestTLV(tlv.TagAssistantF, tlv.WrapID("1", string(fd)))
	}

	// 1. Simulate ToolCallStart: Name set, no input yet
	_, err := out.Write(makeStartFD("call-abc", "write_file"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("Expected 1 window after start event, got %d", wb.WindowCount())
	}
	w := wb.WindowAt(0)
	if w.RawToolName() != "write_file" {
		t.Errorf("Expected tool name 'write_file', got %q", w.RawToolName())
	}

	// 2. Simulate ToolCallComplete: Name nil with full JSON input
	_, err = out.Write(makeInputFD("call-abc", `{"path":"/tmp/f.txt","content":"hello world"}`))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if wb.WindowCount() != 1 {
		t.Fatalf("Expected still 1 window, got %d", wb.WindowCount())
	}
	w = wb.WindowAt(0)
	if ti := w.ToolInfo(); ti == nil || ti.Input != "write_file: /tmp/f.txt\nhello world" {
		t.Errorf("Expected full input, got %v", w.ToolInfo())
	}
}

func TestToolRendererDeltaTruncation(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name      string
		innerW    int
		toolName  string
		delta     string
		wantLines int
		wantTrim  bool // expect "…" truncation
	}{
		{
			name:      "short delta fits",
			innerW:    80,
			toolName:  "write_file",
			delta:     `{"path":"/tmp/foo"}`,
			wantLines: 3, // top border + content + bottom border
			wantTrim:  false,
		},
		{
			name:      "long delta truncated",
			innerW:    30,
			toolName:  "write_file",
			delta:     `{"path":"/home/user/very/long/path/that/exceeds/width/for/preview"}`,
			wantLines: 3, // still single content line
			wantTrim:  true,
		},
		{
			name:      "narrow window",
			innerW:    10,
			toolName:  "cat",
			delta:     `{"path":"/tmp/foo"}`,
			wantLines: 3,
			wantTrim:  false, // no room for delta after "• cat: " (7 cols) in innerWidth=6
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &toolRenderer{
				name:        tt.toolName,
				deltaBuffer: tt.delta,
				status:      ToolStatusPending,
			}
			result, lineCount := tr.BuildInner(tt.innerW, false, styles)

			if lineCount != tt.wantLines {
				t.Errorf("Expected %d lines, got %d", tt.wantLines, lineCount)
			}

			hasTrim := strings.Contains(result, "…")
			if hasTrim != tt.wantTrim {
				t.Errorf("Truncation mismatch: wantTrim=%v, got contains-ellipsis=%v\nResult: %q", tt.wantTrim, hasTrim, result)
			}
		})
	}
}
