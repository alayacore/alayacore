# Error Handling

How AlayaCore detects and propagates errors from LLM API providers during streaming responses.

## Overview

Both OpenAI and Anthropic APIs use finish reasons (or stop reasons) to indicate why a streaming response ended. AlayaCore monitors these reasons to detect errors and prevent silent failures — cases where incomplete responses could be processed as if complete, the agent loop stops without explanation, or users see "context: 0" with no error message.

## OpenAI Provider

### Valid Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `stop` | Normal completion | Process the full message |
| `length` | Hit `max_tokens` limit, response is truncated | **Error** — `ErrResponseTruncated` |
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
| `max_tokens` | Hit token limit | **Error** — `ErrResponseTruncated` |
| `stop_sequence` | Hit custom stop sequence | Valid |
| `tool_use` | Tool call complete | Execute tool and continue |
| `pause_turn` | Server-side extended turn | Valid — part of extended flow |

### Error Stop Reasons

| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `refusal` | Model refused (content policy) | **Error** — `"model refused to respond: content policy violation"` |
| Any other value | Unknown reason | **Error** — `"stream finished with unexpected stop reason: ..."` |

### Implementation

`internal/llm/providers/anthropic.go` → `handleStopReason`:

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

### Provider-level errors (content filter, refusal, unknown reasons)

1. The provider returns an error from the event handler via the streaming iterator's error parameter (`yield(nil, err)`)
2. The agent's event loop in `streamEvents` receives the error from the `iter.Seq2[StreamEvent, error]` iterator (`for event, err := range events`)
3. The agent loop terminates and returns the error to the caller
4. The UI displays the error message to the user

### Agent-level errors (truncation, max steps)

Truncation (`max_tokens` / `length`) is **not** an error at the provider level — the response is valid, just incomplete. The provider includes the stop reason in `StepCompleteEvent.StopReason`.

The agent detects truncation in `streamEvents` and checks for it in `Stream()`. Partial messages are still included in the `StreamResult` so the caller can inspect what was generated before the cutoff.

```go
// In streamEvents:
case StepCompleteEvent:
	stepContents = assembler.parts()
	stepUsage = e.Usage
	if e.StopReason == "max_tokens" || e.StopReason == "length" {
		truncated = true
	}
```

### Session-level errors (persistence, summarization)

Failures that put the conversation at risk are reported to the adapter as
SM system errors (`type: "error"`) — the same channel as task failures,
so adapters treat them consistently (e.g. terseio sets exit code 1):

- **Auto-save** — writing the session file when a task ends (success or failure — including after a failed step) failed (`Auto-save failed: ...`)
- **Pre-summarize backup** — the timestamped backup written before auto-summarization failed (`Failed to create pre-summarize backup: ...`); without it the original conversation is unrecoverable after summarization
- **Auto-summarization** — the summarization LLM call failed (`Auto-summarization failed: ...`). The turn ends rather than continuing on the uncompressed history: the request that would follow is the one the threshold exists to prevent, and where the provider enforces `prompt + max_tokens` it is that request the user sees rejected a step later, with no mention of the summarize that failed to prevent it. This arrives at the start of a task or part-way through one (see [context-tracking.md](context-tracking.md)).

## Automatic Retry of Transient Failures

A provider that is rate-limiting (429), overloaded (529), or briefly unavailable
(408/500/502/503/504, plus transport timeouts) is retried automatically before
the turn is allowed to fail — so a transient failure stays invisible unless it
outlives the retry budget.

**Where.** `llm.Agent.sendWithRetry` (`internal/llm/agent.go`) re-sends the
step's request. A step's request is issued **synchronously inside**
`Provider.StreamMessages`, so a failure there means nothing has streamed yet: no
content, no tool call, no history ID. Re-sending is therefore idempotent — it
cannot duplicate content, re-run a tool whose side effects already happened, or
pollute history. An error that arrives *after* the stream has started (e.g. an
Anthropic `error` event) may follow content the user already watched stream in,
so it is surfaced as-is rather than retried.

**Classification is structural, not a string match.** The provider returns a
typed `*llm.APIError` (`internal/llm/retry.go`) carrying the HTTP status and the
parsed `Retry-After` hint; `llm.IsRetryable` decides from those. The retryable
statuses are listed explicitly — 501/505 are permanent, so retrying them only
delays the error. `context.Canceled`/`context.DeadlineExceeded` are never
retried.

**Timing.** `Retry-After` wins when the server sent one; otherwise exponential
backoff (1s, 2s, 4s…), both capped. Retries are bounded (`maxRetries`), so a
persistent failure cannot loop forever. The wait is a `select` on the context,
so `:cancel` interrupts a backoff immediately.

**Visibility.** Each retry is announced **before** the wait through
`StreamCallbacks.OnRetry`, which the session turns into an SM `notify` (not an
`error` — the turn has not failed):

```
provider rate limited the request (status 429) — retrying in 2s (retry 1/3)
```

If every retry is exhausted, the ordinary failed-step path reports the final
failure as it always has. Because the retry lives in the session/agent layer,
all four adapters inherit it.

## Error Recovery

When an error occurs during prompt processing, the prompt **fails** instead of continuing.

Errors include:
- **Provider errors** — network failure, API error, content filter, refusal
- **Max steps exceeded** — agent loop hit `--max-steps` limit without final response (error: `"agent loop exceeded maximum steps"`)
- **Response truncated** — model hit output token limit (error: `"response truncated: hit output token limit"`)

### Recovery

The user can:
- `:continue` — retry the last prompt
- `:model_set` — switch to a different model, then `:continue`
- Type a new prompt — sent as a new user message; if a task is still running, it is spliced into that turn at its next step boundary instead (see [architecture.md](architecture.md#steering--a-prompt-that-arrives-mid-turn))

The session is idle as soon as a turn fails, which is what makes the two commands
above usable: a failed turn does **not** start anything on its own. Anything the
user had steered into it — never delivered, because the turn did not land — is
discarded, with an SM `notify` saying so; retyping it is the user's call. That is
the one place the session drops input, and it is never silent.

### `:continue`

`:continue` runs in a separate goroutine — it can be canceled with `:cancel` while the LLM call is in progress. It resends the last prompt to the LLM. See [commands.md](commands.md) for details.

No queue management needed.

## Testing

Error handling is tested in `internal/llm/providers/providers_test.go`:

| Test | Verifies |
|------|----------|
| `TestOpenAINetworkError` | Unexpected finish reasons trigger error |
| `TestOpenAIContentFilter` | `content_filter` triggers error |
| `TestOpenAILengthFinishReason` | `length` doesn't trigger provider error, `StopReason` is populated |
| `TestAnthropicRefusalStopReason` | `refusal` triggers error |
| `TestAnthropicUnknownStopReason` | Unknown stop reasons trigger error |
| `TestAnthropicValidStopReasons` | All valid reasons don't trigger errors, `StopReason` is populated |

Max steps and truncation behavior is tested in `internal/llm/agent_maxsteps_test.go`:

| Test | Verifies |
|------|----------|
| `TestAgentMaxStepsExceeded` | Agent returns `ErrMaxStepsExceeded` after reaching limit, with accumulated messages |
| `TestAgentCompletesWithinMaxSteps` | Agent completes normally when final response arrives before limit |
| `TestAgentTruncatedMaxTokens` | Agent returns `ErrResponseTruncated` for `max_tokens`, partial messages preserved |
| `TestAgentTruncatedLength` | Agent returns `ErrResponseTruncated` for `length`, partial messages preserved |
| `TestAgentNoTruncationOnEndTurn` | Agent does not return `ErrResponseTruncated` for `end_turn` |

Automatic retry is tested in `internal/llm/retry_test.go`,
`internal/llm/agent_retry_test.go`, and
`internal/llm/providers/base_retry_test.go`:

| Test | Verifies |
|------|----------|
| `TestIsRetryable` | Every status code in (and out of) the retryable set; cancel/deadline are not retried; transport timeouts are |
| `TestRetryDelay` | Exponential backoff, `Retry-After` wins, both capped (no overflow) |
| `TestAgentRetriesTransientThenSucceeds` | 429 then 503 then success: 3 calls, 2 notices with correct numbering |
| `TestAgentRetriesExhausted` | A permanent 429 stops after `1+maxRetries` calls and surfaces the `*APIError` |
| `TestAgentDoesNotRetryPermanentError` | A 401 is not retried |
| `TestAgentRetryCanceledDuringBackoff` | A cancel during the wait stops before the next request (`context.Canceled`) |
| `TestParseRetryAfter` / `TestDoRequestReturnsTypedAPIError` | Provider parses `Retry-After` (seconds and HTTP-date) and returns a typed `*llm.APIError` |

## Error Message Construction (`fmt.Errorf` Chaining)

When wrapping errors with `fmt.Errorf("...: %w", ...)`, each layer must add **only** the information it knows that lower layers do not. Never repeat information already present in the wrapped error.

### Principle

**Each layer adds what the layer below doesn't have. Never repeat.**

### Layer Responsibilities

| Layer | Knows | Adds | Why |
|-------|-------|------|-----|
| Transport (HTTP/stdio) | wire-level failure | `write: connection reset` | Transport doesn't know server name |
| `sendRequest` | request failure | `no transport` / `marshal params: ...` / `ctx.Err()` | Callers always wrap with server name |
| `stateError` | state info | `not ready (state=5)` / `server connection lost` | Callers always wrap with server name |
| adapter (handshake) | version negotiation | `protocol version mismatch: server returned "X", configured "Y"` | Doesn't know server name |
| `Connect` | connection failure | `already connecting` / `not ready` | Callers always wrap with server name |
| `connectServer` | server name (non-OAuth) | `"server": <error>` | Lower layer (`Connect`) doesn't include it |
| `connectOAuth` | server name (OAuth steps) | `"server": pkce: <error>` | Lower layer (auth) doesn't include it |
| `connectWithOAuthToken` | server name + reconnect | `"server": connect after auth: <error>` | Lower layer (`Connect`) doesn't include it |
| `listAllPages` | op name + server name | `"server": list tools: <error>` | Lower layer didn't include either |
| `CallTool` | tool name + server name | `"server": call greet: <error>` | Lower layer didn't include either |
| `discoverCapabilities` | nothing new | passes `err.Error()` directly | Lower layer already has server name + op name |
| `session_loop` | it's an MCP error | `MCP: <error>` | Lower layer is MCP-agnostic |

### Rule of Thumb

- **Lower layers** (transport, `sendRequest`, adapter, `stateError`, `Connect`): Never include server name. They don't know it, or their callers always add it.
- **Middle layers** (`connectServer`, `connectWithOAuthToken`, `listAllPages`, `CallTool`): Add server name + operation context. The lower layer has neither.
- **Upper layers** (`discoverCapabilities`, `session_loop`): Add only what's new. The lower layer already has server name.

### Examples

#### Good: MCP Handshake Error

```
adapter          protocol version mismatch: server returned "2024-11-05", configured "2025-11-25"
↓
negotiateAndHandshake   handshake: <above>
↓
Connect          returns directly (no wrapping)
↓
connectServer  "embedded": <above>
↓
session_loop     MCP: <above>
```

```
MCP: "embedded": handshake: protocol version mismatch: server returned "2024-11-05", configured "2025-11-25"
```

Each piece of information appears exactly once.

#### Good: List Tools with Canceled Context

```
sendRequest      context canceled
↓
listAllPages     "db": list tools: <above>
↓
discoverCapabilities   passes through
↓
session_loop     MCP: <above>
```

```
MCP: "db": list tools: context canceled
```

Server name is added exactly once, by `listAllPages`.

#### Good: CallTool with State Error

```
stateError       not ready (state=5)
↓
CallTool         "my-server": call greet: <above>
```

```
MCP: "my-server": call greet: not ready (state=5)
```

#### Good: Non-OAuth Connect Failure

```
stateError       not ready (state=5)
↓
Connect          returns directly (no wrapping)
↓
connectServer  "my-server": <above>
```

```
MCP: "my-server": not ready (state=5)
```

#### Good: Connect after Auth Failure

```
stateError       not ready (state=5)
↓
Connect          returns directly (no wrapping)
↓
connectWithOAuthToken  "my-server": connect after auth: <above>
```

```
MCP: "my-server": connect after auth: not ready (state=5)
```

### Anti-patterns

```go
// ❌ BAD: Upper layer repeats what lower layer already said ("handshake" twice)
return fmt.Errorf("%q: handshake: %w", c.config.Name, err)
// Lower layer already says "handshake: ..."
// Result: "server": handshake: handshake: ...

// ❌ BAD: Upper layer repeats operation name that lower layer already added
Error: fmt.Sprintf("list tools: %v", err)
// Lower layer (listAllPages) already says list tools: ...
// Result: list tools: "server": list tools: ...

// ❌ BAD: sendRequest adds server name, but every caller adds it too
return fmt.Errorf("%q: no transport", c.config.Name)
// All callers (listAllPages, CallTool, etc.) wrap with server name
// Result: "server": list tools: "server": no transport  (double)

// ❌ BAD: Caller doesn't wrap with server name + operation context
return c.stateError(op)
// stateError no longer includes server name
// Result: not ready...  (missing "server": and "list tools:" context)
```

### Checklist

When writing `fmt.Errorf("...: %w", ...)`, ask:

1. **What new information does this layer know?** (e.g. server name, operation name, protocol version)
2. **Is that information already in the wrapped error?** If yes, omit it.
3. **Will every caller of this function wrap with the same info?** If yes, don't add it here — let callers do it.
4. **Can the user understand the error without this layer's context?** If no, add it — but only the new piece.
