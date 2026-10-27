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
| `pause_turn` | Server-side extended task | Valid — part of extended flow |

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
- **Auto-summarization** — the summarization LLM call failed (`Auto-summarization failed: ...`). A reply with no summary text is asked once more before it is reported (see [context-tracking.md](context-tracking.md#when-the-model-writes-no-summary-text)); a retry that also writes no text arrives here. The task ends rather than continuing on the uncompressed history: the request that would follow is the one the threshold exists to prevent, and where the provider enforces `prompt + max_tokens` it is that request the user sees rejected a step later, with no mention of the summarize that failed to prevent it. This arrives at the start of a task or part-way through one (see [context-tracking.md](context-tracking.md)).

## Automatic Retry of Transient Failures

A provider that is rate-limiting (429), overloaded (529), or briefly unavailable
(408/500/502/503/504) is retried automatically before the task is allowed to
fail — and so is a connection that failed before any response: a timeout, a
reset, or an EOF where the response should have been (`Post "...": EOF`, which
is what a local server leaves behind when it is killed mid-request). A transient
failure therefore stays invisible unless it outlives the retry budget.

**Why the ambiguity does not stop the retry.** That last class is ambiguous: the
provider may have generated — and billed — the answer before the response was
lost. Withholding the retry does not buy those tokens back. The user's only
alternative is [`:continue`](#continue), and because a failed pre-stream step
contributes nothing to the history, `:continue` re-sends the **identical**
request — the same tokens, plus a human, plus a wait (`:continue` is never
cheaper: past the auto-summarize threshold it summarizes first). So the ambiguity
is a reason to keep the retry bounded, visible and interruptible, not a reason to
skip it.

**Where.** `llm.Agent.sendWithRetry` (`internal/llm/agent.go`) re-sends a step's
**attempt** — its request *and* its stream — as one unit, and the safety argument
is a single condition: the attempt delivered nothing. No event reached the
adapter, no content or tool call was published, no history ID was minted, nothing
entered history. Re-sending is therefore idempotent: it cannot duplicate content,
re-run a tool whose side effects already happened, or pollute history.

Two failures meet that condition, and both are re-sent. One is a request that
failed inside `Provider.StreamMessages`, before anything could stream. The other
is a stream that ended with an error **before it recorded anything** — a 200 whose
body stopped, a gateway that answered with nothing usable, an in-band error as the
opening message — where no block was opened, so the step's record is empty and the
adapter has seen nothing. That second case is retried by construction, not by
classification: a stream failure never arrives inside a `*url.Error` and carries
no status, so `IsRetryable` could only ever say no (`streamBreakError` is what
tells the loop the record was empty). The price is the one already accepted
elsewhere: a permanent in-band error — an `invalid_request` the provider sends as
its opening message — spends the budget before it is reported, where a wrong port
spends it too.

The condition is asked of the **record**, not of the event count, and the
difference is not academic: a provider is free to declare the end of blocks it
never opened before it reports the error. OpenAI closes all three — reasoning,
text, tool calls — so that a body which simply stops is not mistaken for a step
that finished; those boundaries open no block, mint no history ID and reach no
callback. Counting events looked equivalent and left that shape unreported and
un-retried; `streamAssembler.empty` is what answers the question.

An error that arrives *after* an event has been delivered is surfaced as-is.
Content the user already watched stream in cannot be taken back, so re-sending
would duplicate it.

**Classification is structural, not a string match.** The provider returns a
typed `*llm.APIError` (`internal/llm/retry.go`) carrying the HTTP status and the
parsed `Retry-After` hint; `llm.IsRetryable` decides from those. A status is an
allowlist and has to be: it is a decision the server made, and most decisions are
final. In: 408, 425 (the server asking to be asked again later), 429, 500, 502,
503, 504, 529, and Cloudflare's 520–524 — the codes a gateway in front of the
provider returns where 502/504 would be if the origin were reachable. Out:
501/505, which are permanent; 400/401/403/404/413, which describe a request that
would be rejected identically next time; and 525/526, which are a TLS
misconfiguration no retry can fix. A `Retry-After` on any of them — 425 is the
usual carrier — wins over the backoff, so the wait is the server's, not ours.

A transport failure carries no decision at all, so it is decided by the one
question that matters — could a second attempt get a different answer? — and the
default is **yes** (`transportFailed`, same file). That default is deliberate.
The vocabulary for "the answer is not here" is open: kernels, resolvers, proxies,
gateways and HTTP/2 each have their own, and the last three fixes each added a
shape the previous classification had missed (`io.EOF`, then `net.ErrClosed`,
then `ECONNREFUSED`). Enumerating it is a treadmill. What a retry cannot change
is closed and small, so that is what gets written down: a certificate that does
not verify, an https URL answered by a plain-HTTP server, and a name that does
not resolve — plus anything that never reached the transport at all, which is
structural to detect because `http.Client` wraps everything it returns in
`*url.Error`. The price is stated once: a wrong port or host now costs the budget
— three notices, about seven seconds — before reporting the same error, which is
the accepted price of a local model server that is restarting.
`context.Canceled`/`context.DeadlineExceeded` are never retried, and a cancel
that beats its own connection error is reported as the cancellation (no notice) —
including on the last attempt, where the budget has already run out: what the
user did outranks the transport error the budget happened to end on.

One shape still needs the argument spelled out, because its name says the
opposite. `net.ErrClosed` means a socket was written to after it was closed, and
on this path it is **not** "we closed it on purpose". The closers reachable here
are `net/http`'s read half, reacting to a peer that has gone, and the request's
own context ending — and the second is reported as the cancellation, not as this
shape. A peer that leaves while the body is still going out is reported in
whichever costume wins the race: if the body fit in the kernel's buffers the
write is already over, and the missing response arrives as a bare `EOF`; if it
did not, the read half notices the peer first, closes the connection underneath
the writer, and the write reports `readfrom tcp …: write tcp …: use of closed
network connection`. One peer event, two costumes, decided by how long the write
stays in flight — that is, by the size of the request. The earlier report was
the `EOF` costume; this one is a summarize, the largest body the program sends
(`Auto-summarization failed: provider stream failed: failed to send request:
Post "…": readfrom tcp …: … use of closed network connection`).

**Timing.** `Retry-After` wins when the server sent one; otherwise exponential
backoff (1s, 2s, 4s…), both capped. Retries are bounded (`maxRetries`), so a
persistent failure cannot loop forever. The wait is a `select` on the context,
so `:cancel` interrupts a backoff immediately.

**Visibility.** Each retry is announced **before** the wait through
`StreamCallbacks.OnRetry`, which the session turns into an SM `notify` (not an
`error` — the task has not failed):

```
provider rate limited the request (status 429) — retrying in 1s (retry 1/3)
provider connection failed — retrying in 1s (retry 1/3)
provider stream ended early — retrying in 1s (retry 1/3)
```

If every retry is exhausted, the ordinary failed-step path reports the final
failure as it always has. Because the retry lives in the session/agent layer,
all four adapters inherit it.

**What this does not cover.** A connection that goes silent — established,
request delivered, then no bytes and no FIN/RST — has no client-side deadline,
and that is deliberate: any "wait for the response header" limit is a guess, and
a wrong guess fails a request the provider had already generated (and billed) in
exactly the deployments nobody tests — the big model on the slow box. The gap is
also narrower than it sounds. `DefaultTransport` already bounds connect (a 30s
dialer), the TLS handshake (10s) and idle reuse (90s), and a peer whose kernel
died with our bytes in flight gives up once its retransmit budget runs out,
surfacing as a timeout — which *is* retried, above. What remains is a peer whose
kernel answers while its application never does: rare, visible in that server's
own logs, and `:cancel` ends it. Should it ever become necessary, the shape that
cannot misfire is an opt-in limit (0 = off) or a notify-only watchdog that
reports the silence without failing the request; `http.Client.Timeout` is the
one thing not to reach for, since it cuts a long generation mid-answer. Note
that `internal/llm/providers/proxy.go` builds a zero-value `http.Transport`, so
the proxy path has no connect, handshake or idle limit at all — the first place
to look if a hang is ever reported behind a proxy.

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
- Type a new prompt — sent as a new user message; if a task is still running, it is spliced into that task at its next step boundary instead (see [architecture.md](architecture.md#steering--a-prompt-that-arrives-mid-task))

The session is idle as soon as a task fails, which is what makes the two commands
above usable: a failed task does **not** start anything on its own. Anything the
user had steered into it — never delivered, because the task did not land — is
discarded, with a `steering` message (phase `dropped`, reason `failed`) saying so; retyping it is the user's call. That is
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
| `TestIsRetryable` | Every status code in (and out of) the retryable set; cancel/deadline are not retried; a transport failure is retried, including an errno the classifier does not name, while a bad certificate, an https URL answered by plain HTTP, a name that does not resolve and an error that never reached the transport are not |
| `TestRetryReason` | The user-facing notice text: 425, 429, the gateway family, 529, other statuses, and the one wording every connection failure shares |
| `TestRetryDelay` | Exponential backoff, `Retry-After` wins, both capped (no overflow) |
| `TestAgentRetriesTransientThenSucceeds` | 429 then 503 then success: 3 calls, 2 notices with correct numbering |
| `TestAgentRetriesConnectionFailure` | `failed to send request: Post "...": EOF` and `readfrom ...: write ...: use of closed network connection` are both retried and announced as `provider connection failed` |
| `TestAgentCanceledConnectionFailureIsNotRetried` | A cancel that beats the connection error is reported as `context.Canceled`, with no re-send and no notice |
| `TestAgentCancelOnLastAttemptReportsCancellation` | A cancel landing on the last attempt is reported as `context.Canceled` too, not as the transport error the spent budget ended on |
| `TestAgentRetriesStreamThatRecordedNothing` | A stream error from an attempt that recorded nothing is re-sent and announced as `provider stream ended early`, with the step announced once — and the error is a bare `io.EOF`, which the classifier alone would refuse |
| `TestAgentRetriesStreamWithBoundariesForUnopenedBlocks` | Boundaries for blocks the stream never opened (the OpenAI shape) leave the record empty, so the attempt is still re-sent — the gate asks the record, not whether an event arrived |
| `TestAgentDoesNotRetryStreamThatDiedAfterContent` | A stream error after a block was opened is surfaced in the provider's own words: no retry, no notice, no pre-stream prefix |
| `TestAgentRetriesStreamBreakUntilTheBudgetIsSpent` | A stream that keeps dying with the record empty spends the budget and reports the provider's error unchanged |
| `TestAgentRetriesExhausted` | A permanent 429 stops after `1+maxRetries` calls and surfaces the `*APIError` |
| `TestAgentDoesNotRetryPermanentError` | A 401 is not retried |
| `TestAgentRetryCanceledDuringBackoff` | A cancel during the wait stops before the next request (`context.Canceled`) |
| `TestParseRetryAfter` / `TestDoRequestReturnsTypedAPIError` | Provider parses `Retry-After` (seconds and HTTP-date) and returns a typed `*llm.APIError` |
| `TestStreamMessagesConnectionClosedBeforeResponseIsRetryable` | A server that closes the connection without responding produces an error `llm.IsRetryable` accepts — over a real socket, not a hand-built error |
| `TestStreamMessagesPeerGoneWhileSendingBodyIsRetryable` | The same peer event while the request body is still going out — small body (`EOF`) and whole-conversation body (the field's `use of closed network connection`) both stay retryable, over a real socket |
| `TestStreamMessagesConnectionRefusedIsRetryable` | A provider that is not listening yet — a local server mid-restart — produces a refused connect `llm.IsRetryable` accepts, over a real socket |

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
