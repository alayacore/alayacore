# Step Messages

A **step** is one LLM round trip. It produces 1 or 2 messages in the conversation history:

- `[assistantMsg]` — text-only response (no tool calls)
- `[assistantMsg, toolResultMsg]` — tool calls followed by tool results

## Flow

1. `Stream()` calls the provider and processes streaming events via `streamEvents()`
2. `streamEvents()` handles all tools concurrently — tools needing no confirmation execute immediately in goroutines, while tools needing confirmation block on per-tool channels until the user responds. All results are collected into one slice. It then re-orders tool results by ID to match the assistant message's content order and returns 1–2 pre-assembled messages (`[assistantMsg]` or `[assistantMsg, toolResultMsg]`).
3. `Stream()` appends the returned step contents to `allContents`, fires `OnStepFinish(allContents, stepUsage)`, and checks whether the task is done.
4. The session publishes the *new* parts of this step to run() as a delta
   (`stepFinishEvent.NewParts`), so `:save` mid-task sees all completed
   steps. The full history is only replaced wholesale at task completion
   via `taskResultCh`. `Stream()` also returns the final messages as a
   convenience.
5. Loop repeats until the model responds with text only (no tool calls) or the response is truncated.

## Key details

- **The step's content parts are assembled by `llm.Agent`** (`assemble.go`) from the stream: reasoning, text and tool calls are all parts of one record, built the same way whether the step finished or was cut. `StepCompleteEvent` itself carries only usage and stop reason.
- **Tool execution** starts during streaming for all tools — tools needing no confirmation execute immediately via goroutines, while tools needing confirmation block on per-tool channels until the user responds. All results flow through a shared channel and are collected in a single loop. Results are matched to their tool call by ID.
- **Result matching is strict**: `attachToolResults` requires every tool call in a finished step to have exactly one result. A tool call whose result never arrived (e.g. a non-conforming provider that reuses an empty tool-call ID) is surfaced as an error — never a nil entry in the conversation history, which would panic later in `GroupByRole`.
- **All tool results** of a step are collected into one tool-result group in the domain history (a single run of `RoleTool` parts, ordered by `attachToolResults`). The *wire* shape is not one message everywhere: Anthropic emits a single `user` message holding N `tool_result` blocks, while OpenAI emits N separate `role:"tool"` messages — one per `tool_call_id` — plus, when any result carried media, one promoted `user` message after them. See docs/providers.md → "Tool results".
- **What a failed, canceled, or panicking step contributes:** the same record the step would have produced, assembled by the same code — reasoning and text as streamed, plus every `tool_use`/`tool_result` pair whose result arrived; calls still unanswered are dropped, since a `tool_use` without its result cannot be replayed. This is not a salvage path in the sense of a second implementation: `openai_salvage_parity_test.go` requires a cut step and a finished step over one stream to land identical history.
- **Why partial content is kept rather than discarded:** it is what the model emitted and what the user already watched arrive — the TUI's windows are built from the same fragments and are never removed on cancel, so dropping them leaves the display holding content that history and, after save-and-reopen, the session file do not. A bare `tool_use` with no reasoning is also a turn the model never sent: `openaiConvertContents` pads `reasoning_content` rather than omitting the key, because DeepSeek-family endpoints expect reasoning on a tool-call message. Keeping a partial answer matches what `max_tokens` truncation already does — the partial text is retained, which is what lets `:continue` resume the turn.
- **Incomplete tool calls on cancel:** messages may still end up with `tool_use` lacking a `tool_result` if a tool never returned; `cleanIncompleteToolInputs()` removes those to prevent API errors on the next request.
- **Tool result message ordering:** `OnStepFinish` receives complete step messages including both the assistant message (with tool calls) and the tool result message. `OnToolOutput` should only send UI notifications — the agent loop handles message assembly.
