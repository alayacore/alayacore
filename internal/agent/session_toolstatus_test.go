package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

func TestOnToolOutputCallback(t *testing.T) {
	output := &mockOutput{}
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	// Simulate the OnToolOutput callback from processPrompt
	callback := func(id string, content []llm.ContentPart, err error) {
		session.Contents = append(session.Contents, &llm.ToolOutputPart{
			ID:      id,
			Output:  content,
			IsError: err != nil,
		})

		data, marshalErr := marshalToolOutputBody(protocol.ContentMeta{}, id, content, err != nil)
		if marshalErr == nil {
			session.writeTLV(wire.TagUserF, data)
		}
	}

	callback("call1", []llm.ContentPart{&llm.TextPart{Text: "success output"}}, nil)

	if len(session.Contents) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(session.Contents))
	}

	tag, value := parseFrameFromBytes(output.data)
	if tag != wire.TagUserF {
		t.Errorf("Expected tag %s, got %s", wire.TagUserF, tag)
	}

	var got protocol.ToolOutputData
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.CallID != "call1" || got.IsError != false {
		t.Errorf("Expected {call1, false}, got {%s, %t}", got.CallID, got.IsError)
	}

	output.data = nil
	callback("call2", []llm.ContentPart{&llm.TextPart{Text: "something failed"}}, errors.New("something failed"))

	tag, value = parseFrameFromBytes(output.data)
	if tag != wire.TagUserF {
		t.Errorf("Expected tag %s, got %s", wire.TagUserF, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.CallID != "call2" || got.IsError != true {
		t.Errorf("Expected {call2, true}, got {%s, %t}", got.CallID, got.IsError)
	}
}

// parseFrameFromBytes decodes one frame from what a writer captured.
func parseFrameFromBytes(data []byte) (string, string) {
	tag, value, err := wire.Decode(data)
	if err != nil {
		return "", ""
	}
	return tag, value
}
