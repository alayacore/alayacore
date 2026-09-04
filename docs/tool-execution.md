# Tool Execution: Concurrent or Serial, with Per-Tool Confirmation

AlayaCore runs tool calls in one of two modes, chosen per model by
`serial_tool_calls` in `model.conf` (see
[configuration.md](configuration.md#tool-calling-mode-serial_tool_calls)).
Both modes share one implementation of what a tool call does; they differ only
in when calls start.

| Mode | Selected by | Calls run |
|---|---|---|
| **Concurrent** (default) | `serial_tool_calls` omitted or `false` | Each starts as soon as its arguments finish streaming, overlapping with calls still streaming |
| **Serial** | `serial_tool_calls: true` | One at a time, in the order the model made them, after the stream has ended |

Serial mode exists because a great many models and servers have no notion of
parallel tool calls: their side effects have to land in the sequence the model
asked for, and two of them must never be writing files at the same time.

## One Lifecycle, Two Drivers

The life of a single call — ask for confirmation when the tool needs it,
execute, produce the part that answers the call — lives in `llm.Agent.runToolCall`
and nowhere else. The two drivers behind the `toolRunner` interface differ only in
when a call may start: the concurrent one launches a goroutine per call from
inside the streaming loop, the serial one waits for the stream to end. So the two
modes cannot disagree about what a denied, failed, or canceled call means, and a
fix to one cannot silently miss the other.

`streamEvents` itself contains no mode check at all. It calls `runner.add(...)`
when a call's arguments complete and `runner.finish()` after the stream, and
`newToolRunner` is the single place that reads the setting.

`runToolCall` reports a second value: whether the call ever ran. A call canceled
while awaiting confirmation ran nowhere and produced no side effect, so it must
stay out of history — an assistant `tool_use` without the `tool_result` that
answers it is a conversation the next request cannot build. Both drivers drop it
identically, because both are the same function.

Anything that changes what a tool call does belongs in `runToolCall`, never in a
driver.

## How It Works (concurrent mode)

When a `ToolInputPart` event arrives during streaming, the agent calls `ToolNeedsConfirm(toolName)`:

1. **No confirmation needed** — The tool executes immediately in a goroutine. Results flow back through a channel and are appended in receive order.
2. **Confirmation needed** — A goroutine is started that obtains a per-tool confirm channel and blocks until the user responds. Once confirmed, the tool executes in the same goroutine.

All results are collected and then attached by tool call ID, in the order the calls were made.

## How It Works (serial mode)

Completed calls are queued instead of launched — nothing runs, so nothing can
have a side effect. The queue is built in the order the provider declared the
calls (OpenAI sorts by `tool_calls[].index`, Anthropic by block index), which is
the order the model made them and not necessarily the order their argument
fragments arrived in.

Once the stream has ended, the driver runs the queue front to back, each call
finishing before the next begins, and their results go straight into the same
list the concurrent collector fills. History is therefore identical in both
modes: same parts, same order.

Two properties follow from starting after the stream:

- **A stream that fails runs nothing.** In concurrent mode a call could already
  be mid-side-effect when the stream died. Serial mode has no such window.
- **Cancellation between two calls keeps both truths.** The calls that ran are
  salvaged into history with their results; the calls that never started are
  dropped rather than recorded as unanswered. This is the same forgiving pairing
  the concurrent error path uses, reached by returning the context error.

Declining one tool answers that call with an error and lets the remaining calls
run: a refusal is the user's answer to one call, not a stop order for the step.

See `internal/llm/agent.go` → `Stream()`, `streamEvents()` (which contains no
mode check at all), and `internal/llm/agent_execution.go` → `toolRunner`,
`serialToolRunner`, `parallelToolRunner`, `runToolCall`, `executeTool`.

## Execution Strategy

| Phase | Mode | Execution |
|-------|-------|-----------|
| **During streaming** | Concurrent, no confirmation needed | Goroutine per call, results appended and re-ordered by ID |
| **During streaming** | Concurrent, confirmation needed | Goroutine blocks on per-tool confirm channel, then executes |
| **During streaming** | Serial | Calls are queued; nothing executes |
| **After the stream ends** | Serial | One call at a time, in declared order, each awaited before the next |
| **Final** | Both | Re-ordered by tool call ID to match LLM response order |

## Confirmation

`ToolNeedsConfirm` filters which tools need user approval. When a tool needs confirmation, `OnToolConfirm` is called **per tool** and returns a per-tool channel. The caller blocks on this channel — a per-tool goroutine in concurrent mode, the serial driver itself in serial mode. The session stores the channel in a map keyed by tool call ID. When the user responds via `:tool_confirm <id>` or `:tool_decline <id>`, the session looks up the channel in the map and writes the response, unblocking the waiter which then executes the tool.

Each tool has its own confirm channel (buffered, capacity 1), following the same pattern used by MCP OAuth confirmation. This keeps the tool lifecycle continuous — no artificial segmentation between streaming, confirmation collection, and execution.

The TUI adapter processes confirmations sequentially (one dialog at a time). Other adapters can process them in parallel. In serial mode there is at most one confirmation outstanding per step anyway, so a parallel-capable adapter sees the same one-at-a-time flow the TUI enforces.

## Implementation

In concurrent mode all results (from both confirmed and unconfirmed tools) flow through a single shared channel, which is what turns a set of senders finishing in arbitrary order into one list. In serial mode the driver already holds that list in call order. Both feed the same pairing step:

```go
stepContents = attachToolResults(stepContents, results, strict)
```

`attachToolResults` matches each `ToolOutputPart` to its `ToolInputPart` by ID and appends the results in call order, so the record matches the sequence the model asked for. On a step that ended early it runs in forgiving mode and drops calls whose results never arrived, rather than recording a `tool_use` no one answered.

## Error Handling

Tool goroutines run under a **per-stream context** and are tracked with a
`WaitGroup`. If the stream fails mid-execution (provider error, callback
failure, reorder failure), `streamEvents` cancels that context and **waits
for all in-flight tool goroutines to terminate before returning**:

- A tool that is still executing (e.g. `execute_command`, `search_content`)
  is canceled via its context — no tool keeps running and no side effects
  happen after the stream has errored.
- A tool awaiting user confirmation is unblocked by the cancellation — a
  late `:tool_confirm` response can no longer execute the tool against a
  stale context, and the tool produces no result (it never executed).
- Results of tools that already **executed** are **salvaged**: `streamEvents`
  pairs each delivered result with its tool call and folds the
  `[tool_use, tool_result]` pairs into the conversation history (via
  `OnStepFinish`) before returning the error, so a retry or `:continue`
  sees a state consistent with the side effects that happened. A tool
  that never completed (still running when the stream died, dropped by a
  racing result send, or never confirmed) produces no history entry — an
  assistant `tool_use` never appears without its `tool_result`. See
  `attachToolResults` in forgiving mode.

This guarantees `Stream()` never returns while a tool goroutine is still
alive, preventing goroutine leaks and post-error side effects.

The serial mode needs none of that machinery and inherits its guarantees
instead: no goroutine is ever started, so there is nothing to wait for, and a
stream that errors before the driver runs leaves no side effect at all. What
both modes share is the per-stream context handed to each tool — the tool is
still the only thing that decides what cancellation means — and the salvage
path, which is reached from serial by returning the context error like the
concurrent collector does.
