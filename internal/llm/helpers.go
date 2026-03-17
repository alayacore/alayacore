package llm

import (
	"context"
	"encoding/json"
)

// NewSystemMessage creates a system message
func NewSystemMessage(text string) Message {
	return Message{
		Role: RoleSystem,
		Content: []ContentPart{
			TextPart{Type: "text", Text: text},
		},
	}
}

// NewUserMessage creates a user message
func NewUserMessage(text string) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentPart{
			TextPart{Type: "text", Text: text},
		},
	}
}

// NewAssistantMessage creates an assistant message
func NewAssistantMessage(parts []ContentPart) Message {
	return Message{
		Role:    RoleAssistant,
		Content: parts,
	}
}

// NewToolResultMessage creates a tool result message
func NewToolResultMessage(toolCallID string, output ToolResultOutput) Message {
	return Message{
		Role: RoleTool,
		Content: []ContentPart{
			ToolResultPart{
				Type:       "tool_result",
				ToolCallID: toolCallID,
				Output:     output,
			},
		},
	}
}

// NewTextResponse creates a text tool response
func NewTextResponse(text string) ToolResultOutput {
	return ToolResultOutputText{
		Type: "text",
		Text: text,
	}
}

// NewTextErrorResponse creates an error tool response
func NewTextErrorResponse(errMsg string) ToolResultOutput {
	return ToolResultOutputError{
		Type:  "error",
		Error: errMsg,
	}
}

// ToolBuilder helps build tool definitions
type ToolBuilder struct {
	tool Tool
}

// NewTool creates a new tool builder
func NewTool(name, description string) *ToolBuilder {
	return &ToolBuilder{
		tool: Tool{
			Definition: ToolDefinition{
				Name:        name,
				Description: description,
			},
		},
	}
}

// WithSchema sets the tool schema
func (b *ToolBuilder) WithSchema(schema json.RawMessage) *ToolBuilder {
	b.tool.Definition.Schema = schema
	return b
}

// WithExecute sets the execute function
func (b *ToolBuilder) WithExecute(fn func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error)) *ToolBuilder {
	b.tool.Execute = fn
	return b
}

// Build returns the tool
func (b *ToolBuilder) Build() Tool {
	return b.tool
}
