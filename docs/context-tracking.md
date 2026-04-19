# Context Token Tracking

How AlayaCore tracks conversation context size across LLM API calls and providers.

## Overview

`ContextTokens` in `Session` tracks the current conversation's context size (input tokens) as reported by the LLM provider. It is used for:

- Displaying context usage in the status bar (e.g. `context: 2118 / 128000`)
- Triggering auto-summarization when context exceeds 80% of `context_limit`

## Data Flow

```
Provider API response
  → Provider extracts usage (InputTokens, CacheReadTokens, CacheCreationTokens)
    → StepCompleteEvent carries Usage
      → Agent.Stream calls OnStepFinish callback with stepUsage
        → Session.trackUsage(stepUsage)
          → ContextTokens = InputTokens + CacheReadTokens + CacheCreationTokens (only if non-zero)
```

## The `trackUsage` Function

```go
func (s *Session) trackUsage(usage llm.Usage) {
	s.mu.Lock()
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	// Only overwrite ContextTokens if the provider reported a non-zero value.
	// OpenAI-compatible APIs (e.g. GLM-5.1) occasionally omit the usage chunk
	// or return all zeros, which would incorrectly reset ContextTokens to 0.
	newContext := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
	if newContext > 0 {
		s.ContextTokens = newContext
	}
	s.mu.Unlock()
	s.sendSystemInfo()
}
```

Key design decisions:

- **Overwrite (`=`), not accumulate (`+=`).** Each API call's `InputTokens` already represents the *entire conversation history* sent in that request. Accumulating would double-count.
- **Guard against zero reports.** Some OpenAI-compatible providers (e.g. GLM-5.1) may omit the `usage` field from SSE chunks entirely — they simply never send a chunk containing `"usage": {"prompt_tokens": N, ...}`. Go's `json.Unmarshal` leaves absent fields at their zero values, so the parsed `Usage` struct arrives as all zeros. Without the guard, this would reset `ContextTokens` to 0, breaking auto-summarization and the status bar display. The `if newContext > 0` check preserves the last known good value.
- **Only the last step's value matters.** For multi-step tool call loops, each step re-sends the full history (plus new messages). The last step has the most complete count.
- **Cache tokens are additive.** Anthropic reports `InputTokens` as the non-cached portion; `CacheReadTokens` and `CacheCreationTokens` are separate. The sum gives the true context size.

## Multi-Step Tool Calls

When the agent loop runs multiple steps (tool call → tool result → next step), `trackUsage` is called once per step via `OnStepFinish`. Each call overwrites `ContextTokens` with that step's full-context measurement:

```
Step 1 (tool call):     InputTokens=500, CacheRead=8000 → ContextTokens = 8500
Step 2 (tool response): InputTokens=900, CacheRead=8000 → ContextTokens = 8900  ← final, correct value
```

The last step's value is always the most accurate because it includes all prior tool results.

## Provider Differences

### Anthropic Protocol

Reports usage across multiple SSE events (`message_start`, `message_delta`, `message_stop`). The provider's `extractAndSetUsage` merges partial chunks:

| Event | Fields Present |
|-------|---------------|
| `message_start` | `input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` |
| `message_delta` | `output_tokens` |
| `message_stop` | (final usage, if any) |

`InputTokens` = non-cached portion only. Cache tokens are separate.

**Usage extraction is a merge, not a single-shot read.** `extractAndSetUsage` preserves values from earlier events and only overwrites fields that are present in the current chunk:

```go
inputTokens := current.InputTokens         // keep previous value
if v, ok := usage["input_tokens"].(float64); ok {
    inputTokens = int64(v)                  // overwrite only if present
}
```

This makes the Anthropic path inherently resilient to missing usage data — if one event omits a field, the value from a prior event survives.

In practice, the Anthropic spec guarantees that `message_start` always includes a `usage` object with `input_tokens`, so usage is rarely missing entirely. However, Anthropic-compatible servers (e.g. llama.cpp exposing `/v1/messages`) may not fully implement the spec. The `newContext > 0` guard in `trackUsage` provides a safety net for these cases.

### OpenAI Protocol

Reports usage in a **single SSE chunk** with `prompt_tokens` and `completion_tokens`:

| Field | Meaning |
|-------|---------|
| `prompt_tokens` | Full context size (all messages sent) |
| `completion_tokens` | Output tokens generated |

`prompt_tokens` already includes any cached tokens — no separate cache fields.

**This is a single point of failure.** Unlike the Anthropic path, usage is set in one shot:

```go
if streamResp.Usage.PromptTokens > 0 || streamResp.Usage.CompletionTokens > 0 {
    state.setUsage(...)
}
```

If the provider never sends a chunk containing `"usage"`, `state.usage` stays at its Go zero value (`{InputTokens: 0, OutputTokens: 0, ...}`). The provider is not returning `{"usage": {"prompt_tokens": 0}}` — it simply omits the `usage` field from the SSE chunk entirely, and Go's `json.Unmarshal` initializes absent fields to their zero values.

This is why the ContextTokens-reset-to-zero bug is specific to OpenAI-compatible providers. Some providers (e.g. GLM-5.1) intermittently omit the usage chunk, causing `trackUsage` to receive all zeros. The `newContext > 0` guard in `trackUsage` prevents the last known good value from being overwritten.

## Model Switching and Token Count Changes

When switching models (e.g. Anthropic → OpenAI), the reported context size may change even though the conversation history is unchanged. This is expected — **different providers use different tokenizers**.

### Example

| Step | Provider | ContextTokens | API Reported |
|------|----------|--------------|-------------|
| Prompt 1, Step 1 (tool) | Anthropic/llama.cpp | 1149 | input=4, cache_read=1145 |
| Prompt 1, Step 2 (answer) | Anthropic/llama.cpp | 2118 | input=973, cache_read=1145 |
| Model switch | → OpenAI/glm-5.1 | *(unchanged)* | — |
| Prompt 2, Step 1 (tool) | OpenAI/glm-5.1 | 2073 | prompt_tokens=2073 |
| Prompt 2, Step 2 (answer) | OpenAI/glm-5.1 | 2317 | prompt_tokens=2317 |

The apparent "drop" from 2118 to 2073 after model switch is the difference in tokenization between the two models. The full conversation was sent correctly.

## Related

- `shouldAutoSummarize()` — triggers when `ContextTokens >= ContextLimit * 80%` (only when `--auto-summarize` is enabled)
- `summarize()` — appends the summary prompt to Messages, calls `processPrompt`, then replaces conversation history with the summary and resets `ContextTokens` to the summary's output token count
- `applyModelContextLimit()` — sets `ContextLimit` from the active model's config
