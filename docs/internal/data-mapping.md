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
// on each ContentPart via ContentPartMeta. These are in-memory fields
// (all three are `json:"-"`): the session file stores no history ID, and
// loading re-issues IDs sequentially in file order. See architecture.md,
// "History IDs are not in the file".

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
apiMsg.Content = ...          // TextParts — plus media blocks on user messages
                              // (content array; see openaiConvertRegularContent)
apiMsg.ReasoningContent = ... // only ReasoningParts go here
apiMsg.ToolCalls = ...        // only ToolInputParts go here
// ToolOutputParts become entirely separate messages with role="tool", whose
// content is always a plain string — media inside a tool result is therefore
// promoted onto a follow-up user message (see docs/providers.md → "Tool results").
```

And on receive, both providers use the same pattern: accumulate content by `index` across streaming chunks, then assemble into a single `[]ContentPart` at step completion. OpenAI accumulates three parallel fields (reasoning, text, tool arguments) by index; Anthropic accumulates content blocks by index — structurally the same approach.

**Conclusion:** The domain layer was clearly inspired by Anthropic's content block array model. It's the more general and extensible design — adding a new content type just means adding a new `ContentPart` implementation and a new case in each provider's switch statement. OpenAI's flat-field model is the odd one out requiring non-trivial split/merge logic.

## Wire Format Comparison

| Domain Type | OpenAI Wire | Anthropic Wire |
|---|---|---|
| `TextPart` | `content` (top-level field) | `content[]` array: `{type:"text", text:"..."}` |
| `ReasoningPart` | Top-level key named by model.conf `reasoning_field` (same key on send and receive) — `reasoning_content` (default) or `reasoning` (vLLM) | `content[]` array: `{type:"thinking", thinking:"..."}` |
| `ImagePart` | `content[]` array: `{type:"image_url", image_url:{url:"data:image/...;base64,..."}}` | `content[]` array: `{type:"image", source:{type:"base64", media_type:"image/jpeg", data:"..."}}` |
| `AudioPart` | `content[]` array: `{type:"input_audio", input_audio:{data:"UklGRiQ...", format:"wav"}}` | ❌ No audio block exists → `{type:"text"}` placeholder (payload not echoed) |
| `VideoPart` | `content[]` array: `{type:"video_url", video_url:{url:"data:video/...;base64,..."}, fps:2, media_resolution:"default"}` | ❌ No video block exists → `{type:"text"}` placeholder (payload not echoed) |
| `DocumentPart` | ❌ No document block → `{type:"text"}` placeholder naming the file | `content[]` array: `{type:"document", source:{type:"base64", media_type:"application/pdf", data:"..."}}` |
| `ToolInputPart` | `tool_calls[]` (top-level array) | `content[]` array: `{type:"tool_use", id, name, input}` |
| `ToolOutputPart` | Separate message: `{role:"tool", tool_call_id, content}` (content is JSON-wrapped with `"status"` field — see note below). Media inside the result cannot live on this message; it is **promoted** to one follow-up `user` message carrying native media blocks (see note below) | `content[]` array: `{type:"tool_result", tool_use_id, content, is_error}`, **role remapped to "user"**. `content` can be a string or an array of content blocks (text, image, etc.) |

> **Note on OpenAI tool result content format:** OpenAI's API has no native `is_error` field for tool results (unlike Anthropic). To prevent ambiguity — e.g., a tool returning `"no such file"` as an error vs. a file containing the literal text `"no such file"` — the OpenAI provider wraps tool results as JSON:
>
> - Success: `{"status":"success","data":"<plain text output>"}`
> - Error:   `{"status":"error","data":"<error message>"}`
>
> This ensures the model can distinguish success from failure structurally rather than guessing from the content string. The Anthropic provider uses the native `is_error: true` flag instead, so results remain unwrapped plain text.

> **Note on media inside OpenAI tool results (promotion):** `role:"tool"` accepts only a string, so an `ImagePart`/`AudioPart`/`VideoPart` nested in a `ToolOutputPart` cannot be serialized on its own message. `openaiConvertToolOutputs()` therefore *promotes* it: all tool messages are emitted first, then a single `user` message carries the media as native content blocks (built by the same `openaiMediaBlock()` used for regular messages). The tool message itself keeps a text label that points forward — `"[Image (image/png)] — attached to the next message"` — so the model correlates the label with the attachment instead of concluding the media is unavailable.
>
> Promotion covers image, video, and audio-as-data-URI. Document/PDF and audio-behind-a-remote URL have no Chat Completions block at all, so they remain flattened to a text summary and spawn no promoted message. `openaiMediaBlock()` reports which case applies via its boolean return.

## Receiving (Wire → Domain)

### Example 1: Reasoning only

**OpenAI wire:**
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
Chunk 2: {"choices":[{"delta":{"reasoning_content":" about this"}}]}
```

> On a vLLM endpoint the same two chunks arrive as
> `{"delta":{"reasoning":"Let me think..."}}` — vLLM renamed the key and no
> longer emits `reasoning_content`. The key is therefore declared per
> model.conf entry (`reasoning_field: "reasoning"`); unset means
> `reasoning_content`, and a configured key is read exclusively. Replayed
> reasoning is sent under that same key — one key per deployment, both
> directions.

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

**Why this works:** `openAIStreamState` remembers the identity of each tool call by index — the protocol sends ID and name once and fragments the arguments afterwards — while the argument fragments and the reasoning text travel out as deltas and are accumulated only by `llm.Agent`. Nothing is kept twice, so the two cannot disagree.

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

**Anthropic wire output** (only image survives as a media block):
```json
{
    "role": "user",
    "content": [
        {"type": "text", "text": "Describe this multimedia"},
        {"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "/9j/4AAQ..."}},
        {"type": "text", "text": "[Unreadable audio (audio/wav): this API has no audio input block, so the content was NOT delivered to you and you have not perceived it. Do not describe or quote it. To inspect it, transcribe it to text (e.g. an available CLI via execute_command).]"},
        {"type": "text", "text": "[Unreadable video (video/mp4): this API has no video input block, so the content was NOT delivered to you and you have not perceived it. Do not describe or quote it. To inspect it, extract a frame as an image (e.g. ffmpeg via execute_command).]"}
    ]
}
```

> **Why degrading beats a native block:** an unrecognized block type makes the
> API reject the *whole* request, and the realistic trigger is a model calling
> `read_file` on a video or audio file (the tool description advertises both),
> not a user attaching media. See docs/providers.md → "Multimodal support
> comparison" for the placeholder wording rationale.

> **Note:** All media content parts store a URI (`data:{mime};base64,...` or `https://...`) in the domain layer. Each provider extracts or passes through the format it needs:
> - OpenAI `image_url` / `video_url`: passes the URI directly as the `url` field
> - OpenAI `input_audio`: parses the data URI to extract raw base64 `data` and `format` from the MIME type; remote URLs are not supported and replaced with a text placeholder
> - Anthropic (image and document only): parses data URIs to extract `media_type` and raw base64 `data`; plain URLs use the `url` source type

### Wire Format Differences (Anthropic vs OpenAI)

| Aspect | OpenAI | Anthropic |
|---|---|---|
| **Message structure** | Flat fields (`content`, `reasoning_content` or `reasoning`, `tool_calls` at top level) | Content is always `[]anthropicContentBlock` array |
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
	reasoningBuilder  strings.Builder                ← delta chunks under the configured reasoning_field
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

Every wire event carries an `index` (start, delta, stop), just like OpenAI's `tool_calls[index]`. Blocks may arrive interleaved — block 1 can start before block 0 finishes. Each block is independently accumulated by index. `content_block_stop(i)` emits a boundary event for that index and nothing else; because blocks close in declared index order, the assembler's record follows it.

### Block keys: how a history ID finds its content

A block's protocol `index` is a chunk-correlation handle: it says which fragments belong together. It is **not** an identity, and it must never be used as one — the first version of this bound history IDs by position in the array assembled at step end, which quietly assumes `event index == array position`, a thing no provider guarantees. A server whose tool-call indices skipped a number left that tool holding `HistoryID = 0` for the rest of the session, the same value as "never numbered".

Providers therefore name each block, and the name is held by `llm.Agent`'s assembler for the block's whole life:

```
streaming event   Key: "tool:0"  ──▶  assembler block { key, kind, body, historyID }  ──▶  persisted part
```

The key never reaches the part: it was only ever the join between a streamed block and a separately-assembled part, and once one object holds the content and its ID, there is nothing left to join. `persisted part` above is the record's entry, and what it carries is content, role, and the ID minted at the block's first byte.

| Provider | Naming | Why |
|----------|--------|-----|
| OpenAI reasoning / text | `"reasoning"` / `"text"` | Flat fields, exactly one builder each, so a per-kind name is unique by construction. |
| OpenAI tool calls | `"tool:<raw SSE index>"` | Same handle the accumulator map uses: present on the first chunk and immutable for the block's life. The call ID is **not** usable — continuation chunks send `id: ""` and some servers reuse one ID across calls, which would move or merge the key mid-stream. |
| Anthropic | `"block:<index>"` | The server declares one shared index space, so the number is given, not invented. A message can hold several thinking or text blocks, so a per-kind string would collide. |

Three rules follow for anyone implementing a provider:

1. `Key` is opaque. It is compared for equality and never ordered, incremented, or used to index a slice. The old `2 + index` arithmetic existed only to fake a position, and faking a position is what broke.
2. A block's key must not change between its first event and the part it becomes. OpenAI's `"tool:<index>"` is stable precisely because it is fixed before the call ID arrives.
3. A part exists only if its block streamed content under a key. There is no step-end binding step to get wrong, so a provider cannot persist a part the stream never carried, and an unfilled slot cannot become an empty part. (Numbering is skipped entirely when `IDGen` is nil; the content is still assembled.)

IDs are issued in first-touch order, so they number blocks by arrival, not by record position. See [providers.md](../providers.md) → "Complete-event order" for the obligation that puts on providers, and [tui.md](../tui.md) → "Window Order" for the one place that reads ID magnitude as an order.
