# Tool Execution: Concurrent with Per-Tool Confirmation

AlayaCore executes tool calls using a **unified concurrent strategy**: all tools run in goroutines during streaming. Tools that need confirmation block on a per-tool channel until the user responds, while tools that don't need confirmation execute immediately.

## How It Works

When a `ToolInputPart` event arrives during streaming, the agent calls `ToolNeedsConfirm(toolName)`:

1. **No confirmation needed** — The tool executes immediately in a goroutine. Results flow back through a channel and are appended in receive order.
2. **Confirmation needed** — A goroutine is started that obtains a per-tool confirm channel and blocks until the user responds. Once confirmed, the tool executes in the same goroutine.

All results are collected and then attached by tool call ID, in the order the calls were made.

See `internal/llm/agent.go` → `Stream()`, `streamEvents()`, and `handleStreamedToolInput()`.

## Execution Strategy

| Phase | Tools | Execution |
|-------|-------|-----------|
| **During streaming** | No confirmation needed (`ToolNeedsConfirm` returns false) | Concurrent goroutines, results appended and re-ordered by ID |
| **During streaming** | Confirmation needed (`ToolNeedsConfirm` returns true) | Goroutine blocks on per-tool confirm channel, then executes |
| **Final** | All results | Re-ordered by tool call ID to match LLM response order |

## Confirmation

`ToolNeedsConfirm` filters which tools need user approval. When a tool needs confirmation, `OnToolConfirm` is called **per tool** and returns a per-tool channel. The tool's goroutine blocks on this channel. The session stores the channel in a map keyed by tool call ID. When the user responds via `:tool_confirm <id>` or `:tool_decline <id>`, the session looks up the channel in the map and writes the response, unblocking the goroutine which then executes the tool.

Each tool has its own confirm channel (buffered, capacity 1), following the same pattern used by MCP OAuth confirmation. This keeps the tool lifecycle continuous — no artificial segmentation between streaming, confirmation collection, and execution.

The TUI adapter processes confirmations sequentially (one dialog at a time). Other adapters can process them in parallel.

## Implementation

All results (from both confirmed and unconfirmed tools) flow through a single shared channel and are re-ordered by ID:

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
