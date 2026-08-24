# Provider Wire Format → Domain Type Mapping

How OpenAI and Anthropic API wire formats map to the domain types in `llm/types.go`.

## Domain Layer Overview

All providers eat different wire formats and emit the same domain types:

```go
// llm/types.go — the domain types

// ContentPart is implemented by all content block types.
// Each implementation embeds ContentPartMeta for HistoryID and Role.
type ContentPart interface {
	GetHistoryID() uint64
	SetHistoryID(uint64)
	GetRole() MessageRole
	SetRole(MessageRole)
	UpdateContentPartMeta(historyID uint64, role MessageRole)
}

// Implementations:
type TextPart       struct { ContentPartMeta; Text string }
type ReasoningPart  struct { ContentPartMeta; Text string }
type ImagePart      struct { ContentPartMeta; URI string }
type AudioPart      struct { ContentPartMeta; URI string }
type VideoPart      struct { ContentPartMeta; URI string }
type DocumentPart   struct { ContentPartMeta; URI string }
type ToolInputPart  struct { ContentPartMeta; ID string; Name string; Input json.RawMessage }
type ToolOutputPart struct { ContentPartMeta; ID string; Output []ContentPart; IsError bool }

// Messages are represented as flat []ContentPart slices.
// There is no Message wrapper struct — role and history ID are stored
// on each ContentPart via ContentPartMeta.

type StreamEvent interface {
	isStreamEvent()
}
```

## Design: Domain Layer Models Anthropic, Not OpenAI

The domain layer `[]ContentPart` is practically a **generic version** of Anthropic's `[]anthropicContentBlock` array, **not** OpenAI's flat-field model.

Compare the three representations for the same assistant message:

```go
// Domain (llm/types.go) — flat array of ContentPart interfaces
[]ContentPart{
	&ReasoningPart{Text:"Let me think...", ContentPartMeta: {Role: "assistant"}},
	&TextPart{Text:"The answer is 42", ContentPartMeta: {Role: "assistant"}},
	&ToolInputPart{ID:"call_abc", Name:"read_file", Input: json.RawMessage(`{"path":"/tmp/foo"}`), ContentPartMeta: {Role: "assistant"}},
}
```

```go
// Anthropic wire (anthropic.go) — array of concrete blocks, nearly 1:1
anthropicMessage{
	Role: "assistant",
	Content: []anthropicContentBlock{
		{Type:"thinking", Thinking: &"Let me think..."},
		{Type:"text", Text: "The answer is 42"},
		{Type:"tool_use", ID:"call_abc", Name:"read_file", Input: {"path":"/tmp/foo"}},
	},
}
```

```go
// OpenAI wire (openai.go) — THREE separate top-level fields
openAIMessage{
	Role:             "assistant",
	ReasoningContent: &"Let me think...",
	Content:          "The answer is 42",
	ToolCalls: []openAIToolCall{
		{ID:"call_abc", Function: {Name:"read_file", Arguments:"{\"path\":\"/tmp/foo\"}"}},
	},
}
```

### Why this matters: Adapter complexity

The Anthropic adapter is a **direct 1:1 mapping** — each `ContentPart` becomes one `anthropicContentBlock`:

```go
// Anthropic — simple type switch, one block per ContentPart
for _, part := range msg.Contents {
	switch v := part.(type) {
	case llm.TextPart:
		→ {Type:"text", Text: v.Text}
	case llm.ReasoningPart:
		→ {Type:"thinking", Thinking: &v.Text}
	case llm.ToolInputPart:
		→ {Type:"tool_use", ID: v.ID, Name: v.Name, Input: v.Input}
	case llm.ToolOutputPart:
		→ {Type:"tool_result", ToolCallID: v.ID, Output: [...], IsError: v.IsError}
	// Output is an array of content blocks (text, image, etc.)
	// Single text block uses string shorthand for backward compat
	}
}
```

The OpenAI adapter must **split** a single `[]ContentPart` across three independent fields:

```go
// OpenAI — must distribute ContentParts into separate wire fields
apiMsg.Content = ...          // only TextParts go here
apiMsg.ReasoningContent = ... // only ReasoningParts go here
apiMsg.ToolCalls = ...        // only ToolInputParts go here
// ToolOutputParts become entirely separate messages with role="tool"
```

And on receive, both providers use the same pattern: accumulate content by `index` across streaming chunks, then assemble into a single `[]ContentPart` at step completion. OpenAI accumulates three parallel fields (reasoning, text, tool arguments) by index; Anthropic accumulates content blocks by index — structurally the same approach.

**Conclusion:** The domain layer was clearly inspired by Anthropic's content block array model. It's the more general and extensible design — adding a new content type just means adding a new `ContentPart` implementation and a new case in each provider's switch statement. OpenAI's flat-field model is the odd one out requiring non-trivial split/merge logic.

## Wire Format Comparison

| Domain Type | OpenAI Wire | Anthropic Wire |
|---|---|---|
| `TextPart` | `content` (top-level field) | `content[]` array: `{type:"text", text:"..."}` |
| `ReasoningPart` | `reasoning_content` (top-level field) | `content[]` array: `{type:"thinking", thinking:"..."}` |
| `ImagePart` | `content[]` array: `{type:"image_url", image_url:{url:"data:image/...;base64,..."}}` | `content[]` array: `{type:"image", source:{type:"base64", media_type:"image/jpeg", data:"..."}}` |
| `AudioPart` | `content[]` array: `{type:"input_audio", input_audio:{data:"UklGRiQ...", format:"wav"}}` | `content[]` array: `{type:"audio", source:{type:"base64", media_type:"audio/mpeg", data:"..."}}` |
| `VideoPart` | `content[]` array: `{type:"video_url", video_url:{url:"data:video/...;base64,..."}, fps:2, media_resolution:"default"}` | `content[]` array: `{type:"video", source:{type:"base64", media_type:"video/mp4", data:"..."}}` |
| `DocumentPart` | ❌ Not supported | `content[]` array: `{type:"document", source:{type:"base64", media_type:"application/pdf", data:"..."}}` |
| `ToolInputPart` | `tool_calls[]` (top-level array) | `content[]` array: `{type:"tool_use", id, name, input}` |
| `ToolOutputPart` | Separate message: `{role:"tool", tool_call_id, content}` (content is JSON-wrapped with `"status"` field — see note below) | `content[]` array: `{type:"tool_result", tool_use_id, content, is_error}`, **role remapped to "user"**. `content` can be a string or an array of content blocks (text, image, etc.) |

> **Note on OpenAI tool result content format:** OpenAI's API has no native `is_error` field for tool results (unlike Anthropic). To prevent ambiguity — e.g., a tool returning `"no such file"` as an error vs. a file containing the literal text `"no such file"` — the OpenAI provider wraps tool results as JSON:
>
> - Success: `{"status":"success","data":"<plain text output>"}`
> - Error:   `{"status":"error","data":"<error message>"}`
>
> This ensures the model can distinguish success from failure structurally rather than guessing from the content string. The Anthropic provider uses the native `is_error: true` flag instead, so results remain unwrapped plain text.

## Receiving (Wire → Domain)

### Example 1: Reasoning only

**OpenAI wire:**
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
Chunk 2: {"choices":[{"delta":{"reasoning_content":" about this"}}]}
```

**Anthropic wire:**
```
event: content_block_start / {"type":"thinking","thinking":""}
event: content_block_delta / {"delta":{"type":"thinking_delta","thinking":"Let me think..."}}
event: content_block_delta / {"delta":{"type":"thinking_delta","thinking":" about this"}}
event: content_block_stop
```

**Domain output (same for both):**
```go
// Stream events:
ReasoningDeltaEvent{Delta: "Let me think..."}
ReasoningDeltaEvent{Delta: " about this"}

// Final content (flat []ContentPart — no Message wrapper):
[]ContentPart{
	&ReasoningPart{
		Text: "Let me think... about this",
	},
}
```

### Example 2: Tool calls only

**OpenAI wire** (args arrive as chunks linked by `index`):
```
Chunk 1: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"id":"call_abc","function":{"name":"read_file"}}
]}}]}

Chunk 2: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"{\"path\":"}}
]}}]}

Chunk 3: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"\"/tmp/foo\""}}
]}}]}

Chunk 4: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"}"}}
]}}]}
```

**Anthropic wire** (tool call is a block lifecycle):
```
event: content_block_start / {"type":"tool_use","id":"toolu_abc","name":"read_file"}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"\"/tmp/foo\""}}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"}"}}
event: content_block_stop
```

**Domain output (same for both):**
```go
// Stream event at name arrival:
ToolInputStartEvent{ID: "call_abc", Name: "read_file"}

// Stream event at completion (after all args received):
ToolInputPart{
	ID:    "call_abc",
	Name:  "read_file",
	Input: json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final content (flat []ContentPart — no Message wrapper):
[]ContentPart{
	&ToolInputPart{
		ID:    "call_abc",
		Name:  "read_file",
		Input: json.RawMessage(`{"path":"/tmp/foo"}`),
	},
}
```

### Example 3: Reasoning + Tool calls (mixed in same message)

**OpenAI wire** (both fields arrive interleaved, accumulated separately):
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Read file"}}]}
Chunk 2: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"id":"call_abc","function":{"name":"read_file"}}
]}}]}
Chunk 3: {"choices":[{"delta":{"reasoning_content":" to check"}}]}
Chunk 4: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"{\"path\":"}}
]}}]}
Chunk 5: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"\"/tmp/foo\""}}
]}}]}
Chunk 6: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"}"}}
]}}]}
```

**Domain output:**
```go
// Interleaved stream events:
ReasoningDeltaEvent{Delta: "Read file"}
ToolInputStartEvent{ID: "call_abc", Name: "read_file"}
ReasoningDeltaEvent{Delta: " to check"}
// (no more ReasoningDelta or ToolInputStart — just args accumulating)

ToolInputPart{
	ID:    "call_abc",
	Name:  "read_file",
	Input: json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final content — both parts in flat []ContentPart:
[]ContentPart{
	&ReasoningPart{Text: "Read file to check"},
	&ToolInputPart{ID: "call_abc", Name: "read_file",
		Input: json.RawMessage(`{"path":"/tmp/foo"}`)
	},
}
```

**Why this works:** The `openAIStreamState` has a single `toolAccumulators[index]` per tool call, storing both metadata and argument fragments. Reasoning text accumulates independently in `reasoningBuilder`. They never interfere. `getContents()` simply appends all non-empty accumulators to the Content slice.

## Sending (Domain → Wire)

### Example 4: Message with reasoning + tool calls

**Domain input:**
```go
// Flat []ContentPart — there is no Message wrapper struct
[]ContentPart{
	&ReasoningPart{Text: "Let me read the file"},
	&ToolInputPart{
		ID:    "call_abc",
		Name:  "read_file",
		Input: json.RawMessage(`{"path":"/tmp/foo"}`),
	},
}
```

**OpenAI wire output** (flat fields):
```json
{
    "role": "assistant",
    "reasoning_content": "Let me read the file",
    "tool_calls": [{
        "id": "call_abc",
        "type": "function",
        "function": {
            "name": "read_file",
            "arguments": "{\"path\":\"/tmp/foo\"}"
        }
    }]
}
```

**Anthropic wire output** (array of content blocks):
```json
{
    "role": "assistant",
    "content": [
        {"type": "thinking", "thinking": "Let me read the file"},
        {"type": "tool_use", "id": "call_abc", "name": "read_file",
         "input": {"path": "/tmp/foo"}}
    ]
}
```

### Example 5: Multimodal user message (image + audio + video)

**Domain input:**
```go
// Flat []ContentPart — there is no Message wrapper struct
[]ContentPart{
	&TextPart{Text: "Describe this multimedia"},
	&ImagePart{URI: "data:image/jpeg;base64,/9j/4AAQ..."},
	&AudioPart{URI: "data:audio/wav;base64,UklGR..."},
	&VideoPart{URI: "data:video/mp4;base64,AAAA..."},
}
```

**OpenAI wire output** (content array with typed blocks):
```json
{
    "role": "user",
    "content": [
        {"type": "text", "text": "Describe this multimedia"},
        {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,/9j/4AAQ..."}},
        {"type": "input_audio", "input_audio": {"data": "UklGRiQ...", "format": "wav"}},
        {"type": "video_url", "video_url": {"url": "data:video/mp4;base64,AAAA..."},
         "fps": 2, "media_resolution": "default"}
    ]
}
```

**Anthropic wire output** (content array with source blocks):
```json
{
    "role": "user",
    "content": [
        {"type": "text", "text": "Describe this multimedia"},
        {"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "/9j/4AAQ..."}},
        {"type": "audio", "source": {"type": "base64", "media_type": "audio/wav", "data": "UklGR..."}},
        {"type": "video", "source": {"type": "base64", "media_type": "video/mp4", "data": "AAAA..."}}
    ]
}
```

> **Note:** All media content parts store a URI (`data:{mime};base64,...` or `https://...`) in the domain layer. Each provider extracts or passes through the format it needs:
> - OpenAI `image_url` / `video_url`: passes the URI directly as the `url` field
> - OpenAI `input_audio`: parses the data URI to extract raw base64 `data` and `format` from the MIME type; remote URLs are not supported and replaced with a text placeholder
> - Anthropic: parses data URIs to extract `media_type` and raw base64 `data`; plain URLs use the `url` source type

### Wire Format Differences (Anthropic vs OpenAI)

| Aspect | OpenAI | Anthropic |
|---|---|---|
| **Message structure** | Flat fields (`content`, `reasoning_content`, `tool_calls` at top level) | Content is always `[]anthropicContentBlock` array |
| **Tool result role** | `"tool"` | Remapped to `"user"` |
| **Tool call args encoding** | Double-encoded JSON string (`json.Marshal(string(rawMsg))`) | Raw JSON object (`json.RawMessage` directly) |
| **Empty reasoning when reasoning mode is on** | Sets `"reasoning_content": ""` (string pointer) | Prepends `{"type":"thinking","thinking":""}` to content array |
| **SSE event format** | Data-only lines, `[DONE]` terminator | Named events (`message_start`, `content_block_start`, etc.) |
| **Tool call arg chunks** | Linked by `index` field across multiple deltas | Grouped by block lifecycle (start → delta → stop) |

## Stream State Machines

### OpenAI: Parallel accumulators

```
openAIStreamState {
	textBuilder       strings.Builder                ← "content" delta chunks
	reasoningBuilder  strings.Builder                ← "reasoning_content" delta chunks
	toolAccumulators  map[int]*openAIToolAccumulator ← tool calls keyed by index
}

openAIToolAccumulator {
	id   string          ← tool call id
	name string          ← function name
	args strings.Builder ← accumulated arguments fragments
}
```

All three accumulate simultaneously during streaming. At `StepCompleteEvent`, they merge into a single `[]ContentPart` slice.

### Anthropic: Indexed block accumulator (like OpenAI)

```
blockAccumulator {
	blockType string              // "text" | "thinking" | "tool_use"
	buffer    strings.Builder     // text, thinking, or tool_use partial_json
	id, name string
}

anthropicStreamState {
	contentParts  map[int]ContentPart          ← finished blocks by index
	blocks        map[int]*blockAccumulator    ← in-progress blocks by index
}
```

Every wire event carries an `index` (start, delta, stop), just like OpenAI's `tool_calls[index]`. Blocks may arrive interleaved — block 1 can start before block 0 finishes. Each block is independently accumulated by index. `content_block_stop(i)` stores the result in `contentParts[i]`, and `getContents()` sorts by index to produce the final ordered slice.
