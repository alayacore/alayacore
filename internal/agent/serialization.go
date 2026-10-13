package agent

// ContentPart ↔ wire frame (TLV tag + JSON body) serialization.
//
// This file is the single source of truth for turning a ContentPart into a
// frame and back. Two callers share it: the wire (echo / replay, which carry
// the history id) and the session file (which does not). The id travels as the
// top-level "id" field of a content frame's JSON body, so the serializer takes
// it as an argument — an empty id means "omit", which is what persistence uses
// so a live history id can never leak into a file.
//
// The frame's concrete kind lives inside the JSON ("type"), not in the tag: the
// tag names the plane (UX/AX content, AF/UF function), the content item names
// the kind. See internal/tlv for the tag vocabulary and internal/protocol for
// the payload types.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// malformedToolInputPrefix marks a tool-input field whose raw bytes are
// not valid JSON. The prefix is fixed ASCII so it can never collide with a
// real argument, and the raw bytes are preserved after it for diagnosis.
// It marks the RAW BYTES (the ToolInputData.Input field content) as
// damaged — not a parsed argument, which never came into existence.
const malformedToolInputPrefix = "malformed tool input: "

// ============================================================================
// Time helpers
// ============================================================================

// rfc3339 renders a part time for the wire, or "" when unset.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// parseWireTime parses a wire time; "" or a malformed value yields the zero
// time (an old, migrated file carries no per-part time).
func parseWireTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ============================================================================
// Frame bodies
// ============================================================================

// marshalContentBody builds the JSON body of a content frame (UX/AX).
func marshalContentBody(meta protocol.ContentMeta, item protocol.ContentItem) (string, error) {
	b, err := json.Marshal(protocol.ContentData{ContentMeta: meta, ContentItem: item})
	if err != nil {
		return "", fmt.Errorf("failed to marshal content body: %w", err)
	}
	return string(b), nil
}

// marshalToolInputBody builds the JSON body of an AF frame.
func marshalToolInputBody(meta protocol.ContentMeta, callID, name string, input json.RawMessage) (string, error) {
	if len(input) > 0 && !json.Valid(input) {
		// json.Marshal(string), not %q: \xff is not valid JSON.
		encoded, _ := json.Marshal(malformedToolInputPrefix + string(input))
		input = encoded
	}
	b, err := json.Marshal(protocol.ToolInputData{CallID: callID, Name: name, Input: input, ContentMeta: meta})
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool input body: %w", err)
	}
	return string(b), nil
}

// marshalToolOutputBody builds the JSON body of a UF frame. On a content-item
// serialization error the body carries a fallback text item, so the caller only
// sees errors from the outer JSON marshal.
func marshalToolOutputBody(meta protocol.ContentMeta, callID string, parts []llm.ContentPart, isError bool) (string, error) {
	items, itemsErr := contentItemsFromParts(parts)
	if itemsErr != nil {
		items = []protocol.ContentItem{{Type: protocol.TypeText, Text: "(serialization error)"}}
	}
	b, err := json.Marshal(protocol.ToolOutputData{CallID: callID, Output: items, IsError: isError, ContentMeta: meta})
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool output body: %w", err)
	}
	return string(b), nil
}

// ============================================================================
// ContentPart ↔ frame
// ============================================================================

// contentPartJSON serializes a ContentPart as a TLV tag and JSON body. id is
// the history id for a wire frame, or "" for the session file.
func contentPartJSON(part llm.ContentPart, id string) (tag string, body string, err error) {
	meta := protocol.ContentMeta{ID: id, Time: rfc3339(part.GetTime())}

	switch v := part.(type) {
	case *llm.TextPart:
		tag = tlv.TagUserX
		if part.GetRole() == llm.RoleAssistant {
			tag = tlv.TagAssistantX
		}
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeText, Text: v.Text})
	case *llm.ReasoningPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeReasoning, Text: v.Text})
		tag = tlv.TagAssistantX
	case *llm.ImagePart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeImage, URI: v.URI})
		tag = tlv.TagUserX
	case *llm.VideoPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeVideo, URI: v.URI, FPS: v.FPS, Res: v.Res})
		tag = tlv.TagUserX
	case *llm.AudioPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeAudio, URI: v.URI})
		tag = tlv.TagUserX
	case *llm.DocumentPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeDocument, URI: v.URI})
		tag = tlv.TagUserX
	case *llm.ToolInputPart:
		body, err = marshalToolInputBody(meta, v.ID, v.Name, v.Input)
		tag = tlv.TagAssistantF
	case *llm.ToolOutputPart:
		body, err = marshalToolOutputBody(meta, v.ID, v.Output, v.IsError)
		tag = tlv.TagUserF
	default:
		return "", "", fmt.Errorf("unknown content part type: %T", part)
	}
	if err != nil {
		return "", "", err
	}
	return tag, body, nil
}

// contentPartFromJSON converts a TLV tag + JSON body into a ContentPart with
// Role and Time set. A nil part with a nil error means "nothing to keep" — an
// AF start frame (name only) is skipped, matching the old writer's shape.
func contentPartFromJSON(tag string, body []byte) (llm.ContentPart, error) {
	switch tag {
	case tlv.TagUserX, tlv.TagAssistantX:
		var d protocol.ContentData
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, fmt.Errorf("failed to parse content: %w", err)
		}
		// The tag names the side, the item names the kind; a kind on the wrong
		// side is a malformed frame. Rejecting it keeps a part's role from
		// depending on which representation a reader saw: reasoning is assistant
		// content, and contentPartJSON writes a ReasoningPart as AX whatever its
		// role — so accepting one under UX would store a user-role part that the
		// file then records as assistant.
		if tag == tlv.TagUserX && d.Type == protocol.TypeReasoning {
			return nil, fmt.Errorf("content type %q cannot be user content", d.Type)
		}
		if tag == tlv.TagAssistantX && d.Type != protocol.TypeText && d.Type != protocol.TypeReasoning {
			return nil, fmt.Errorf("content type %q cannot be assistant content", d.Type)
		}
		part, err := partFromContentItem(d.ContentItem)
		if err != nil {
			return nil, err
		}
		role := llm.RoleUser
		if tag == tlv.TagAssistantX {
			role = llm.RoleAssistant
		}
		part.SetRole(role)
		part.SetTime(parseWireTime(d.Time))
		return part, nil
	case tlv.TagAssistantF:
		var fd protocol.ToolInputData
		if err := json.Unmarshal(body, &fd); err != nil {
			return nil, fmt.Errorf("failed to parse function data: %w", err)
		}
		if fd.Name == "" {
			return nil, nil
		}
		return &llm.ToolInputPart{
			ID: fd.CallID, Name: fd.Name, Input: fd.Input,
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, Time: parseWireTime(fd.Time)},
		}, nil
	case tlv.TagUserF:
		var tr protocol.ToolOutputData
		if err := json.Unmarshal(body, &tr); err != nil {
			return nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		parts, err := partsFromContentItems(tr.Output)
		if err != nil {
			return nil, fmt.Errorf("failed to parse tool result content: %w", err)
		}
		return &llm.ToolOutputPart{
			ID: tr.CallID, Output: parts, IsError: tr.IsError,
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool, Time: parseWireTime(tr.Time)},
		}, nil
	default:
		return nil, fmt.Errorf("unknown tag: %s", tag)
	}
}

// wireID formats a history id for the wire "id" field.
func wireID(id uint64) string {
	return strconv.FormatUint(id, 10)
}

// wireMetaNow is the frame metadata for a wire frame carrying a history id,
// stamped with the time of the step it belongs to (Session.stepStartedAt), so
// the frame's time is the record's. A task with no step clock (a summarize)
// falls back to the moment of use.
func (s *Session) wireMetaNow(id uint64) protocol.ContentMeta {
	t := s.stepStartedAt
	if t.IsZero() {
		t = time.Now()
	}
	return protocol.ContentMeta{ID: wireID(id), Time: rfc3339(t)}
}
