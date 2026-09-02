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
| **Audio** | ✅ `input_audio.data` + `format` (DataURI only) | ❌ No such block in the API → degraded to a text placeholder that tells the model it did not hear anything (`anthropicUnsupportedMediaBlock`) |
| **Video** | ✅ `video_url.url` + `fps` + `media_resolution` | ❌ No such block in the API → degraded to a text placeholder, same as audio |
| **Document (PDF)** | ❌ Falls back to text placeholder | ✅ `source.type="base64"` or `"url"` |

> **Why audio/video degrade instead of failing loudly:** sending an
> `{type:"audio"}` or `{type:"video"}` block is not a partial failure — the API
> rejects the *whole* request. The realistic trigger is not a user attaching a
> clip but a model calling `read_file` on one, because the tool description
> advertises video and audio support; before this degradation, that model broke
> its own next turn. The placeholder text is deliberately explicit that nothing
> was delivered: a bare "[video unsupported]" reads to a model as an ordinary
> tool result, after which it will describe frames it never received. This is a
> workaround to delete, not a policy to maintain — if the API gains the blocks,
> the two cases move back onto `anthropicMediaBlock`.

### Tool results

A `tool` message can only carry a string — that is a hard rule of the Chat
Completions wire format, and it still holds. What changed is that media is no
longer merely described: OpenAI **promotes** it to a follow-up `user` message,
which does accept a content array.

| Capability | OpenAI | Anthropic |
|---|---|---|
| **Nested media in tool result** | ✅ Media is promoted onto one follow-up `user` message, so the model sees the actual pixels/frames/audio rather than a label. Promotable: image (data URI or URL), audio (data URI only), video (data URI or URL) — and only when the URI is `data:` or `http(s)://`. Not promotable → stays a text summary: document/PDF (no block type exists), audio behind a remote URL, and any media whose URI the server could not fetch (bare path, `file:`, empty). | ✅ `tool_result.content` is an array that can contain text, image, document, etc. sub-blocks, recursively serialized via `anthropicPartToBlock`. |
| **Implementation** | `openaiConvertToolOutputs()` collects native blocks via `openaiMediaBlock()`, emits all tool messages first, then appends exactly one `user` message. Inside it, every media-carrying result opens its own text heading naming its `tool_call_id`, so a result that returned several items (an MCP server may) stays attributable without the model counting items per id. The tool message keeps a forward-pointing label (`openaiPromotedMediaLabel()`) so the model does not read `[Image (image/png)]` as "pixels unavailable". | `anthropicPartToBlock()` calls itself recursively for each sub-part in `ToolOutputPart.Output`, producing proper content blocks. |

> ⚠️ **What the Anthropic column describes is what our serializer emits, not
> confirmed server acceptance.** `image` nested in `tool_result` is
> long-standing and exercised in practice; `document` nested in `tool_result`
> is **not verified here**. An unrecognized block type anywhere in the request
> fails the whole request — the exact failure mode audio and video had until
> `anthropic.go` gotcha 3 was introduced. Verify against a live API before
> relying on a tool that returns a PDF.

Three consequences of promotion worth knowing:

- **Emission order is load-bearing.** Every `tool_call` of the preceding
  assistant message must be answered before any other role appears, so all tool
  messages go out first and their media is aggregated into a *single* trailing
  user message. Per-round promotion keeps each promoted message adjacent to the
  round that produced it.
- **Attribution travels as text, per result.** A media block has nowhere to
  carry provenance, so each media-carrying result opens its own text heading
  above its blocks. A single global header listing `tool_call_id`s would be
  ambiguous as soon as one result returns several items (an MCP server may) —
  resolving it needs the model to count items per id across messages, and a
  miscount misattributes media silently, with no error to reveal it.
- **Recomputed every turn, but prefix-stable — which is what keeps prompt
  caching intact.** Promotion is a pure function of history, so unlike a
  cached/synthesized message it cannot drift between turns: the same history
  serializes to the same bytes, and the message sequence of a shorter history
  stays an exact prefix of a longer one, with new content appended after it
  (locked by `TestOpenAIPromotionIsDeterministicAndPrefixStable` — a property
  that no failure mode would reveal, since only cache hit rates change).
  The promoted media therefore sits inside the cached prefix and is typically
  accounted as cache reads rather than fresh input tokens on caching endpoints
  (provider-dependent; alayacore sets no explicit `cache_control` breakpoint,
  while still parsing `cache_read_input_tokens` into usage). The costs that are
  genuinely repeated are upload bytes and server-side tokenization, plus full
  input pricing on endpoints that do not cache. It also means anything that
  rewrites an *earlier* block invalidates the prefix from that point:
  `:video_config` changes `fps`/`media_resolution` embedded in every video
  block, and `video_url` itself is a non-standard extension — so promoting
  video to a strict Chat Completions endpoint can turn a request that
  previously succeeded (media flattened to text) into a 400.

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

## Reasoning mode and reasoning fields

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

> **Note:** The OpenAI-compatible thinking/reasoning parameters (`thinking`, `reasoning_effort`, `reasoning_content`) are not part of the official OpenAI API standard. They originate from [DeepSeek's thinking mode documentation](https://api-docs.deepseek.com/guides/thinking_mode) and are supported by **DeepSeek**, **GLM**, and **MiniMax**. Other providers silently ignore unknown fields — so a custom `reasoning_*` block for one provider is no harm to another. The response side is no more standard than this: the key carrying reasoning text is configurable (`reasoning_field`) — see below.

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

### Sending reasoning back in tool-call chains

Some OpenAI-compatible providers (e.g. DeepSeek) return `reasoning_content` in assistant responses. Per [DeepSeek's documentation](https://api-docs.deepseek.com/guides/thinking_mode):

> Between two user messages, if the model performed a tool call, the intermediate assistant's `reasoning_content` must participate in the context concatenation and must be passed back to the API in all subsequent user interaction turns.

This means **all** intermediate assistant messages in a multi-turn tool call chain must include their reasoning text. Dropping it causes a 400 error from providers that require it. The key that text travels under is the one declared by `reasoning_field` — `reasoning_content` by default, which is the spelling DeepSeek itself expects.

### Which key carries reasoning (`reasoning_field`)

No key for reasoning exists in OpenAI's `ChatCompletionStreamResponseDelta`
schema (only `content`, `role`, `tool_calls`, `function_call`) — every server
that ships reasoning invented one, and the invented names differ:

| Key | Served by |
|---|---|
| `reasoning_content` | DeepSeek (originator), GLM, MiniMax, Qwen/DashScope — **the default here** |
| `reasoning` | vLLM (renamed from `reasoning_content`, old name no longer emitted), OpenRouter (`reasoning_content` is a documented alias of it) |
| `reasoning_details` | OpenRouter — an **array** of typed blocks (`reasoning.summary` / `reasoning.text` / `reasoning.encrypted`), not a string |

The key is **per model.conf entry**, because it is a property of the serving
stack, not of the model: the same deepseek weights answer as
`reasoning_content` on `api.deepseek.com` and as `reasoning` on a self-hosted
vLLM. Declare it explicitly:

```yaml
name: "172.16.9.6:9999 / Local LLM (OpenAI)"
protocol_type: "openai"
base_url: "http://172.16.9.6:9999/v1"
reasoning_field: "reasoning"
```

Rules:

- **Omitted → `reasoning_content`.** Existing configs are unaffected.
- **A configured key is used exclusively.** Nothing is read from any other
  spelling, so a wrong value reads as empty reasoning — see below.
- **A non-string value under the key is ignored.** Pointing
  `reasoning_field` at `reasoning_details` yields no reasoning rather than
  garbage; extracting those blocks needs type-aware parsing, not a name.
- **One field, both directions.** Replayed reasoning in a tool-call chain goes
  out under `reasoning_field` too. A deployment has one vocabulary for one
  concept, so declaring "this endpoint calls it `reasoning`" must not make
  alayacore answer with `reasoning_content`; and `reasoning` is vLLM's own
  canonical input field (`ChatMessage.reasoning`), so the symmetric choice is
  also the non-translated one. There is no separate send-side knob.
  Pinned by `TestOpenAIReasoningFieldAppliesToSendSide` and
  `TestOpenAIReasoningFieldGovernsEmptyPadding`.

Why the key is configured rather than auto-detected: a guess has no sound
tie-break once a server populates two names at once (OpenRouter does, as
aliases), and a hardcoded candidate list only grows by shipping a new binary —
whereas the model.conf entry already knows which stack it points at.

**The failure mode to watch for:** reasoning disappearing with no error. A
wrong or missing `reasoning_field` produces exactly that — the `REASONING`
window never appears, text streams normally, nothing logs. Check this setting
before suspecting `:reason` itself. See `TestOpenAIReasoningField`.

### Empty reasoning block padding — implementation

Both providers pad assistant messages with an empty reasoning value — but **only when reasoning mode is enabled** — to avoid wasting input tokens when it isn't needed.

This behavior is independent of the `reasoning_*` JSON blocks — it is a hardcoded message-layer convention that DeepSeek and similar providers require. Configuring `reasoning_0: {"thinking":{"type":"disabled"}}` does NOT turn off empty padding; the padding is gated solely on the reasoning level being > 0.

- **Anthropic provider** (`anthropicConvertContents`): prepends an empty `{"type":"thinking","thinking":""}` block to every assistant message that lacks one. The thinking block must come first per Anthropic's API.
- **OpenAI provider** (`openaiConvertContents`): extracts reasoning text via `openaiExtractReasoning()` and sets the reasoning key — `reasoning_field`, default `reasoning_content` (`openAIMessage.MarshalJSON` performs the redirect) — on every assistant message, even as an empty string when no reasoning text exists.

Both are conditional on reasoning mode being enabled. When reasoning mode is off, no padding is added.