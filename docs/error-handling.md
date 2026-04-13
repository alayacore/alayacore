# Error Handling

This document describes how AlayaCore handles errors from LLM API providers during streaming responses.

> **Note**: This document was originally written for the `llmcompat` package existed and some references are that openai.go`
> function `handleEvent`. The code has since moved to `providers/openai.go` and `handleEvent`.
> The Anthropic error handling has moved from `anthropic.go` `handleMessageDelta`.

> The specific error messages below are based on current code behavior.

>

## Overview

Both OpenAI and Anthropic APIs use finish reasons (or stop reasons) to indicate why a streaming response ended. AlayaCore monitors these reasons to detect errors and prevent silent failures.

## OpenAI Provider

### Valid Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `stop` | Model finished naturally (end of response, hit a stop sequence, or normal completion) | Normal completion - process the full message |
| `length` | Hit `max_tokens` / `max_completion_tokens` limit. Response is truncated | Valid - response may be partial but not an error |
| `tool_calls` | Model wants to call one or more tools/functions | Execute the tool(s) and send results back in the next message |
| `""` (empty) | No finish reason yet (streaming in progress) | Continue processing |

### Error Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `content_filter` | Content was blocked or partially omitted by OpenAI's safety filters | **Error** - emit `StreamErrorEvent` with message "content blocked by safety filter" |
| Any other value | Unknown or unexpected finish reason | **Error** - emit `StreamErrorEvent` with unexpected reason |

### Implementation

Error detection is implemented in `internal/llm/providers/openai.go` in the `handleEvent` function:

```go
if choice.FinishReason == "content_filter" {
    return fmt.Errorf("content blocked by safety filter")
}
if choice.FinishReason != "" && choice.FinishReason != "stop" &&
    choice.FinishReason != "length" && choice.FinishReason != "tool_calls" {
    return fmt.Errorf("stream finished with unexpected reason: %s", choice.FinishReason)
}
```

### What to Do When Errors Occur

- **`content_filter`**: The request violated content policy. Review the prompt and modify to comply with safety guidelines. May contain partial content.
- **`length`**: Not an error, but response is truncated. Increase `max_tokens` if you need the complete response, or handle partial output.
- **Other errors**: Check API documentation or contact support.

---

## Anthropic Provider

### Valid Stop Reasons
| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `end_turn` | Model reached a natural stopping point | Normal completion - most common for regular responses |
| `max_tokens` | Hit the `max_tokens` limit you specified | Valid - response is truncated but not an error |
| `stop_sequence` | Hit one of your custom `stop_sequences` | Valid - model stopped at the specified sequence |
| `tool_use` | Model wants to call a tool (tool_use content block is complete) | Execute the tool and send results back |
| `pause_turn` | Used with server-side tool execution or long-running turns (e.g., agent loops) | Valid - part of extended conversation flow |

### Error Stop Reasons
| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `refusal` | Model refused to respond (e.g., due to safety/content policy) | **Error** - emit `StreamErrorEvent` with message "model refused to respond: content policy violation" |
| Any other value | Unknown or unexpected stop reason | **Error** - emit `StreamErrorEvent` with unexpected reason |

### Implementation

Error detection is implemented in `internal/llm/providers/anthropic.go` in the `handleMessageDelta` function:

```go
if stopReason == "refusal" {
    return fmt.Errorf("model refused to respond: content policy violation")
}
if stopReason != "" && stopReason != "end_turn" && stopReason != "max_tokens" &&
    stopReason != "stop_sequence" && stopReason != "tool_use" && stopReason != "pause_turn" {
    return fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason)
}
```

### What to Do When Errors Occur
- **`refusal`**: The request violated content policy. Review and modify the prompt to comply with safety guidelines.

---

## Error Propagation

When an error finish reason is detected:

1. The provider returns an error from the event handler
2. The error is wrapped in a `StreamErrorEvent` and sent through the event channel
3. The agent's `processStreamEvents` function receives the error event
4. The agent loop terminates and returns the error to the caller
5. The UI displays the error message to the user

This prevents silent failures where:
- Incomplete responses are processed as if complete
- The agent loop stops without explanation
- Users see "context: 0" in the status bar with no error message

## Testing

Error handling is tested in `internal/llm/providers/providers_test.go`:

### OpenAI Tests
- `TestOpenAINetworkError` - Verifies unexpected finish reasons (e.g. `network_error`) trigger error
- `TestOpenAIContentFilter` - Verifies `content_filter` finish reason triggers error
- `TestOpenAILengthFinishReason` - Verifies `length` is treated as valid (not an error)

### Anthropic Tests
- `TestAnthropicRefusalStopReason` - Verifies `refusal` stop reason triggers error
- `TestAnthropicUnknownStopReason` - Verifies unknown stop reasons trigger error
- `TestAnthropicValidStopReasons` - Verifies all valid stop reasons (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`) don't trigger errors

