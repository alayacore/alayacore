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

## Who accumulates streamed content

One step's text is held in three places at once. That looks like an accident; it is not, and the way to read it is the question each buffer answers.

| Layer | Where | Answers | Can it be deleted |
|-------|-------|---------|-------------------|
| Provider | `openai.go` `textBuilder` / `reasoningBuilder` / `toolAccumulators`; `anthropic.go` `blockAccumulator.buffer` | "what is the assembled turn?" — builds `StepCompleteEvent.Contents` via `getContents()`, and the authoritative text each `*CompleteEvent` carries | No: it is the record's source |
| `llm.Agent` | `agent.go` `stepTextBlocks` | "what did this step produce if the stream is cut?" — the failed-step path contributes these parts to history | No: see the second constraint below |
| Adapter | `terminal/output.go` `pendingTextDeltas`; `terseio` final-answer buffer | "how do I redraw without taking `WindowBuffer.mu` once per token?" | Only by regressing to per-token renders |

Two constraints force the split:

- **Only the parser knows where a block ends.** Anthropic says so (`content_block_stop`); OpenAI never does — `reasoning_content`, `content` and `tool_calls` are three flat fields on a chunk with no per-field terminator, and tool arguments arrive as JSON fragments under a wire index whose continuation chunks carry `id: ""`. Reassembling that is protocol knowledge, so it lives with the protocol. Moving it outward means the agent learns each wire format, which is one copy of the same logic per provider instead of one.
- **`iter.Seq2` gives a producer nothing to say after the consumer stops pulling.** When `streamEvents` returns on a stream error, the provider's pending `yield` returns `false`; its tail never runs, and calling `yield` again is a runtime error (pinned by `TestYieldAfterAbandonmentPanics` and `TestProviderCannotHandBackItsBufferAfterAbandonment`). So "let the provider salvage itself" has no transport: the buffer that already holds the text is unreachable exactly when it is needed. `stepTextBlocks` is what closes that hole, not a re-implementation of the provider's job.

The accepted cost is a second copy of one step's text inside the agent's stack frame, released when the step ends. On a step that finishes normally the copy is discarded unused, because `StepCompleteEvent.Contents` wins.

**That framing understates it, so state the real smell plainly: what is duplicated is not memory, it is logic.** Two pieces of code that never call each other turn a stream into `[]ContentPart` — `getContents()` in each provider, `stepTextBlocks` plus `salvageExecutedTools` in the agent — and neither can guarantee the other agrees. The agent's copy is also narrower than the provider's: it covers blocks that arrived *as events*, so anything a provider assembles only in its tail has no counterpart there. That is not hypothetical — the first version of the termination rule below dropped an already-complete tool call from a cut stream (`tool ran=0`, history empty) while the adapter had drawn it from its deltas. So the arrangement is fragile in a way the buffers above do not show, and it is currently contained by a test rather than by design: `openai_salvage_parity_test.go` requires both assemblers to land the same history for the same stream.

Design that removes it: keep exactly one assembler. Providers emit per-block events and nothing else, `llm.Agent` builds the record from the stream on every path, and `StepCompleteEvent` carries only `Usage` and `StopReason`. The salvage path then stops being a second implementation; the check that rejects a provider persisting a part it never streamed has nothing left to check, because there is no second source to disagree with; and ID order and record order become the same fact rather than two that happen to coincide, which would also close the arrival-order residue under "Complete-event order". Not done: it rewrites both providers, the agent's history binding, and every test that reads `StepCompleteEvent.Contents`.

Two things this does *not* mean:

- `--no-delta` does not stop the response streaming. It selects which frames the session sends to its adapter (`session_task.go` registers no delta callbacks), so the provider still streams and still numbers blocks by delta arrival. Pin: `TestStreamNumbersBlocksAtDeltasEvenWithNoDeltaCallbacks`.
- Adapters must not read any of this as an ordering source. Only the provider's record and the agent's ID assignment carry order; see below.

## Complete-event order

`parseStream()` emits the step's trailing complete events after the stream ends, from the accumulators. Their order is load-bearing, not cosmetic: it has to agree with the order `getContents()` persists the step's parts in (`reasoning`, `text`, `tools`), for two reasons.

- **historyID numbering.** `llm.Agent` hands out a historyID at the *first event* for each block key (`blockID`), whether or not a callback is registered for it — so IDs number blocks by stream arrival in every mode, `--no-delta` included (measured: identical numbering with and without delta callbacks). Emitting a later block first in the *trailing* events does not reorder the numbering, because the numbering was settled while the response streamed.
- **Adapter output.** Adapters render in frame order. The terminal creates a window per block the moment its frame arrives (positions are fixed at creation; `WindowBuffer` only ever appends), and `--plainio` prints content as it streams — measured: `[AT, AR]` → `"Hello!\nuser said hello"`, `[AR, AT]` → `"user said hello\nHello!"`. Emitting out of array order puts the answer above the reasoning it came from.

Both swaps are applied: `ReasoningCompleteEvent` precedes `TextCompleteEvent`, and the tool loop moved below that pair. Measured IDs per block for a `reasoning + text + one tool call` step:

| streamed order | mode | before | after |
|----------------|------|--------|-------|
| reasoning, text, tool | delta (default) | 100 / 101 / 102 ✅ | 100 / 101 / 102 ✅ |
| reasoning, text, tool | `--no-delta` | 102 / 103 / **100** ❌ | 100 / 101 / 102 ✅ |
| tool_calls first | delta | 101 / 102 / **100** ❌ | 101 / 102 / **100** ❌ |

Row 2's numbering did not move because of the frame reorder. It moved because `blockID` now runs outside the `callbacks.On* != nil` guards, so a block is numbered at its first streamed event even when no delta frame is emitted for it — which makes numbering arrival-based in *every* mode, `--no-delta` included (measured identical with and without). The frame reorder is what fixes row 2's *display*: with no deltas pending, `flushPendingDeltas` never runs and the authoritative frames alone decide window order.

**What the move fixes** (`--no-delta`): no deltas are ever pending in that mode, so `flushPendingDeltas` is a no-op and the TUI creates windows purely in frame order. With tools emitted first, `TOOL CALL` landed *above* the `REASONING` that produced it while the record listed reasoning, text, tool — and reopening the saved session re-laid the same conversation as reasoning, text, tool, so live and reopened disagreed. `--plainio` printed in the same inverted order. Emission now equals array order, so all three agree.

**What it cannot fix** (row 3): a provider that *streams* its `tool_calls` delta before its reasoning/text deltas takes the tool's ID at that first delta, long before the trailing block runs — reordering the trailing block doesn't reach it.

**Why the record is ordered by kind, not by arrival.** For Anthropic the question does not arise: the server declares a block index, blocks arrive in it, and `anthropicConvertContents` emits content blocks back in array order, so arrival order *is* record order. For OpenAI it is a real choice, and arrival order was rejected because it is not reliably observable. `ChatCompletionStreamResponseDelta` has no notion of a block — `reasoning`, `content` and `tool_calls` are three flat fields that can all be populated in the same chunk, and `handleDelta` walks them in a fixed field order. So "which arrived first" is partly decided by our own handler, not by the model. Recording that as history would be exactly the invented-authority move that produced the bug above. `getContents()` instead rebuilds the one shape OpenAI defines for an assistant turn — reasoning, content, tool calls — so the same reply yields the same record however it is transported. One order constraint is hard and unrelated to this: a `ToolInputPart` must precede its `ToolOutputPart`, since `reorderToolResults` sequences results by input order (outputs are appended by the agent after execution, never by the provider).

Order *within* the tools is the protocol's, not ours to reinterpret: `toolIndices()` sorts by the declared `tool_calls[].index`, so a server that streams index 1 before index 0 still persists `call_first` first, and `reorderToolResults` then sequences the results by that same call order. Block keys are `"tool:<index>"`, taken from that same value, so identity and order agree by construction. Nothing here may switch to first-appearance order — that would lay out and number parallel calls by streaming luck instead of by what the model asked for. Pinned by `openai_tool_order_test.go`.

So the remaining residue is OpenAI-only, needs a server to stream against its own message shape, and has never been observed; the code path is confirmed with a synthetic stream. Note also that array order carries no weight on OpenAI's *request* side (`openaiConvertContents` routes parts into separate fields by kind) — it is Anthropic, where the array becomes an ordered block list, where record order decides what the model sees next turn. Either way the divergence is not durable: IDs are not saved, and reload renumbers in record order.

**Separately fixed:** an ID no longer depends on a position matching. Blocks are bound by the opaque key they stream under (see [data-mapping.md](internal/data-mapping.md) → "Block keys"), which retired the `2 + index` arithmetic entirely. A server whose tool-call indices skipped a number used to leave that tool holding `HistoryID = 0` for the rest of the session — indistinguishable from "never numbered", invisible, and unresolvable by `:fork`, which looks parts up by ID. It now claims its own ID, and a part naming a block that was never streamed is rejected with an error instead. (The bad value never reached disk: history IDs are not stored in the session file and are re-issued on load — see [architecture.md](architecture.md) → session file format.)

Pinned by `openai_event_order_test.go` (emission order, tools last), `openai_history_id_order_test.go` (ID numbering monotonic with content position; non-contiguous tool indices all get IDs), and `agent_blockkey_test.go` in the llm package (unstreamed block key is an error; no `IDGen` means no numbering to bind).

## Stream termination

The two protocols end a stream differently, and the difference is load-bearing for what reaches history.

**Anthropic** ends a message with `message_stop`, and that event is the only place this provider emits `StepCompleteEvent`. A body that simply stops — a proxy timeout, an upstream close — therefore yielded no events and no error: `llm.Agent` saw a step that produced nothing, reported the turn as a success, and the reasoning and text the user had watched stream in existed only in the display, so they disappeared on save-and-reopen. `parseStream` now tracks whether `message_stop` arrived and, on a clean EOF without it, yields `anthropic stream ended before message_stop`. The error is the point: `llm.Agent`'s failed-step path then contributes the streamed blocks to history (see [step-messages.md](step-messages.md)), so a truncated turn is kept *and* known to be truncated. Synthesizing a `StepCompleteEvent` instead was rejected because there would be no stop reason, and a cut connection presented as a normally finished turn makes a retry believe the assistant concluded.

**OpenAI** has no terminal event, but it has two terminal *signals*: the `finish_reason` field on the last chunk, and the `[DONE]` sentinel that closes the stream. Neither used to be required — `parseStream` rebuilt the step from its accumulators whenever the body ended, so a proxy timeout or upstream close completed a turn with whatever `finish_reason` had arrived, which for a cut stream is none at all, and `llm.Agent` treats only `max_tokens`/`length` as truncation. Measured before the change: a stream with no `[DONE]` returned `err = nil` and persisted its reasoning and text as a concluded answer, so a retry believed the assistant had finished thinking.

`parseStream` now requires **either** signal and errors on a body carrying neither, which puts both providers under one rule: a premature end is an error, and the streamed content still reaches history through the failed-step path (see [step-messages.md](step-messages.md)), kept *and* known to be partial.

**Where the error is raised matters as much as raising it.** It replaces the step's *conclusion* only — the per-block complete events are still emitted first, because a block whose arguments arrived in full did complete individually regardless of how the message ended. Reporting the error before them (the first version of this rule) made a cut stream drop the tool call entirely: never executed, never recorded, while the adapter had already drawn it from its deltas. Measured, that version gave `tool ran=0 history=[]` where the terminated stream gave `tool ran=1 history=[call result]` — the same "display holds what history lacks" shape as the ordering bug above, reached from the opposite direction.

Why *either*, not both: each signal alone is a legitimate ending for real endpoints, and demanding the pair would reject them. Measured against this repo's own suite — requiring `finish_reason` specifically fails 12 tests whose bodies close with `[DONE]` and never name a reason (including every tool-call streaming test); requiring `[DONE]` on top of a `finish_reason` fails the ones that end the body after the final chunk. A body with neither is the only shape that cannot be a finished turn.

Pinned by `anthropic_stream_termination_test.go` and `openai_stream_termination_test.go` (each terminal signal accepted on its own, no signal rejected, and the rejected stream's content verified to land in history anyway). `openai_salvage_parity_test.go` pins the stronger property, which is the one that keeps the two independent assemblers honest: the same body run terminated and run cut must land the *same* history — same parts, same order, same IDs — and must execute the tool both times.

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