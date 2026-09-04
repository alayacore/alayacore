# Tool Input Repair

AlayaCore includes a **tool input repair layer** that automatically fixes
common JSON output errors produced by LLMs before the tool input reaches
execution or is persisted to history.

## Motivation

Several popular LLMs (DeepSeek, GLM, Qwen) produce structurally valid JSON
that nevertheless deviates from the tool's JSON Schema in predictable ways.
These deviations cause tool execution failures, API errors on history replay,
and inconsistent persisted state.

Rather than patching each provider or each tool individually, a centralized
repair function (`RepairToolInput`) intercepts tool inputs at the agent level
and corrects them against the tool's declared schema.

## Why These Errors Occur

These 4 patterns share a common root cause: **structural amnesia around
JSON's type system**. The model serializes content correctly at the value
level (the right data is there), but defaults to the simplest possible JSON
construct at the container level — string over array, null over omission,
scalar over collection.

They are **generation-order artifacts**, not knowledge gaps:

```
Token generation for {"files": ["a", "b"]}:

Position 1:  {"files":
Position 2:  {"files":           ← model must decide: string token or array token?
Position 3:  {"files": [         ← if it chose array, great
             {"files": "         ← if it chose string, already wrong
```

At position 2 the model has just finished the key `"files"`. Its first
instinct is to produce a string (the most common JSON value type). By the
time it realizes the schema demands an array, it has already committed to
a `"` token. The semantic content is correct; the structural wrapper is
an afterthought.

### Relationship with model size

The relationship is **non-monotonic** — bigger does not simply mean fewer
errors. Models can be classified by a three-level error hierarchy:

| Level | Description | Typical model size | These 4 patterns |
|-------|-------------|-------------------|-----------------|
| **3** | Invalid JSON (broken brackets, unquoted strings) | < 3B parameters | Overshadowed by syntax failures |
| **2** | Valid JSON but schema-violating in basic ways | 3B–14B | **Peak frequency** — model is good enough for valid JSON but not for correct nesting |
| **1** | Valid JSON, schema violations at the container/type level | 14B–70B | Most visible — syntax is clean but these 4 patterns persist |
| **0** | Schema-compliant JSON | 70B+ (frontier) | Decrease significantly, appear under streaming pressure or with complex nested schemas |

These 4 errors are **most characteristic of the 14B–70B band**: the model
has learned to produce syntactically flawless JSON, but hasn't fully
internalized the schema's structural requirements. They persist even in
frontier models under time pressure (early streaming tokens), when
distracted by reasoning, or when the schema involves deeply nested arrays
(e.g. MCP-style tools).

## The Four Error Patterns

### 1. null for optional fields

The model sends `null` for an optional field instead of omitting it entirely.

```json
// Schema: "start_line" is optional (not in "required")
// Model sends:
{"path": "/foo", "start_line": null}
// Repaired:
{"path": "/foo"}
```

### 2. JSON-stringified array

The model sends a JSON string whose contents are a serialized JSON array,
instead of sending a real JSON array.

```json
// Schema: "files" is "array" of "string"
// Model sends:
{"files": "[\"a\", \"b\", \"c\"]"}
// Repaired:
{"files": ["a", "b", "c"]}
```

### 3. Bare object where array expected / empty placeholder

The model sends a bare object where the schema expects an array, or includes
empty object placeholders (`{}`) inside an array.

```json
// Schema: "tools" is "array" of objects with required field "name"
// Model sends (bare object):
{"tools": {"name": "read_file", "arguments": {"path": "/foo"}}}
// Repaired:
{"tools": [{"name": "read_file", "arguments": {"path": "/foo"}}]}

// Model sends (empty placeholder in array):
{"tools": [{"name": "read_file", ...}, {}]}
// Repaired (empty object removed when item schema has required fields):
{"tools": [{"name": "read_file", ...}]}
```

### 4. Bare string where array expected

The model sends a plain string where the schema expects an array of strings.

```json
// Schema: "files" is "array" of "string"
// Model sends:
{"files": "README.md"}
// Repaired:
{"files": ["README.md"]}
```

## Architecture

```
LLM Output (SSE chunks)
    │
    ▼
Provider streaming parser (e.g. openAIStreamState)
    │  accumulates partial JSON in tool accumulators
    ▼
ToolInputCompleteEvent
    │  e.Input = raw accumulated JSON
    │
    ├── RepairToolInput(e.Input, toolSchema)
    │       │
    │       ├── parseSchema(schema)     → simplified schema tree
    │       ├── repairObject(input, schema) → recursively fix
    │       └── return fixed JSON
    │
    ├── runner.add()                    → tool receives repaired input
    │   (toolRunner: parallel launches  (unfixable bytes fail parse → UF ✗)
    │    here, serial queues until
    │    finish) — the repaired input is
    │    what runs either way)
    │
    └── OnToolInputComplete()           → streaming callback with repaired input
    │
    ▼
attachToolResults (record from the assembler)
    │  independent ToolInputParts from provider
    │
    └── repairToolInputsInContents()    → history gets repaired input
        ↓
    OnStepFinish()                      → persisted (unfixable bytes kept as-is)
```

Repair is applied at **two points** in the streaming pipeline, because the
`ToolInputCompleteEvent` and `StepCompleteEvent` carry **different object
instances** — the former delivers the input for immediate tool execution,
the latter assembles fresh content parts for history persistence.

## The repair function

`RepairToolInput` in `internal/llm/toolinput_repair.go` is the core function.

```go
func RepairToolInput(input json.RawMessage, schema json.RawMessage) json.RawMessage
```

It returns the original `input` unchanged if:
- The schema has no `type` field (treated as "any type")
- The root type is not `"object"`
- The input is not valid JSON
- No repairs were needed

### Schema parser

The schema parser (`parseSchema`) builds a simplified tree containing only
the fields needed for repair:

| Schema field | Used for |
|-------------|----------|
| `type` | Determine expected value type (object, array, primitive) |
| `properties` | Walk nested objects recursively |
| `items` | Determine array element schema |
| `required` | Distinguish optional null (→ remove) from required null (→ keep) |

It uses only the standard library (`encoding/json`) and intentionally does
not attempt to handle the full JSON Schema specification (no `$ref`,
`anyOf`, `oneOf`, `patternProperties`, etc.). This is sufficient because:

- Built-in tool schemas are generated by `GenerateSchema` (flat structs only)
- MCP tool schemas are validated by `sanitizeInputSchema` (no external `$ref`)

### Idempotency

`RepairToolInput` is **idempotent**: applying it twice produces the same
result as applying it once. This is guaranteed because each repair rule
only converts a value from "wrong form" to "correct form" — never back.

## Integration points

| File | What changed |
|------|-------------|
| `internal/llm/toolinput_repair.go` | Core repair logic + schema parser |
| `internal/llm/toolinput_repair_test.go` | ~50 test cases covering all patterns |
| `internal/llm/agent.go` | Calls `repairToolInput` in `streamEvents` at two points |

## Relationship to existing fixes

The repair layer is **complementary** to, not a replacement for, the existing
scattered fixes in the provider code:

| Existing fix | Layer | Relationship |
|-------------|-------|-------------|
| `openAIStreamState.appendToolCallArgs` null skip | Streaming transport | Prevents chunk-level corruption; repair layer works on final JSON |
| `openAIStreamState.appendToolCallArgs` string unquoting | Streaming transport | Same — different abstraction level |
| `streamAssembler` | Content array structure | Records only blocks that streamed content, so an empty reasoning/text slot never becomes a part; unrelated to tool input JSON |
| `openaiConvertToolInputs` JSON-string wrapping | API compatibility | OpenAI wire format requirement, not a model-error fix |
| `marshalToolInputData` prefix-marked fallback | Serialization | Catches what repair cannot touch — bytes that fail to unmarshal at all. Prefix-marks them so the AF frame is still delivered and the tool fails parsing (UF ✗) instead of the frame never being sent; see `internal/agent/serialization.go` |
