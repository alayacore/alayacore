package agent

import (
	"encoding/binary"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

func TestWriteToolResult(t *testing.T) {
	// Create a mock output to capture TLV messages
	output := &mockOutput{}
	session := &Session{
		Output: output,
	}

	// Test success case
	session.writeToolResult("tool123", "success")

	// Parse the written data to extract TLV
	tag, value := parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionState {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionState, tag)
	}

	expectedValue := "[:tool123:]success"
	if value != expectedValue {
		t.Errorf("Expected value %s, got %s", expectedValue, value)
	}

	// Test error case
	output.data = nil
	session.writeToolResult("tool456", "error")

	tag, value = parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionState {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionState, tag)
	}

	expectedValue = "[:tool456:]error"
	if value != expectedValue {
		t.Errorf("Expected value %s, got %s", expectedValue, value)
	}

	// Test pending case
	output.data = nil
	session.writeToolResult("tool789", "pending")

	tag, value = parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionState {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionState, tag)
	}

	expectedValue = "[:tool789:]pending"
	if value != expectedValue {
		t.Errorf("Expected value %s, got %s", expectedValue, value)
	}
}

func TestOnToolResultCallback(t *testing.T) {
	// Create a session with mock output
	output := &mockOutput{}
	session := &Session{
		Output:   output,
		Messages: []llm.Message{},
	}

	// Create a mock tool result callback (simulating what happens in processPrompt)
	callback := func(toolCallID string, result llm.ToolResultOutput) error {
		// Add tool result message to session messages
		session.Messages = append(session.Messages, llm.Message{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: toolCallID,
				Output:     result,
			}},
		})

		// Send tool result status indicator to adaptor
		status := "success"
		if _, ok := result.(llm.ToolResultOutputError); ok {
			status = "error"
		}
		session.writeToolResult(toolCallID, status)

		return nil
	}

	// Test success result
	err := callback("call1", llm.ToolResultOutputText{Type: "text", Text: "success output"})
	if err != nil {
		t.Fatalf("Callback returned error: %v", err)
	}

	// Check that message was added
	if len(session.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(session.Messages))
	}

	// Check that TLV was sent
	tag, value := parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionState {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionState, tag)
	}

	expectedValue := "[:call1:]success"
	if value != expectedValue {
		t.Errorf("Expected value %s, got %s", expectedValue, value)
	}

	// Test error result
	output.data = nil
	err = callback("call2", llm.ToolResultOutputError{Type: "error", Error: "something failed"})
	if err != nil {
		t.Fatalf("Callback returned error: %v", err)
	}

	tag, value = parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionState {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionState, tag)
	}

	expectedValue = "[:call2:]error"
	if value != expectedValue {
		t.Errorf("Expected value %s, got %s", expectedValue, value)
	}
}

func TestWriteToolCallWithPending(t *testing.T) {
	// Create a session with mock output
	output := &mockOutput{}
	session := &Session{
		Output: output,
	}

	// Call writeToolCall with posix_shell (a known tool)
	session.writeToolCall("posix_shell", `{"command":"ls"}`, "tool123")

	// Should have written two TLV messages:
	// 1. TagFunctionCall with tool call JSON (creates window)
	// 2. TagFunctionState with pending status (updates window)

	// Parse first message (tool call display)
	tag1, value1 := parseTLVFromBytes(output.data)
	if tag1 != stream.TagFunctionCall {
		t.Errorf("Expected first tag %s, got %s", stream.TagFunctionCall, tag1)
	}

	// The tool call should be JSON with id, name, input
	if value1 == "" {
		t.Error("Expected non-empty tool call value")
	}

	// Parse second message (pending status)
	// The mockOutput concatenates all writes, so we need to parse from the right position
	data := output.data
	// Find the second TLV message
	if len(data) > 6 {
		// First message length
		length1 := int(binary.BigEndian.Uint32(data[2:6]))
		// Skip to second message
		if len(data) >= 12+length1 {
			offset := 6 + length1
			tag2 := string(data[offset : offset+2])
			if tag2 != stream.TagFunctionState {
				t.Errorf("Expected second tag %s, got %s", stream.TagFunctionState, tag2)
			}

			// Parse second message value
			length2 := int(binary.BigEndian.Uint32(data[offset+2 : offset+6]))
			if len(data) >= offset+6+length2 {
				value2 := string(data[offset+6 : offset+6+length2])
				expectedValue2 := "[:tool123:]pending"
				if value2 != expectedValue2 {
					t.Errorf("Expected pending value %s, got %s", expectedValue2, value2)
				}
			}
		}
	}
}

// parseTLVFromBytes extracts tag and value from TLV-encoded bytes
func parseTLVFromBytes(data []byte) (string, string) {
	if len(data) < 6 {
		return "", ""
	}
	tag := string(data[0:2])
	length := int(binary.BigEndian.Uint32(data[2:6]))
	if len(data) < 6+length {
		return tag, ""
	}
	value := string(data[6 : 6+length])
	return tag, value
}
