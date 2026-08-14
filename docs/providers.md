# Provider-Specific Gotchas

Non-obvious patterns when working with LLM provider implementations.

> **See also:**
> - [data-mapping.md](internal/data-mapping.md) — how OpenAI/Anthropic wire formats map to domain types
> - [tool-input-repair.md](tool-input-repair.md) — how common JSON output errors are repaired at the agent level

## OpenAI multimodal content format

All media types (image, audio, video, document) are stored as a **URI** in the domain layer — either a data URI (`data:{mime};base64,...`) or a plain URL (`https://...`). The OpenAI provider transmits them as follows:

| Content Part | OpenAI Wire Format |
|---|---|
| `ImagePart` | `{"type":"image_url","image_url":{"url":"data:image/...;base64,..."}}` |
| `AudioPart` | `{"type":"input_audio","input_audio":{"data":"UklGRiQ...","format":"wav"}}` |
| `VideoPart` | `{"type":"video_url","video_url":{"url":"data:video/...;base64,..."},"fps":2,"media_resolution":"default"}` |
| `DocumentPart` | ❌ Not supported (skipped) |

Key points:
- **Image** and **video** use the `url` field with the URI value (accepts both data URIs and remote URLs).
- **Audio** uses the `data` field with **raw base64 data** (not a data URI) plus a `format` field derived from the MIME type. **Remote URLs are not supported** for audio — if a plain URL is provided it is replaced with a text placeholder.
- **Video** includes additional parameters `fps` and `media_resolution` (defaults to `2` and `"default"`, configurable via `:video_config`).
- **Document** (e.g. PDF) is silently skipped as OpenAI Chat Completions API has no document content block.

> **Note:** These wire formats are compatible with providers that extend the OpenAI-style API to support multimodal input (e.g. DeepSeek, Qwen, MiniMax, StepFun, Xiaomi MiMo). Standard OpenAI Chat Completions API only supports `image_url` and `input_audio` natively; `video_url` is a non-standard extension.

## Multimodal support comparison

The two providers have complementary multimodal capabilities — neither covers all scenarios.

### User / assistant messages

| Media type | OpenAI | Anthropic |
|---|---|---|
| **Image** | ✅ `image_url.url` (DataURI or URL) | ✅ `source.type="base64"` or `"url"` |
| **Audio** | ✅ `input_audio.data` + `format` (DataURI only) | ❌ Not supported by the API |
| **Video** | ✅ `video_url.url` + `fps` + `media_resolution` | ❌ Not supported by the API |
| **Document (PDF)** | ❌ Falls back to text placeholder | ✅ `source.type="base64"` or `"url"` |

### Tool results

| Capability | OpenAI | Anthropic |
|---|---|---|
| **Nested media in tool result** | ❌ The `tool` role only accepts string content. All media parts are flattened to text summaries like `[Image (image/jpeg)]` — the model sees a label, not the actual media. | ✅ `tool_result.content` is an array that can contain text, image, document, etc. sub-blocks, recursively serialized via `anthropicPartToBlock`. |
| **Implementation** | `openaiMediaSummary()` extracts the MIME type from DataURIs and produces a tag; remote URLs are included as-is. | `anthropicPartToBlock()` calls itself recursively for each sub-part in `ToolOutputPart.Output`, producing proper content blocks. |

### Key trade-off

```
User message:   OpenAI can send audio/video natively,
                Anthropic can only send image & document.

Tool result:    Anthropic can return images inside tool results,
                OpenAI can only describe them in text.
```

## OpenAI tool call chunking

Tool arguments arrive in chunks across multiple delta events:
- First chunk: has `id` and `name`
- Subsequent chunks: `id: ""` but correct `index`
- **Must use `index` (not `id`) to associate chunks** — see `openAIStreamState.appendToolCallArgs()`
- When sending back in history, arguments must be JSON-string (not raw JSON) — see `openaiConvertToolInputs()`

## Null arguments in tool call chunks

Some providers emit no-op deltas with `"arguments": null` (JSON literal null):

```json
{
	"choices": [{
		"delta": {
			"tool_calls": [{
				"function": {"arguments": null},
				"id": "",
				"index": 0,
				"type": "function"
			}]
		},
		"index": 0
	}]
}
```

After `json.Unmarshal` into `json.RawMessage`, `args` becomes the 4 bytes `null`. `unquoteToolArg` unmarshals it into a string, which succeeds with an empty string — so the chunk contributes nothing to the accumulated arguments. Without that, a null chunk would corrupt the accumulated JSON (e.g. `{"path": "README.md"}null`), causing tool execution to fail.

`unquoteToolArg` handles three shapes: a JSON-string-encoded fragment (`"{\"path\":...}"` → the inner text, the standard OpenAI form), a raw JSON fragment (`{"path":...}` → passed through as-is, used by some compatible providers), and `null` (→ empty string). See `openAIStreamState.appendToolCallArgs()`.

## Reasoning mode and reasoning_content

When reasoning mode is set via `:reason [0|1|2]` (or at startup via `--reasoning-level <0|1|2>`), each provider sends explicit thinking configuration in API requests. The key differences are:

1. A top-level **`thinking`** field (`{"type": "enabled"}` or `{"type": "disabled"}`) controls whether reasoning is active. This is always set explicitly — even when reasoning is off — because some providers (e.g. DeepSeek V4) default to thinking enabled. Omitting the field would leave thinking on at the API level, contradicting the UI state.
2. When reasoning mode is on (level 1 or 2), assistant messages that only contain tool calls must still include an **empty reasoning block** (required by DeepSeek and similar providers).

| Provider | Level 1 (normal) | Level 2 (max) | Disabled |
|----------|------------------|---------------|----------|
| **Anthropic** | `"thinking": {"type": "enabled"}`, `"output_config": {"effort": "high"}` | `"thinking": {"type": "enabled"}`, `"output_config": {"effort": "max"}` | `"thinking": {"type": "disabled"}` |
| **OpenAI-compatible** | `"thinking": {"type": "enabled"}`, `"reasoning_effort": "high"` | `"thinking": {"type": "enabled"}`, `"reasoning_effort": "xhigh"` | `"thinking": {"type": "disabled"}` |

> **Note:** The OpenAI-compatible thinking/reasoning parameters (`thinking`, `reasoning_effort`, `reasoning_content`) are not part of the official OpenAI API standard. They originate from [DeepSeek's thinking mode documentation](https://api-docs.deepseek.com/guides/thinking_mode) and are supported by **DeepSeek**, **GLM**, and **MiniMax**. Other providers silently ignore unknown fields.

### OpenAI-compatible — request examples

When reasoning mode is **disabled**, assistant messages contain only the tool calls — no `reasoning_content` field:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"tool_calls": [{
				"function": {
					"arguments": "{\"path\":\"/home/wallace/playground/alayacore/go.mod\",\"num_lines\":5}",
					"name": "read_file"
				},
				"id": "call_ca6eef24512147a6a9dae7bd",
				"index": 0,
				"type": "function"
			}]
		},

		...

	],

	"model": "deepseek-v4-flash",

	"thinking": { "type": "disabled" },

	...
}
```

When reasoning mode is **enabled**, every assistant message is padded with `"reasoning_content": ""` even when there is no actual reasoning text, and the request includes `reasoning_effort`:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"reasoning_content": "",
			"tool_calls": [{
				"function": {
					"arguments": "{\"path\":\"/home/wallace/playground/alayacore/go.mod\",\"num_lines\":5}",
					"name": "read_file"
				},
				"id": "call_ca6eef24512147a6a9dae7bd",
				"index": 0,
				"type": "function"
			}]
		},

		...

	],

	"model": "deepseek-v4-flash",

	"thinking": { "type": "enabled" },
	"reasoning_effort": "xhigh",

	...
}
```

### Anthropic-compatible — request examples

When reasoning mode is **disabled**, assistant messages contain only the tool-use content block — no `thinking` block:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"content": [
				{
					"id": "call_ca6eef24512147a6a9dae7bd",
					"input": {
						"num_lines": 5,
						"path": "/home/wallace/playground/alayacore/go.mod"
					},
					"name": "read_file",
					"type": "tool_use"
				}
			]
		},

		...

	],

	"model": "deepseek-v4-pro",

	"thinking": { "type": "disabled" },

	...
}
```

When reasoning mode is **enabled**, every assistant message is prepended with an empty `{"type": "thinking", "thinking": ""}` block when none is present, and the request includes `output_config`:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"content": [
				{
					"thinking": "",
					"type": "thinking"
				},
				{
					"id": "call_ca6eef24512147a6a9dae7bd",
					"input": {
						"num_lines": 5,
						"path": "/home/wallace/playground/alayacore/go.mod"
					},
					"name": "read_file",
					"type": "tool_use"
				}
			]
		},

		...

	],

	"model": "deepseek-v4-pro",

	"thinking": { "type": "enabled" },
	"output_config": { "effort": "max" },

	...
}
```

Some OpenAI-compatible providers (e.g. DeepSeek) return `reasoning_content` in assistant responses. Per [DeepSeek's documentation](https://api-docs.deepseek.com/guides/thinking_mode):

> Between two user messages, if the model performed a tool call, the intermediate assistant's `reasoning_content` must participate in the context concatenation and must be passed back to the API in all subsequent user interaction turns.

This means **all** intermediate assistant messages in a multi-turn tool call chain must include their `reasoning_content`. Dropping it causes a 400 error from providers that require it.

### Empty reasoning block padding — implementation

Both providers pad assistant messages with an empty reasoning value — but **only when reasoning mode is enabled** — to avoid wasting input tokens when it isn't needed.

- **Anthropic provider** (`anthropicConvertMessages`): prepends an empty `{"type": "thinking", "thinking": ""}` block to every assistant message that lacks one. The thinking block must come first per Anthropic's API.
- **OpenAI provider** (`openaiConvertMessages`): extracts reasoning text via `openaiExtractReasoning()` and sets `reasoning_content` on every assistant message — even as empty string when no reasoning text exists.

Both are conditional on reasoning mode being enabled. When reasoning mode is off, no padding is added.
