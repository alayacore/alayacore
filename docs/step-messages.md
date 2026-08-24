# Step Messages

A **step** is one LLM round trip. It produces 1 or 2 messages in the conversation history:

- `[assistantMsg]` — text-only response (no tool calls)
- `[assistantMsg, toolResultMsg]` — tool calls followed by tool results

## Flow

1. `Stream()` calls the provider and processes streaming events via `streamEvents()`
2. `streamEvents()` handles all tools concurrently — tools needing no confirmation execute immediately in goroutines, while tools needing confirmation block on per-tool channels until the user responds. All results are collected into one slice. It then re-orders tool results by ID to match the assistant message's content order and returns 1–2 pre-assembled messages (`[assistantMsg]` or `[assistantMsg, toolResultMsg]`).
3. `Stream()` appends the returned step contents to `allContents`, fires `OnStepFinish(allContents, stepUsage)`, and checks whether the task is done.
4. The session receives the *full* history and replaces its own copy. `Stream()` also returns the final messages as a convenience.
5. Loop repeats until the model responds with text only (no tool calls) or the response is truncated.

## Key details

- **`StepCompleteEvent.Contents`** is a `[]ContentPart` (the assistant step contents). Tool calls, text, and reasoning are all content parts within it.
- **Tool execution** starts during streaming for all tools — tools needing no confirmation execute immediately via goroutines, while tools needing confirmation block on per-tool channels until the user responds. All results flow through a shared channel and are collected in a single loop. Results are matched to their tool call by ID.
- **Result matching is strict**: `reorderToolResults` requires every tool call in the step to have exactly one result. A tool call whose result never arrived (e.g. a non-conforming provider that reuses an empty tool-call ID) is surfaced as an error — never a nil entry in the conversation history, which would panic later in `GroupByRole`.
- **All tool results** go into one tool result message per step (required by both Anthropic and OpenAI).
- **Incomplete tool calls on cancel:** When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolInputs()` removes these to prevent API errors on next request.
- **Tool result message ordering:** `OnStepFinish` receives complete step messages including both the assistant message (with tool calls) and the tool result message. `OnToolOutput` should only send UI notifications — the agent loop handles message assembly.
