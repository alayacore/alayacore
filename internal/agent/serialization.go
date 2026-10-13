package agent

// ContentPart ↔ wire frame (frame tag + JSON body) serialization.
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
// the kind. See internal/wire for the tag vocabulary and internal/protocol for
// the payload types.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
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

// contentPartJSON serializes a ContentPart as a frame tag and JSON body. id is
// the history id for a wire frame, or "" for the session file.
func contentPartJSON(part llm.ContentPart, id string) (tag string, body string, err error) {
	meta := protocol.ContentMeta{ID: id, Time: rfc3339(part.GetTime())}

	switch v := part.(type) {
	case *llm.TextPart:
		tag = wire.TagUserX
		if part.GetRole() == llm.RoleAssistant {
			tag = wire.TagAssistantX
		}
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeText, Text: v.Text})
	case *llm.ReasoningPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeReasoning, Text: v.Text})
		tag = wire.TagAssistantX
	case *llm.ImagePart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeImage, URI: v.URI})
		tag = wire.TagUserX
	case *llm.VideoPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeVideo, URI: v.URI, FPS: v.FPS, Res: v.Res})
		tag = wire.TagUserX
	case *llm.AudioPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeAudio, URI: v.URI})
		tag = wire.TagUserX
	case *llm.DocumentPart:
		body, err = marshalContentBody(meta, protocol.ContentItem{Type: protocol.TypeDocument, URI: v.URI})
		tag = wire.TagUserX
	case *llm.ToolInputPart:
		body, err = marshalToolInputBody(meta, v.ID, v.Name, v.Input)
		tag = wire.TagAssistantF
	case *llm.ToolOutputPart:
		body, err = marshalToolOutputBody(meta, v.ID, v.Output, v.IsError)
		tag = wire.TagUserF
	default:
		return "", "", fmt.Errorf("unknown content part type: %T", part)
	}
	if err != nil {
		return "", "", err
	}
	return tag, body, nil
}

// intoModelKind reports whether a content kind travels into the model — user
// content (UX) and tool-result content (UF.output). Reasoning is the one kind
// that belongs to the assistant side alone, so it may appear under neither; a
// kind accepted on one side and not the other would let a part's role depend on
// which representation a reader saw.
func intoModelKind(kind string) bool {
	switch kind {
	case protocol.TypeText, protocol.TypeImage, protocol.TypeVideo, protocol.TypeAudio, protocol.TypeDocument:
		return true
	}
	return false
}

// assistantKind reports whether a content kind may appear under AX: the
// assistant produces text and reasoning. Tool calls and their results have
// their own frames (AF/UF), not content items.
func assistantKind(kind string) bool {
	return kind == protocol.TypeText || kind == protocol.TypeReasoning
}

// contentPartFromJSON converts a frame tag + JSON body into a ContentPart with
// Role and Time set. A nil part with a nil error means "nothing to keep" — an
// AF start frame (name only) is skipped, matching the old writer's shape.
func contentPartFromJSON(tag string, body []byte) (llm.ContentPart, error) {
	switch tag {
	case wire.TagUserX, wire.TagAssistantX:
		var d protocol.ContentData
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, fmt.Errorf("failed to parse content: %w", err)
		}
		// The tag names the side, the item names the kind; a kind on the wrong
		// side is a malformed frame. Checked after partFromContentItem so an
		// unknown kind still reports itself as unknown.
		part, err := partFromContentItem(d.ContentItem)
		if err != nil {
			return nil, err
		}
		if tag == wire.TagUserX && !intoModelKind(d.Type) {
			return nil, fmt.Errorf("content type %q cannot be user content", d.Type)
		}
		if tag == wire.TagAssistantX && !assistantKind(d.Type) {
			return nil, fmt.Errorf("content type %q cannot be assistant content", d.Type)
		}
		role := llm.RoleUser
		if tag == wire.TagAssistantX {
			role = llm.RoleAssistant
		}
		part.SetRole(role)
		part.SetTime(parseWireTime(d.Time))
		return part, nil
	case wire.TagAssistantF:
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
	case wire.TagUserF:
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
