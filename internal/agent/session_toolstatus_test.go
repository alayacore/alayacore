package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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
			session.writeTLV(tlv.TagUserF, data)
		}
	}

	callback("call1", []llm.ContentPart{&llm.TextPart{Text: "success output"}}, nil)

	if len(session.Contents) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(session.Contents))
	}

	tag, value := parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
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

	tag, value = parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.CallID != "call2" || got.IsError != true {
		t.Errorf("Expected {call2, true}, got {%s, %t}", got.CallID, got.IsError)
	}
}

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
