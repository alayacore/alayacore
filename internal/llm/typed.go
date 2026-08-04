package llm

import (
	"context"
	"encoding/json"
)

// TypedExecuteFunc is a type-safe tool execution function
type TypedExecuteFunc[T any] func(ctx context.Context, args T) ([]ContentPart, error)

// TypedExecute wraps a type-safe function to work with raw JSON
// Note: It only handles JSON unmarshaling. Tools should validate their own invariants.
func TypedExecute[T any](fn TypedExecuteFunc[T]) func(context.Context, json.RawMessage) ([]ContentPart, error) {
	return func(ctx context.Context, input json.RawMessage) ([]ContentPart, error) {
		var args T
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return fn(ctx, args)
	}
}

// TypedExecuteStreamingFunc is a type-safe streaming tool execution function.
// onDelta delivers ephemeral preview snapshots for display only.
type TypedExecuteStreamingFunc[T any] func(ctx context.Context, args T, onDelta func(text string)) ([]ContentPart, error)

// TypedExecuteStreaming wraps a type-safe streaming function to work with
// raw JSON. See TypedExecute for unmarshal semantics.
func TypedExecuteStreaming[T any](fn TypedExecuteStreamingFunc[T]) func(context.Context, json.RawMessage, func(string)) ([]ContentPart, error) {
	return func(ctx context.Context, input json.RawMessage, onDelta func(string)) ([]ContentPart, error) {
		var args T
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return fn(ctx, args, onDelta)
	}
}
