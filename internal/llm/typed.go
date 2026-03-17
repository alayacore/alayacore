package llm

import (
	"context"
	"encoding/json"
)

// TypedExecuteFunc is a type-safe tool execution function
type TypedExecuteFunc[T any] func(ctx context.Context, args T) (ToolResultOutput, error)

// TypedExecute wraps a type-safe function to work with raw JSON
// Note: It only handles JSON unmarshaling. Tools should validate their own invariants.
func TypedExecute[T any](fn TypedExecuteFunc[T]) func(context.Context, json.RawMessage) (ToolResultOutput, error) {
	return func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error) {
		var args T
		if err := json.Unmarshal(input, &args); err != nil {
			return NewTextErrorResponse("failed to parse input: " + err.Error()), nil
		}
		return fn(ctx, args)
	}
}
