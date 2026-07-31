# Tool Execution: Concurrent with Per-Tool Confirmation

AlayaCore executes tool calls using a **unified concurrent strategy**: all tools run in goroutines during streaming. Tools that need confirmation block on a per-tool channel until the user responds, while tools that don't need confirmation execute immediately.

## How It Works

When a `ToolInputPart` event arrives during streaming, the agent calls `ToolNeedsConfirm(toolName)`:

1. **No confirmation needed** — The tool executes immediately in a goroutine. Results flow back through a channel and are appended in receive order.
2. **Confirmation needed** — A goroutine is started that obtains a per-tool confirm channel and blocks until the user responds. Once confirmed, the tool executes in the same goroutine.

All results are collected and then re-ordered by tool call ID to match the original `stepMessage.Contents` order.

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
stepContents = reorderToolResults(stepContents, results)
```

`reorderToolResults` matches each `ToolOutputPart` to its `ToolInputPart` by ID and places results in the original SSE index order so they match the assistant message's content order.

## Error Handling

Tool goroutines run under a **per-stream context** and are tracked with a
`WaitGroup`. If the stream fails mid-execution (provider error, callback
failure, reorder failure), `streamEvents` cancels that context and **waits
for all in-flight tool goroutines to terminate before returning**:

- A tool that is still executing (e.g. `execute_command`) is canceled via
  its context — no tool keeps running and no side effects happen after the
  stream has errored.
- A tool awaiting user confirmation is unblocked by the cancellation — a
  late `:tool_confirm` response can no longer execute the tool against a
  stale context.
- Results that would have been delivered after the error are dropped; the
  error path does not collect them.

This guarantees `Stream()` never returns while a tool goroutine is still
alive, preventing goroutine leaks and post-error side effects.
