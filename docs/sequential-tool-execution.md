# Tool Execution: Sequential vs Parallel

AlayaCore executes tool calls **sequentially** — one at a time, in order — even though LLM APIs support returning multiple tool calls in a single response. This is an intentional design decision.

## How It Works

When the LLM returns multiple `tool_use` blocks in one response, the agent's `executeTools()` method processes them one by one in a `for` loop. All results are collected into a single tool result message and sent back to the LLM together. This is correct behavior — both Anthropic and OpenAI APIs expect all tool results for a step in a single response.

See `internal/llm/agent.go` → `executeTools()`.

## Why Sequential

### 1. LLM Inference Dominates Latency

In a typical agentic step, time is spent roughly like this:

```
LLM inference (streaming)    ~3-10s
Tool execution               ~50-500ms
LLM inference (next step)    ~3-10s
```

Tool execution is a small fraction of total latency. The bottleneck is always LLM API round-trips. Parallelizing tool execution doesn't address the dominant cost.

### 2. Most Tools Mutate State

4 out of 5 tools have side effects:

| Tool | Side Effects | Parallelizable? |
|------|-------------|-----------------|
| `read_file` | None | ✅ Safe |
| `search_content` | None | ✅ Safe |
| `edit_file` | Mutates files | ⚠️ Risky |
| `write_file` | Creates/overwrites files | ⚠️ Risky |
| `execute_command` | Anything can happen | ❌ Dangerous |

The LLM frequently mixes reads and writes in the same response block. Running those in parallel creates subtle bugs — two `edit_file` calls on the same file, or a `execute_command` call that depends on a prior `write_file`, would produce unpredictable results.

### 3. Sequential Is Simpler to Reason About

- **No race conditions** — Tools that touch the filesystem or run commands can't interfere with each other.
- **Deterministic errors** — When tool 3 of 5 fails, the error is unambiguous. No partial results, no goroutine cleanup.
- **Clean cancellation** — The loop checks `ctx` between iterations naturally. Goroutines require `errgroup` or similar patterns.
- **Testable** — Sequential behavior is deterministic. Tests don't flake due to scheduling order.

### 4. The LLM Doesn't Guarantee Independence

The LLM returns multiple tool calls because the API format supports it, not because it has verified they are truly independent. Treating them as parallel-safe by default would be an incorrect assumption.

## When Parallel Would Make Sense

Parallel tool execution is worthwhile when:

- Tools are **slow and independent** (e.g., batch HTTP requests to different services)
- The tool catalog is **large and dominated by read-only operations**
- Tool execution time is a **significant fraction** of total step latency

None of these apply to AlayaCore's current tool set. All tools run locally, execute quickly, and most have side effects.

## Potential Future Approach

If the tool catalog grows to include slow, read-only operations, the cleanest approach would be a **per-tool concurrency hint**:

```go
type Tool struct {
	Definition   ToolDefinition
	Execute      func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error)
	SafeToConcur bool // opt-in per tool
}
```

Tools like `read_file` would opt in. `executeTools()` would group calls: run all `SafeToConcur` tools concurrently, then run the rest sequentially. This avoids a blanket parallel strategy while allowing safe concurrency where it helps.
