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
		WithSchema(llm.GenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

func executeWriteFile(_ context.Context, args WriteFileInput) (llm.ToolResultOutput, error) {
	if args.Path == "" {
		return llm.NewTextErrorResponse("path is required"), nil
	}
	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	return llm.NewTextResponse("File written successfully"), nil
}
```

## Before vs After

### Before (70+ lines with manual schema)

```go
func NewWriteFileTool() llm.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The path of the file to write"
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file"
			}
		},
		"required": ["path", "content"]
	}`)

	return llm.NewTool("write_file", "...").
		WithSchema(schema).
		WithExecute(func(_ context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var args WriteFileInput
			if err := json.Unmarshal(input, &args); err != nil {
				return llm.NewTextErrorResponse("failed to parse input: " + err.Error()), nil
			}

			if args.Path == "" {
				return llm.NewTextErrorResponse("path is required"), nil
			}

			if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
				return llm.NewTextErrorResponse(err.Error()), nil
			}

			return llm.NewTextResponse("File written successfully"), nil
		}).
		Build()
}
```

### After (30 lines — 57% reduction)

See the Pattern Overview above.

## Schema Tag Syntax

```go
type Example struct {
	Name     string `json:"name" jsonschema:"required,description=The name"`
	Type     string `json:"type" jsonschema:"required,description=The type,enum=foo|bar|baz"`
	Optional string `json:"optional" jsonschema:"description=This is optional"`
}
```

| Tag | Description |
|-----|-------------|
| `required` | Field is required in the JSON schema |
| `description=...` | Field description for the LLM |
| `type=...` | Override the JSON type (defaults to `string`) |
| `enum=...` | Pipe-separated allowed values |

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
		WithSchema(llm.GenerateSchema(MyInput{})).
		WithExecute(llm.TypedExecute(executeMyTool)).
		Build()
}

func executeMyTool(_ context.Context, args MyInput) (llm.ToolResultOutput, error) {
	// Just the business logic
}
```

For tools needing closure variables:

```go
func NewMyTool(dep *Dependency) llm.Tool {
	return llm.NewTool("name", "description").
		WithSchema(llm.GenerateSchema(MyInput{})).
		WithExecute(llm.TypedExecute(func(_ context.Context, args MyInput) (llm.ToolResultOutput, error) {
			// Can use dep here
		})).
		Build()
}
```

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/llm/helpers.go` | Message constructors and tool builder |
| `internal/llm/schema.go` | `GenerateSchema()` — reads struct tags, produces JSON schema |
| `internal/llm/typed.go` | `TypedExecute[T]()` — generic unmarshaling + execution wrapper |
| `internal/llm/schema_test.go` | Tests for schema generator |

All five built-in tools use this pattern:

| Tool | File | Lines |
|------|------|-------|
| `read_file` | `internal/tools/read_file.go` | ~100 |
| `edit_file` | `internal/tools/edit_file.go` | ~88 |
| `write_file` | `internal/tools/write_file.go` | ~39 |
| `shell` | `internal/tools/shell.go` | ~84 |
| `activate_skill` | `internal/tools/activate_skill.go` | ~30 |
