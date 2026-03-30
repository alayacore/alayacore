// Package llm provides a custom LLM client with streaming support
package llm

import (
	"context"
	"encoding/json"
)

// MessageRole represents the role of a message
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ContentPart represents a part of message content
type ContentPart interface {
	isContentPart()
}

// TextPart represents text content
type TextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextPart) isContentPart() {}

// ReasoningPart represents reasoning/thinking content
type ReasoningPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (ReasoningPart) isContentPart() {}

// ToolCallPart represents a tool call
type ToolCallPart struct {
	Type       string          `json:"type"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Input      json.RawMessage `json:"input"`
}

func (ToolCallPart) isContentPart() {}

// ToolResultPart represents a tool execution result
type ToolResultPart struct {
	Type       string           `json:"type"`
	ToolCallID string           `json:"tool_call_id"`
	Output     ToolResultOutput `json:"output"`
}

func (ToolResultPart) isContentPart() {}

// ToolResultOutput represents the output of a tool
type ToolResultOutput interface {
	isToolResultOutput()
}

// ToolResultOutputText represents text output
type ToolResultOutputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (ToolResultOutputText) isToolResultOutput() {}

// ToolResultOutputError represents error output
type ToolResultOutputError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

func (ToolResultOutputError) isToolResultOutput() {}

// Message represents a single message in the conversation
type Message struct {
	Role    MessageRole   `json:"role"`
	Content []ContentPart `json:"content"`
}

// ToolDefinition defines a tool that can be called
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Usage tracks token usage
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens,omitempty"`
}

// StreamEvent represents a streaming event
type StreamEvent interface {
	isStreamEvent()
}

// TextDeltaEvent represents text content streaming
type TextDeltaEvent struct {
	Delta string
}

func (TextDeltaEvent) isStreamEvent() {}

// ReasoningDeltaEvent represents reasoning content streaming
type ReasoningDeltaEvent struct {
	Delta string
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ToolCallEvent represents a tool call
type ToolCallEvent struct {
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
}

func (ToolCallEvent) isStreamEvent() {}

// StepCompleteEvent represents completion of an agentic step
type StepCompleteEvent struct {
	Messages []Message
	Usage    Usage
}

func (StepCompleteEvent) isStreamEvent() {}

// StreamErrorEvent represents an error during streaming
type StreamErrorEvent struct {
	Error error
}

func (StreamErrorEvent) isStreamEvent() {}

// Provider defines the interface for LLM providers
type Provider interface {
	// StreamMessages streams a conversation with tools
	// systemPrompt is the base system prompt (always present)
	// extraSystemPrompt is the user-provided additional system prompt (optional, from --system flag)
	// The provider should merge them appropriately (joined by "\n\n" if both present)
	StreamMessages(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		systemPrompt string,
		extraSystemPrompt string,
	) (<-chan StreamEvent, error)
}
