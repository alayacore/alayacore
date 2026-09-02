// Package llm provides a custom LLM client with streaming support
package llm

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"time"
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
	// GetBlockKey returns the provider-assigned identity of the stream block
	// this part was assembled from. It is how llm.Agent binds a history ID onto
	// the right part; see the BlockKey docs on ContentPartMeta.
	GetBlockKey() string
	SetBlockKey(string)
	UpdateContentPartMeta(historyID uint64, role MessageRole)
}

// ContentPartMeta holds the metadata common to all ContentPart types.
// Embedded in each concrete ContentPart to avoid duplicating
// the HistoryID/Role fields and their accessor methods.
type ContentPartMeta struct {
	HistoryID uint64      `json:"-"`
	Role      MessageRole `json:"-"`

	// BlockKey is the identity of the streaming block this part came from, as
	// handed out by the provider. A history ID is issued when that block first
	// appears in the stream and later claimed by the part carrying the same
	// key — an identity lookup, never a positional one.
	//
	// The key is opaque by contract: it is only ever compared for equality.
	// It must be stable from the block's first event to the part's assembly,
	// and it must not be derived from, or interpreted as, a position in the
	// content array. Providers own the naming; see the Key field on the
	// streaming events below, which carries the same value.
	BlockKey string `json:"-"`
}

func (m *ContentPartMeta) GetHistoryID() uint64   { return m.HistoryID }
func (m *ContentPartMeta) SetHistoryID(id uint64) { m.HistoryID = id }
func (m *ContentPartMeta) GetRole() MessageRole   { return m.Role }
func (m *ContentPartMeta) SetRole(r MessageRole)  { m.Role = r }
func (m *ContentPartMeta) GetBlockKey() string    { return m.BlockKey }
func (m *ContentPartMeta) SetBlockKey(k string)   { m.BlockKey = k }
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

// StepStats captures speed metrics for a single LLM round trip (step).
//
//   - Duration is measured from step start (recorded before
//     Provider.StreamMessages, so request/network latency is included)
//     to the provider stream end (StepCompleteEvent). Tool execution
//     time — which happens in parallel after the stream ends — is
//     excluded.
//   - TokensPerSec is the END-TO-END throughput: output tokens per
//     second of the whole round trip, latency (TimeToFirstToken)
//     included. It is deliberately simple and always computable for any
//     completed step with output tokens — no reliability gates. It is
//     NOT the server-side decode speed (e.g. llama.cpp's eval time): the
//     client cannot observe that exactly, and any attempt to estimate it
//     (subtracting TTFT) is systematically inflated for short or burst
//     outputs. Expect this value to be <= the server's decode rate.
//   - OutputTokens comes from the provider's authoritative usage.
//
// A zero-value StepStats (Duration == 0) means the step never completed
// (canceled or failed) and carries no speed information.
type StepStats struct {
	Step             int
	OutputTokens     int64
	Duration         time.Duration // end-to-end: request → stream end (incl. TTFT)
	TokensPerSec     float64       // OutputTokens / Duration (end-to-end throughput)
	TimeToFirstToken time.Duration
}

// setFirstToken records the time-to-first-token on the step's first output
// delta (text, reasoning, or tool-call arguments). Idempotent — the first
// delta wins, later ones leave it untouched.
func (s *StepStats) setFirstToken(stepStart time.Time) {
	if s.TimeToFirstToken == 0 {
		s.TimeToFirstToken = time.Since(stepStart)
	}
}

// StreamEvent represents a streaming event
type StreamEvent interface {
	isStreamEvent()
}

// TextDeltaEvent represents text content streaming
type TextDeltaEvent struct {
	Delta string

	// Key identifies which content block this fragment belongs to. Every
	// streaming event carries it and every persisted part carries the same
	// value, so a history ID issued during streaming can be claimed by the
	// right part at step completion without any positional assumption. Opaque:
	// only ever compared for equality, never ordered, added to, or used as an
	// array index. Providers choose the naming (see internal/llm/providers).
	Key string
}

func (TextDeltaEvent) isStreamEvent() {}

// TextCompleteEvent signals that a text content block is fully received.
// Carries the complete authoritative text.
type TextCompleteEvent struct {
	Text string
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
}

func (TextCompleteEvent) isStreamEvent() {}

// ReasoningDeltaEvent represents reasoning content streaming
type ReasoningDeltaEvent struct {
	Delta string
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ReasoningCompleteEvent signals that a reasoning content block is fully received.
// Carries the complete authoritative reasoning text.
type ReasoningCompleteEvent struct {
	Text string
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
}

func (ReasoningCompleteEvent) isStreamEvent() {}

// ToolInputStartEvent signals that a tool call has started
type ToolInputStartEvent struct {
	ID   string
	Name string
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
}

func (ToolInputStartEvent) isStreamEvent() {}

// ToolInputDeltaEvent signals a partial JSON chunk of tool arguments.
type ToolInputDeltaEvent struct {
	ID    string
	Delta string
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
}

func (ToolInputDeltaEvent) isStreamEvent() {}

// ToolInputCompleteEvent signals that a tool call's arguments have finished streaming
type ToolInputCompleteEvent struct {
	ID    string
	Input json.RawMessage
	// Key identifies the content block; see TextDeltaEvent.Key.
	Key string
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
