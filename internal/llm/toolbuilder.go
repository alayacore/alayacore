package llm

import (
	"context"
	"encoding/json"
)

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

func (b *ToolBuilder) WithSchema(schema json.RawMessage) *ToolBuilder {
	b.tool.Definition.Schema = schema
	return b
}

func (b *ToolBuilder) WithExecute(fn func(ctx context.Context, input json.RawMessage) ([]ContentPart, error)) *ToolBuilder {
	b.tool.Execute = fn
	return b
}

// WithExecuteStreaming registers an optional streaming executor.
// The onDelta callback delivers ephemeral result preview snapshots
// for display only; the returned []ContentPart is always authoritative.
func (b *ToolBuilder) WithExecuteStreaming(fn func(ctx context.Context, input json.RawMessage, onDelta func(text string)) ([]ContentPart, error)) *ToolBuilder {
	b.tool.ExecuteStreaming = fn
	return b
}

func (b *ToolBuilder) Build() Tool {
	return b.tool
}
