# Error Handling

How AlayaCore detects and propagates errors from LLM API providers during streaming responses.

## Overview

Both OpenAI and Anthropic APIs use finish reasons (or stop reasons) to indicate why a streaming response ended. AlayaCore monitors these reasons to detect errors and prevent silent failures — cases where incomplete responses could be processed as if complete, the agent loop stops without explanation, or users see "context: 0" with no error message.

## OpenAI Provider

### Valid Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `stop` | Normal completion | Process the full message |
| `length` | Hit `max_tokens` limit, response is truncated | Valid — partial but not an error |
| `tool_calls` | Model wants to call tools | Execute tools and continue |
| `""` (empty) | Still streaming | Continue processing |

### Error Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `content_filter` | Content blocked by safety filters | **Error** — `"content blocked by safety filter"` |
| Any other value | Unknown reason | **Error** — `"stream finished with unexpected reason: ..."` |

### Implementation

`internal/llm/providers/openai.go` → `handleEvent`:

```go
if choice.FinishReason == "content_filter" {
	return fmt.Errorf("content blocked by safety filter")
}
if choice.FinishReason != "" && choice.FinishReason != "stop" &&
	choice.FinishReason != "length" && choice.FinishReason != "tool_calls" {
	return fmt.Errorf("stream finished with unexpected reason: %s", choice.FinishReason)
}
```

## Anthropic Provider

### Valid Stop Reasons

| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `end_turn` | Natural stopping point | Normal completion |
| `max_tokens` | Hit token limit | Valid — truncated but not an error |
| `stop_sequence` | Hit custom stop sequence | Valid |
| `tool_use` | Tool call complete | Execute tool and continue |
| `pause_turn` | Server-side extended turn | Valid — part of extended flow |

### Error Stop Reasons

| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `refusal` | Model refused (content policy) | **Error** — `"model refused to respond: content policy violation"` |
| Any other value | Unknown reason | **Error** — `"stream finished with unexpected stop reason: ..."` |

### Implementation

`internal/llm/providers/anthropic.go` → `handleMessageDelta`:

```go
if stopReason == "refusal" {
	return fmt.Errorf("model refused to respond: content policy violation")
}
if stopReason != "" && stopReason != "end_turn" && stopReason != "max_tokens" &&
	stopReason != "stop_sequence" && stopReason != "tool_use" && stopReason != "pause_turn" {
	return fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason)
}
```

## Error Propagation

When an error finish reason is detected:

1. The provider returns an error from the event handler via the streaming iterator's error parameter (`yield(nil, err)`)
2. The agent's `processStreamEvents` function receives the error from the `iter.Seq2[StreamEvent, error]` iterator (`for event, err := range events`)
3. The agent loop terminates and returns the error to the caller
4. The session sets `pausedOnError = true` and notifies the user via a system error message
5. The UI displays the error message to the user

## Queue Pause on Error

When a provider error (network failure, API error, content filter, etc.) occurs during prompt processing, the task queue **pauses** instead of moving on to the next queued task. This prevents cascading failures and gives the user control over recovery.

### Why pause instead of continue

Without pausing, a network outage would cause every queued prompt to fail in sequence, each adding an orphaned user message (with no assistant reply) to the conversation history. This corrupts context for subsequent API calls.

### How it works

1. `handleUserPrompt` (or `resendPrompt` / `summarize`) detects the error from `processPrompt`
2. Sets `pausedOnError = true` on the session
3. `waitForNextTask` blocks — it won't dequeue the next task while `pausedOnError` is true
4. Remaining queued tasks stay in the queue (visible via Ctrl+Q)
5. The user can now:
   - `:continue` — resend the failed prompt (or `:continue skip` to skip it and resume the queue)
   - `:model_set` — switch to a different model, then `:continue`
   - Type a new prompt — submits a new task, clears the pause if the queue was empty
   - `:cancel_all` — clear the queue and the pause
   - Inspect the queue with Ctrl+Q

### Command dispatch

Commands are split into two paths:

**Immediate commands** — run immediately on the input goroutine, regardless of queue state:
`:cancel`, `:cancel_all`, `:model_set`, `:model_load`, `:taskqueue_get_all`, `:taskqueue_del`, `:thinking`

**Deferred commands** — enqueued at the front of the task queue via `submitDeferredCommand`, which rejects if a task is already running (unless paused on error):
`:continue`, `:summarize`, `:save`, and all others

Note: `:quit` / `:q` is handled directly by each adaptor and never reaches the session.

Deferred commands run on the `taskRunner` goroutine with a cancellable context, so `:cancel` can interrupt them at any time. They are placed at the front of the queue so they run ahead of any accumulated user prompts.

### Implementation

`internal/agent/session.go`:
- `pausedOnError` field on `Session`
- `waitForNextTask` checks `s.pausedOnError` in its loop condition
- `submitDeferredCommand` guards: rejects if `inProgress && !pausedOnError`, then calls `enqueueTask`
- `submitTask` clears `pausedOnError` when the queue was empty (before `enqueueTask` signals, so `taskRunner` sees consistent state)

`internal/agent/session_io.go`:
- `handleUserPrompt`, `resendPrompt`, and `summarize` set `pausedOnError = true` on error
- `cancelAllTasks` clears `pausedOnError` and signals the condition variable
- `handleContinue` clears `pausedOnError`, then either calls `resendPrompt` (no args) or skips and resumes the queue (`skip`)
- `resendPrompt` re-sets `pausedOnError` on failure

`internal/agent/command_registry.go`:
- `:continue` is dispatched via `dispatchCommand` → `handleContinue`

## Testing

Error handling is tested in `internal/llm/providers/providers_test.go`:

| Test | Verifies |
|------|----------|
| `TestOpenAINetworkError` | Unexpected finish reasons trigger error |
| `TestOpenAIContentFilter` | `content_filter` triggers error |
| `TestOpenAILengthFinishReason` | `length` is valid, not an error |
| `TestAnthropicRefusalStopReason` | `refusal` triggers error |
| `TestAnthropicUnknownStopReason` | Unknown stop reasons trigger error |
| `TestAnthropicValidStopReasons` | All valid reasons (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`) don't trigger errors |
