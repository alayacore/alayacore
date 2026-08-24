// Package llm provides a custom LLM client with streaming support
package llm

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
)

// DefaultMaxTokens is the default maximum output tokens when the user
// doesn't specify one. 128K covers coding agents generating large code
// blocks, multi-file changes, and long tool call chains.
const DefaultMaxTokens = 131072

// MessageRole represents the role of a message
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ContentPart type discriminator strings for TLV serialization.
const (
	ContentPartText       = "text"
	ContentPartImage      = "image"
	ContentPartVideo      = "video"
	ContentPartAudio      = "audio"
	ContentPartDocument   = "document"
	ContentPartReasoning  = "reasoning"
	ContentPartToolCall   = "tool_use"
	ContentPartToolResult = "tool_result"
)

// ContentPart represents a part of message content
type ContentPart interface {
	GetHistoryID() uint64
	SetHistoryID(uint64)
	GetRole() MessageRole
	SetRole(MessageRole)
	UpdateContentPartMeta(historyID uint64, role MessageRole)
}

// ContentPartMeta holds the metadata common to all ContentPart types.
// Embedded in each concrete ContentPart to avoid duplicating
// the HistoryID/Role fields and their accessor methods.
type ContentPartMeta struct {
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`
}

func (m *ContentPartMeta) GetHistoryID() uint64   { return m.HistoryID }
func (m *ContentPartMeta) SetHistoryID(id uint64) { m.HistoryID = id }
func (m *ContentPartMeta) GetRole() MessageRole   { return m.Role }
func (m *ContentPartMeta) SetRole(r MessageRole)  { m.Role = r }
func (m *ContentPartMeta) UpdateContentPartMeta(id uint64, r MessageRole) {
	m.HistoryID = id
	m.Role = r
}

// TextPart represents text content
type TextPart struct {
	ContentPartMeta
	Text string
}

// ImagePart represents an image content (data:image/...;base64,... or URL)
type ImagePart struct {
	ContentPartMeta
	URI string
}

// VideoPart represents a video content (data:video/...;base64,... or URL)
type VideoPart struct {
	ContentPartMeta
	URI string
}

// AudioPart represents an audio content (data:audio/...;base64,... or URL)
type AudioPart struct {
	ContentPartMeta
	URI string
}

// DocumentPart represents a document content (data:application/...;base64,... or URL)
type DocumentPart struct {
	ContentPartMeta
	URI string
}

// MediaContentPart returns the appropriate ContentPart for a MIME type.
// Supported prefixes:
//
//	image/ → ImagePart
//	video/ → VideoPart
//	audio/ → AudioPart
//	default → DocumentPart
//
// Note: text/* MIME types should be decoded to TextPart by the caller
// before calling this function.
func MediaContentPart(mimeType, dataURI string) ContentPart {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return &ImagePart{URI: dataURI}
	case strings.HasPrefix(mimeType, "video/"):
		return &VideoPart{URI: dataURI}
	case strings.HasPrefix(mimeType, "audio/"):
		return &AudioPart{URI: dataURI}
	default:
		return &DocumentPart{URI: dataURI}
	}
}

// ReasoningPart represents reasoning/thinking content.
type ReasoningPart struct {
	ContentPartMeta
	Text string
}

// ToolInputPart represents a tool call stored in conversation history.
type ToolInputPart struct {
	ContentPartMeta
	ID    string
	Input json.RawMessage
	Name  string
}

// ToConfirmRequest builds a ToolConfirmRequest from a ToolInputPart.
func (tc *ToolInputPart) ToConfirmRequest() ToolConfirmRequest {
	return ToolConfirmRequest{
		ID:    tc.ID,
		Name:  tc.Name,
		Input: tc.Input,
	}
}

// ToolOutputPart represents a tool execution result.
type ToolOutputPart struct {
	ContentPartMeta
	ID      string
	Output  []ContentPart
	IsError bool
}

// ToolDefinition defines a tool that can be called
type ToolDefinition struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Usage tracks token usage.
type Usage struct {
	CacheCreationTokens int64
	CacheReadTokens     int64
	InputTokens         int64
	OutputTokens        int64
}

// StreamEvent represents a streaming event
type StreamEvent interface {
	isStreamEvent()
}

// TextDeltaEvent represents text content streaming
type TextDeltaEvent struct {
	Delta string
	Index int
}

func (TextDeltaEvent) isStreamEvent() {}

// TextCompleteEvent signals that a text content block is fully received.
// Carries the complete authoritative text.
type TextCompleteEvent struct {
	Text  string
	Index int
}

func (TextCompleteEvent) isStreamEvent() {}

// ReasoningDeltaEvent represents reasoning content streaming
type ReasoningDeltaEvent struct {
	Delta string
	Index int
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ReasoningCompleteEvent signals that a reasoning content block is fully received.
// Carries the complete authoritative reasoning text.
type ReasoningCompleteEvent struct {
	Text  string
	Index int
}

func (ReasoningCompleteEvent) isStreamEvent() {}

// ToolInputStartEvent signals that a tool call has started
type ToolInputStartEvent struct {
	ID    string
	Name  string
	Index int
}

func (ToolInputStartEvent) isStreamEvent() {}

// ToolInputDeltaEvent signals a partial JSON chunk of tool arguments.
type ToolInputDeltaEvent struct {
	ID    string
	Delta string
	Index int
}

func (ToolInputDeltaEvent) isStreamEvent() {}

// ToolInputCompleteEvent signals that a tool call's arguments have finished streaming
type ToolInputCompleteEvent struct {
	ID    string
	Input json.RawMessage
	Index int
}

func (ToolInputCompleteEvent) isStreamEvent() {}

// StepCompleteEvent represents completion of an agentic step.
type StepCompleteEvent struct {
	Contents   []ContentPart
	Usage      Usage
	StopReason string
}

func (StepCompleteEvent) isStreamEvent() {}

// Provider defines the interface for LLM providers
type Provider interface {
	StreamMessages(ctx context.Context, contents []ContentPart, tools []ToolDefinition, systemPrompt string, extraSystemPrompt string) (iter.Seq2[StreamEvent, error], error)
	SetReasoningLevel(level int)
	SetReasoningConfigs(configs map[int]json.RawMessage)
	SetVideoConfig(fps int, resolution int)
}
