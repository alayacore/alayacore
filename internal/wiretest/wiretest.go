// Package wiretest builds v14 wire frames for tests.
//
// It exists so the hundred-odd tests that feed a frame into a session or an
// adapter do not each have to hand-write the content-item JSON. Every builder
// returns a frame *value* (the JSON body), to be paired with its tag by the
// caller — the tag is the part a test usually wants to be explicit about.
//
// The history id is the "id" field; authoritative frames also carry a fixed
// "time" (tests care that a time round-trips, not what it is).
package wiretest

import (
	"encoding/json"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
)

// Stamp is the fixed time written onto authoritative frames.
var Stamp = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func meta(id string) protocol.ContentMeta {
	m := protocol.ContentMeta{ID: id}
	if id != "" {
		m.Time = Stamp.Format(time.RFC3339)
	}
	return m
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// UserText builds a UX body carrying text (id "" for input, set for an echo).
func UserText(id, text string) string {
	return mustJSON(protocol.ContentData{ContentMeta: meta(id), ContentItem: protocol.ContentItem{Type: protocol.TypeText, Text: text}})
}

// UserMedia builds a UX body carrying media of the given kind.
func UserMedia(id, kind, uri string) string {
	return mustJSON(protocol.ContentData{ContentMeta: meta(id), ContentItem: protocol.ContentItem{Type: kind, URI: uri}})
}

// AssistantText builds an AX body carrying text.
func AssistantText(id, text string) string {
	return mustJSON(protocol.ContentData{ContentMeta: meta(id), ContentItem: protocol.ContentItem{Type: protocol.TypeText, Text: text}})
}

// AssistantReasoning builds an AX body carrying reasoning.
func AssistantReasoning(id, text string) string {
	return mustJSON(protocol.ContentData{ContentMeta: meta(id), ContentItem: protocol.ContentItem{Type: protocol.TypeReasoning, Text: text}})
}

// AssistantTextDelta builds an Ax body carrying a text fragment (no time).
func AssistantTextDelta(id, text string) string {
	return mustJSON(protocol.ContentData{ContentMeta: protocol.ContentMeta{ID: id}, ContentItem: protocol.ContentItem{Type: protocol.TypeText, Text: text}})
}

// AssistantReasoningDelta builds an Ax body carrying a reasoning fragment.
func AssistantReasoningDelta(id, text string) string {
	return mustJSON(protocol.ContentData{ContentMeta: protocol.ContentMeta{ID: id}, ContentItem: protocol.ContentItem{Type: protocol.TypeReasoning, Text: text}})
}

// ToolInput builds an AF body. input is the tool-argument JSON (nil for a start
// frame, or a name-only frame when name is set and input is nil).
func ToolInput(id, callID, name string, input json.RawMessage) string {
	return mustJSON(protocol.ToolInputData{CallID: callID, Name: name, Input: input, ContentMeta: meta(id)})
}

// ToolInputLegacy builds an AF body from a {"id"/"call_id",...} JSON object —
// the legacy spelling still parses, because tests often marshal a
// protocol.ToolInputData (whose field is now call_id) and hand it here.
func ToolInputLegacy(id, legacy string) string {
	var m struct {
		ID     string          `json:"id"`
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Input  json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal([]byte(legacy), &m)
	callID := m.CallID
	if callID == "" {
		callID = m.ID
	}
	return ToolInput(id, callID, m.Name, m.Input)
}

// ToolInputDelta builds an Af body.
func ToolInputDelta(id, callID, delta string) string {
	return mustJSON(protocol.ToolInputDeltaData{CallID: callID, Delta: delta, ContentMeta: protocol.ContentMeta{ID: id}})
}

// ToolInputDeltaLegacy builds an Af body from an {"id"/"call_id","delta"} object.
func ToolInputDeltaLegacy(id, legacy string) string {
	var m struct {
		ID     string `json:"id"`
		CallID string `json:"call_id"`
		Delta  string `json:"delta"`
	}
	_ = json.Unmarshal([]byte(legacy), &m)
	callID := m.CallID
	if callID == "" {
		callID = m.ID
	}
	return ToolInputDelta(id, callID, m.Delta)
}

// ToolOutput builds a UF body.
func ToolOutput(id, callID string, items []protocol.ContentItem, isError bool) string {
	return mustJSON(protocol.ToolOutputData{CallID: callID, Output: items, IsError: isError, ContentMeta: meta(id)})
}

// ToolOutputText builds a UF body with a single text item.
func ToolOutputText(id, callID, text string, isError bool) string {
	return ToolOutput(id, callID, []protocol.ContentItem{{Type: protocol.TypeText, Text: text}}, isError)
}

// ToolOutputLegacy builds a UF body from an {"id"/"call_id","output"/"content",...} object.
func ToolOutputLegacy(id, legacy string) string {
	var m struct {
		ID      string                 `json:"id"`
		CallID  string                 `json:"call_id"`
		Output  []protocol.ContentItem `json:"output"`
		Content []protocol.ContentItem `json:"content"`
		IsError bool                   `json:"is_error"`
	}
	_ = json.Unmarshal([]byte(legacy), &m)
	callID := m.CallID
	if callID == "" {
		callID = m.ID
	}
	items := m.Output
	if items == nil {
		items = m.Content
	}
	return ToolOutput(id, callID, items, m.IsError)
}

// ToolOutputDelta builds a Uf body.
func ToolOutputDelta(id, callID, text string) string {
	return mustJSON(protocol.ToolOutputDeltaData{CallID: callID, Text: text, ContentMeta: protocol.ContentMeta{ID: id}})
}

// ToolOutputDeltaLegacy builds a Uf body from an {"id"/"call_id","text"} object.
func ToolOutputDeltaLegacy(id, legacy string) string {
	var m struct {
		ID     string `json:"id"`
		CallID string `json:"call_id"`
		Text   string `json:"text"`
	}
	_ = json.Unmarshal([]byte(legacy), &m)
	callID := m.CallID
	if callID == "" {
		callID = m.ID
	}
	return ToolOutputDelta(id, callID, m.Text)
}
