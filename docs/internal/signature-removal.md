# No Anthropic Thinking Signature Support

**Decision**: The `signature` field in Anthropic's extended thinking is
**not parsed, stored, or sent back**. The field has been removed from all
wire structs and the domain `ReasoningPart` type.

Go's `json.Unmarshal` silently skips unknown JSON fields, so the wire data
is harmlessly discarded with zero code overhead.

## Why

The signature fails on all three fronts: it doesn't technically work, the
business motive is vendor lock-in, and there is no tooling to verify it.

---

### 1. It doesn't work

**Reasoning leaks into un-signed channels.** Signature only covers
`thinking` blocks, but the same reasoning process routinely spills into
ordinary `text` blocks — models output chain-of-thought as plain text,
hide reasoning inside code block comments, or produce summaries when the
thinking buffer is exhausted. Every byte of reasoning that lands in an
un-signed channel bypasses the signature entirely.

A mechanism that only covers a subset of the content it claims to
authenticate does not provide integrity — it provides the illusion of it.

**Signature ≠ honesty.** Anthropic's own research shows that models can
"lie" in their thinking chain — for example, to conceal the use of
disallowed prompts or to simulate alignment they do not actually have.
A signature proves only that the text originated from Anthropic's model.
It proves nothing about whether that text is truthful, complete, or
aligned with human intent.

Authenticity without honesty has no practical value for an integrity
claim.

---

### 2. It's vendor lock-in

If integrity were the goal, every content part would be signed — `text`,
`tool_use`, `tool_result` — because those directly determine output and
tool execution. A tampered `tool_use` argument is far more dangerous than
tampered thinking text.

Yet signature is only applied to `thinking` blocks — the one content type
that is never shown to the user by default, does not affect tool execution,
and does not appear in the final output. The only coherent explanation is
**making message history non-portable**: other providers (DeepSeek, GLM,
MiniMax, Ollama) do not use this field, so propagating it ties the
conversation to Anthropic.

This directly conflicts with the core value of an agent framework —
users configure models in `model.conf` and switch freely between them.
The same conversation must work across providers.

---

### 3. It's not for you — there is nothing to verify

The absence of verification tooling is not an oversight. The signature is
not designed for the user to verify — it is designed for **Anthropic's
server** to check when you send thinking blocks back. If the signature is
missing or invalid, the API rejects the request.

This confirms the lock-in thesis: the signature exists solely so Anthropic
can identify and reject traffic that did not originate from their own API.
You were never meant to verify it — you were meant to pass it through
blindly, binding your message history to their platform.

---

## What Happens in Practice

- `signature_delta` events arrive normally over SSE. The JSON is
  unmarshaled but the `signature` field is silently dropped because no
  struct field maps to it.
- The switch in `handleContentDelta` has no case for `"signature_delta"`,
  so these events are a no-op.
- No signature data is included when sending history back to any API.
- Every Anthropic-format endpoint this tree can reach — DeepSeek's, Qwen's
  and MiniMax's `/anthropic` — accepts history with no `signature`, and in
  fact accepts one that is deliberately fake: none of them validates the
  field at all (measured 2026-11; the probe table is in
  [providers.md](../providers.md#sending-reasoning-back-in-tool-call-chains)).
- Anthropic's own API is the one endpoint not tested here. Its
  documentation asks for thinking blocks back "complete and unmodified",
  so a real-Anthropic user may see the rejection first. That is the
  scenario the last section is about.

If Anthropic ever enforces signature validation, users will see an API
error and can re-evaluate at that point. For now, openness wins over
proprietary lock-in.
