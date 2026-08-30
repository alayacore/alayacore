# Tool Definition: Type-Safe Tools with Auto-Generated Schemas

How AlayaCore defines tools using type-safe Go patterns with auto-generated JSON schemas, eliminating manual schema JSON and boilerplate.

## Pattern Overview

Instead of writing raw JSON schemas and manual unmarshaling, each tool defines a Go struct with `jsonschema` tags. A generic `TypedExecute` wrapper handles unmarshaling automatically:

```go
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"required,description=The path of the file to write"`
	Content string `json:"content" jsonschema:"required,description=The content to write to the file"`
}

func NewWriteFileTool() llm.Tool {
	return llm.NewTool("write_file", "Write content to a file").
		WithSchema(llm.MustGenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

func executeWriteFile(_ context.Context, args WriteFileInput) ([]llm.ContentPart, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	// Empty content is valid: it truncates the file. (Rejecting it left the
	// model with no way to empty a file.) The write itself is atomic —
	// resolveWriteTarget, os.CreateTemp beside the target, writeAndClose, then
	// rename — so an interrupted write cannot truncate the original, and the
	// mode is preserved (or, for a new file, newFilePerm(umask)).
	if err := writeAtomically(args.Path, args.Content); err != nil {
		return nil, err
	}
	return []llm.ContentPart{&llm.TextPart{Text: "File written successfully"}}, nil
}
```

> The body above is condensed: the real `write_file.go` inlines the
> create/write/rename sequence in `executeWriteFile`. This document is about
> the schema + `TypedExecute` pattern, not the write strategy.

## Before vs After

The old approach required writing raw JSON schemas and manual `json.Unmarshal` in every tool function. The new approach uses struct tags and `TypedExecute` to eliminate both. See the Pattern Overview above for the current style — the tool definition is ~30 lines instead of 70+, with compile-time type safety and no manual JSON handling.

## Schema Tag Syntax

```go
type Example struct {
	Name     string `json:"name" jsonschema:"required,description=The name"`
	Count    int    `json:"count" jsonschema:"description=The count"`
	Rate     float64 `json:"rate" jsonschema:"description=Rate per second"`
	Enabled  bool   `json:"enabled" jsonschema:"description=Whether enabled"`
	Type     string `json:"type" jsonschema:"required,description=The type,enum=foo|bar|baz"`
	Optional string `json:"optional" jsonschema:"description=This is optional"`
}
```

| Tag | Description |
|-----|-------------|
| `required` | Field is required in the JSON schema |
| `description=...` | Field description for the LLM |
| `type=...` | Override the inferred type (rarely needed) |
| `enum=...` | Pipe-separated allowed values |

### Type Inference

JSON schema types are **automatically inferred** from Go field types:

| Go type | JSON schema type |
|---------|------------------|
| `string` | `"string"` |
| `int`, `int8`, `int16`, `int32`, `int64` | `"integer"` |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64` | `"integer"` |
| `float32`, `float64` | `"number"` |
| `bool` | `"boolean"` |

No `type=` tag is needed — just use the appropriate Go type and the schema generator handles it.

## Benefits

1. **Single source of truth** — Schema defined once via struct tags
2. **Type-safe** — Compile-time checking of input types
3. **Less boilerplate** — ~50-60% less code per tool
4. **Easier to maintain** — Add a field = add one line with tags
5. **Better separation** — Tool definition vs. execution logic
6. **Testable** — Execute functions can be tested independently

## Pattern Guide

For simple tools:

```go
func NewMyTool() llm.Tool {
	return llm.NewTool("name", "description").
		WithSchema(llm.MustGenerateSchema(MyInput{})).
		WithExecute(llm.TypedExecute(executeMyTool)).
		Build()
}

func executeMyTool(_ context.Context, args MyInput) ([]llm.ContentPart, error) {
	// Just the business logic
}
```

For tools needing closure variables:

```go
func NewMyTool(dep *Dependency) llm.Tool {
	return llm.NewTool("name", "description").
		WithSchema(llm.MustGenerateSchema(MyInput{})).
		WithExecute(llm.TypedExecute(func(_ context.Context, args MyInput) ([]llm.ContentPart, error) {
			// Can use dep here
		})).
		Build()
}
```

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/llm/toolbuilder.go` | `ToolBuilder` — fluent tool definition construction (`NewTool`, `WithSchema`, `WithExecute`, `Build`) |
| `internal/llm/provider_helpers.go` | `GroupByRole`, `ParseDataURI` — shared helpers used by both providers |
| `internal/llm/schema.go` | `MustGenerateSchema()` — reads struct tags, produces JSON schema |
| `internal/llm/typed.go` | `TypedExecute[T]()` — generic unmarshaling + execution wrapper |
| `internal/llm/schema_test.go` | Tests for schema generator |

All five built-in tools use this pattern:

| Tool | File | Lines |
|------|------|-------|
| `read_file` | `internal/tools/read_file.go` | ~180 |
| `edit_file` | `internal/tools/edit_file.go` | ~632 (includes whitespace-tolerant fallback) |
| `write_file` | `internal/tools/write_file.go` | ~39 |
| `execute_command` | `internal/tools/execute_command.go` | ~364 |
| `search_content` | `internal/tools/search_content.go` | ~211 |

The `execute_command` tool delegates platform-specific logic to the `internal/tools/shell/` package, which handles shell detection and command execution across Unix and Windows. See [architecture.md](../architecture.md) for details.
