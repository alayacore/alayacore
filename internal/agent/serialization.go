package agent

// ContentPart ↔ TLV serialization and shared marshal helpers.
//
// This file is the single source of truth for serializing ContentParts
// to/from TLV tag+value pairs. All TLV serialization (persistence,
// streaming output, session replay) must go through these functions
// to avoid duplicating the switch dispatch logic.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// Shared Marshal Helpers
// ============================================================================

// marshalToolInputData marshals a tool call (ToolInputData) to JSON bytes.
// Each field is included only when non-zero, matching the TLV protocol's
// start-vs-complete frame distinction:
//   - Start frame: id + name (input is nil/empty)
//   - Complete frame: id + input (name is empty)
//   - Full (persistence): id + name + input
//
// A malformed tool input (truncated/non-JSON bytes from the provider) is
// string-encoded rather than rejected: json.RawMessage.MarshalJSON
// validates the raw bytes, so marshaling it directly would abort the whole
// step inside handleToolInputComplete — before the tool goroutine starts —
// leaving the tool window stuck in its pending/spinner state with no UF
// frame to settle it. With the fallback the frame still carries the input;
// the tool then fails to parse it and reports a normal error result
// (UF isError → ✗). nil input (start frames) is left untouched so it stays
// JSON null. json.Marshal(string) (not %q) generates standard JSON string
// escapes — %q's \xff form is not valid JSON.
func marshalToolInputData(id, name string, input json.RawMessage) ([]byte, error) {
	if len(input) > 0 && !json.Valid(input) {
		encoded, _ := json.Marshal(string(input)) // strings always marshal
		input = encoded
	}
	data, err := json.Marshal(protocol.ToolInputData{
		ID:    id,
		Name:  name,
		Input: input,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool input data: %w", err)
	}
	return data, nil
}

// marshalToolOutputData marshals a tool result (ToolOutputData) to JSON bytes.
// On serialization error for the inner content parts, a fallback text is used
// so the caller never receives a marshal error from content serialization —
// only from the outer JSON marshal.
func marshalToolOutputData(id string, contents []llm.ContentPart, isError bool) ([]byte, error) {
	contentJSON, serErr := serializeContentParts(contents)
	if serErr != nil {
		contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
	}
	data, err := json.Marshal(protocol.ToolOutputData{
		ID:      id,
		Output:  contentJSON,
		IsError: isError,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool output data: %w", err)
	}
	return data, nil
}

// ============================================================================
// ContentPart ↔ TLV
// ============================================================================

// contentPartToTLV serializes a ContentPart as a TLV tag and value string (without history ID).
func contentPartToTLV(part llm.ContentPart) (tag string, content string, err error) {
	switch p := part.(type) {
	case *llm.TextPart:
		if part.GetRole() == llm.RoleAssistant {
			return tlv.TagAssistantT, p.Text, nil
		}
		return tlv.TagUserT, p.Text, nil
	case *llm.ImagePart:
		return tlv.TagUserI, p.URI, nil
	case *llm.VideoPart:
		return tlv.TagUserV, p.URI, nil
	case *llm.AudioPart:
		return tlv.TagUserA, p.URI, nil
	case *llm.DocumentPart:
		return tlv.TagUserD, p.URI, nil
	case *llm.ReasoningPart:
		return tlv.TagAssistantR, p.Text, nil
	case *llm.ToolInputPart:
		jsonData, err := marshalToolInputData(p.ID, p.Name, p.Input)
		if err != nil {
			return "", "", err
		}
		return tlv.TagAssistantF, string(jsonData), nil
	case *llm.ToolOutputPart:
		jsonData, err := marshalToolOutputData(p.ID, p.Output, p.IsError)
		if err != nil {
			return "", "", err
		}
		return tlv.TagUserF, string(jsonData), nil
	default:
		return "", "", fmt.Errorf("unknown content part type: %T", part)
	}
}

// contentPartFromTLV converts a TLV record into a ContentPart with Role set.
func contentPartFromTLV(tag string, content []byte) (llm.ContentPart, error) {
	cleanContent := string(content)
	if _, stripped, ok := tlv.UnwrapID(cleanContent); ok {
		cleanContent = stripped
	}

	switch tag {
	case tlv.TagUserT:
		return &llm.TextPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case tlv.TagUserI:
		return &llm.ImagePart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case tlv.TagUserV:
		return &llm.VideoPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case tlv.TagUserA:
		return &llm.AudioPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case tlv.TagUserD:
		return &llm.DocumentPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case tlv.TagAssistantT:
		return &llm.TextPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}, nil
	case tlv.TagAssistantR:
		return &llm.ReasoningPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}, nil
	case tlv.TagAssistantF:
		var fd protocol.ToolInputData
		if err := json.Unmarshal([]byte(cleanContent), &fd); err != nil {
			return nil, fmt.Errorf("failed to parse function data: %w", err)
		}
		if fd.Name == "" {
			return nil, nil
		}
		return &llm.ToolInputPart{
			ID: fd.ID, Name: fd.Name, Input: fd.Input, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		}, nil
	case tlv.TagUserF:
		var tr protocol.ToolOutputData
		if err := json.Unmarshal([]byte(cleanContent), &tr); err != nil {
			return nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		contentParts, err := deserializeContentParts(tr.Output)
		if err != nil {
			return nil, fmt.Errorf("failed to parse tool result content: %w", err)
		}
		return &llm.ToolOutputPart{ID: tr.ID, Output: contentParts, IsError: tr.IsError, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}}, nil
	default:
		return nil, fmt.Errorf("unknown tag: %s", tag)
	}
}
