# AlayaCore Project Status

## Current Work
None

## Recent Changes
- Added comprehensive error handling documentation in `docs/error-handling.md`
  - Documents all valid and error finish reasons for OpenAI and Anthropic
  - Includes implementation details and troubleshooting guidance
  - References test coverage for error scenarios

- Fixed error handling for LLM API finish reasons (both OpenAI and Anthropic providers)
  - OpenAI: Detects `content_filter` and other error finish reasons (valid: `stop`, `length`, `tool_calls`)
  - Anthropic: Detects `refusal` stop reason (content policy violation)
  - Errors are propagated as `StreamErrorEvent` to prevent silent failures
  - Prevents incomplete responses from silently stopping the agent loop

- Implemented Anthropic automatic prompt caching via `prompt_cache: true` in model.conf
  - Uses single top-level `cache_control: {"type": "ephemeral"}` field in request
  - Anthropic automatically applies cache breakpoint to last cacheable block
  - Cache breakpoint moves forward as conversations grow (best for multi-turn)
  - Factory passes `PromptCache` config through to Anthropic provider
  - Other providers (OpenAI) ignore the setting as they handle caching automatically

## Critical Gotchas

- **ANSI escape sequences are not recursive**: When styling text with lipgloss (or any ANSI styling), each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work - the outer styling will not wrap the inner styled segments. Always render segments separately, then join them.

- **SwitchModel deadlock**: Don't hold mutex while calling methods that may need the same mutex. Pattern: lock → update fields → unlock → call methods.

- **Terminal scroll position**: `userMovedCursorAway` must be set for J/K scrolling, not just j/k, or scroll position is lost on focus switch.

- **Anthropic prompt caching minimum tokens**: System message must be ≥1024 tokens for caching to activate. Shorter prompts won't be cached even with cache_control set.

- **Anthropic cache_control placement**: Cache control is applied only to the first 2 system messages (default prompt + `--system` extra). Other system messages in conversation history are not modified.

- **Dual system prompt architecture**: `--system` flag appends extra system prompt rather than replacing default. Both prompts become separate system messages, each with cache_control for Anthropic APIs.

- **Prompt cache is per-model config**: `prompt_cache: true` in model.conf enables cache_control markers for Anthropic. Other providers auto-cache and ignore this setting.

- **OpenAI tool call arguments chunking**: OpenAI-compatible APIs split tool call arguments across multiple delta events. Critical: subsequent chunks have `"id": ""` (empty) but correct `"index"`. Must use `index` (not `id`) to associate argument chunks with their tool call. See `openAIStreamState.appendToolCallArgs()` in `openai.go`.

- **OpenAI tool call arguments in requests**: When sending tool calls back in conversation history, arguments must be marshaled to a JSON string (not raw JSON). See `convertMessage()` in `openai.go`.

- **OpenAI reasoning support**: OpenAI-compatible APIs (DeepSeek, Qwen, etc.) use `reasoning_content` field for thinking tokens. Handled by `openai.go`.

- **Tool result message ordering**: `OnStepFinish` callback receives complete step messages. For tool-using steps, this includes both the assistant message (with tool calls) AND the tool result message. The `OnToolResult` callback should only send UI notifications, not append to session messages - the agent loop handles message assembly.

- **Incomplete tool calls on cancel**: When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.