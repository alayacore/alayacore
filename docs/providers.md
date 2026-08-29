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
| `DocumentPart` | ❌ No document block — serialized as a text placeholder naming the file |

Key points:
- **Image** and **video** use the `url` field with the URI value (accepts both data URIs and remote URLs).
- **Audio** uses the `data` field with **raw base64 data** (not a data URI) plus a `format` field derived from the MIME type. **Remote URLs are not supported** for audio — if a plain URL is provided it is replaced with a text placeholder.
- **Video** includes additional parameters `fps` and `media_resolution` (defaults to `2` and `"default"`, configurable via `:video_config`).
- **Document** (e.g. PDF) becomes a text placeholder naming the file and MIME type, because OpenAI Chat Completions API has no document content block. It is the one media type that cannot be promoted out of a tool result either — there is no block to attach.

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

A `tool` message can only carry a string — that is a hard rule of the Chat
Completions wire format, and it still holds. What changed is that media is no
longer merely described: OpenAI **promotes** it to a follow-up `user` message,
which does accept a content array.

| Capability | OpenAI | Anthropic |
|---|---|---|
| **Nested media in tool result** | ✅ Media is promoted onto one follow-up `user` message, so the model sees the actual pixels/frames/audio rather than a label. Promotable: image (data URI or URL), audio (data URI only), video (data URI or URL). Not promotable → stays a text summary: document/PDF (no block type exists), audio behind a remote URL. | ✅ `tool_result.content` is an array that can contain text, image, document, etc. sub-blocks, recursively serialized via `anthropicPartToBlock`. |
| **Implementation** | `openaiConvertToolOutputs()` collects native blocks via `openaiMediaBlock()`, emits all tool messages first, then appends exactly one `user` message (`openaiPromotedMediaMessage()`) whose intro text names the contributing `tool_call_id`s. The tool message keeps a forward-pointing label (`openaiPromotedMediaLabel()`) so the model does not read `[Image (image/png)]` as "pixels unavailable". | `anthropicPartToBlock()` calls itself recursively for each sub-part in `ToolOutputPart.Output`, producing proper content blocks. |

Two consequences of promotion worth knowing:

- **Emission order is load-bearing.** Every `tool_call` of the preceding
  assistant message must be answered before any other role appears, so all tool
  messages go out first and their media is aggregated into a *single* trailing
  user message. Per-round promotion keeps each promoted message adjacent to the
  round that produced it.
- **Promoted media is re-sent on every later turn**, exactly like user-attached
  media — it counts toward prompt tokens for the rest of the session. And
  because `video_url` is a non-standard extension, promoting video to a strict
  OpenAI endpoint can turn a request that previously succeeded (media flattened
  to text) into a 400.

### Key trade-off

```
User message:   OpenAI can send audio/video natively,
                Anthropic can only send image & document.

Tool result:    Both can now deliver media to the model —
                Anthropic nests it in tool_result natively,
                OpenAI promotes it to a follow-up user message.

                The remaining asymmetry is *which* media:
                OpenAI promotes image/audio/video but not PDF;
                Anthropic nests image/PDF but has no audio/video block.
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

When reasoning mode is set via `:reason [0|1|2]` (or at startup via `--reasoning-level <0|1|2>`), the provider looks up `reasoning_<level>` from the active model in `model.conf` and **merges that JSON verbatim into the request body**. Top-level keys in the JSON become top-level keys of the request.

This is data-driven: each model carries its own `reasoning_0`, `reasoning_1`, `reasoning_2` blocks describing exactly what thinking-related fields the provider should send. Different provider families have different vocabularies (Anthropic uses `output_config.effort`; OpenAI/DeepSeek use `reasoning_effort`; Qwen3 might use yet another scheme) — the per-model config captures that, so alayacore itself stays provider-agnostic.

When **all** `reasoning_*` blocks are absent from a model entry, **no thinking-related fields appear in the request body** — the server picks its own defaults. This makes the fields purely additive: existing model.conf files keep working.

1. The provider reads `reasoning_<level>` from the active model.
2. It `json.Unmarshal`s the value into `map[string]any` and copies each top-level key into the request body map.
3. Empty/whitespace-only entries are silently skipped — so configuring only some levels is fine; the others fall through to the server default.

Reasoning level itself (`0`/`1`/`2`) drives:
- which `reasoning_*` block is merged into the request body,
- the empty-thinking-block padding in assistant messages (provider-specific message-layer behavior, not configurable),
- the `R0✦`/`R1✦`/`R2✦` status indicator.

### Common shapes

| Provider family | `reasoning_1` example | `reasoning_2` example |
|-----------------|-----------------------|-----------------------|
| Anthropic | `{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}` | `{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}` |
| OpenAI / DeepSeek | `{"thinking":{"type":"enabled"},"reasoning_effort":"high"}` | `{"thinking":{"type":"enabled"},"reasoning_effort":"xhigh"}` |
| Any OpenAI-compatible | the keys documented by that provider | the keys documented by that provider |

> **Note:** The OpenAI-compatible thinking/reasoning parameters (`thinking`, `reasoning_effort`, `reasoning_content`) are not part of the official OpenAI API standard. They originate from [DeepSeek's thinking mode documentation](https://api-docs.deepseek.com/guides/thinking_mode) and are supported by **DeepSeek**, **GLM**, and **MiniMax**. Other providers silently ignore unknown fields — so a custom `reasoning_*` block for one provider is no harm to another.

### OpenAI-compatible — request examples

When **no `reasoning_*` block is configured for the current level**, the request body contains no thinking-related fields and the server falls back to its own defaults:

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
		}

		...

	],

	"model": "deepseek-v4-flash"

}
```

When the user configures `reasoning_2: {"thinking":{"type":"enabled"},"reasoning_effort":"xhigh"}` and reasoning level is 2, those keys are merged into the request body:

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
		}

		...

	],

	"model": "deepseek-v4-flash",

	"thinking": { "type": "enabled" },
	"reasoning_effort": "xhigh"

}
```

### Anthropic-compatible — request examples

With `reasoning_2: {"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}` and reasoning level 2:

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
		}

		...

	],

	"model": "deepseek-v4-pro",

	"thinking": { "type": "enabled" },
	"output_config": { "effort": "max" }

}
```

Some OpenAI-compatible providers (e.g. DeepSeek) return `reasoning_content` in assistant responses. Per [DeepSeek's documentation](https://api-docs.deepseek.com/guides/thinking_mode):

> Between two user messages, if the model performed a tool call, the intermediate assistant's `reasoning_content` must participate in the context concatenation and must be passed back to the API in all subsequent user interaction turns.

This means **all** intermediate assistant messages in a multi-turn tool call chain must include their `reasoning_content`. Dropping it causes a 400 error from providers that require it.

### Empty reasoning block padding — implementation

Both providers pad assistant messages with an empty reasoning value — but **only when reasoning mode is enabled** — to avoid wasting input tokens when it isn't needed.

This behavior is independent of the `reasoning_*` JSON blocks — it is a hardcoded message-layer convention that DeepSeek and similar providers require. Configuring `reasoning_0: {"thinking":{"type":"disabled"}}` does NOT turn off empty padding; the padding is gated solely on the reasoning level being > 0.

- **Anthropic provider** (`anthropicConvertMessages`): prepends an empty `{"type":"thinking","thinking":""}` block to every assistant message that lacks one. The thinking block must come first per Anthropic's API.
- **OpenAI provider** (`openaiConvertMessages`): extracts reasoning text via `openaiExtractReasoning()` and sets `reasoning_content` on every assistant message — even as empty string when no reasoning text exists.

Both are conditional on reasoning mode being enabled. When reasoning mode is off, no padding is added.