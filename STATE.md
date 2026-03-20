# AlayaCore Project Status

## Current Work
None - Phase 4 (Polish) completed.

## Critical Gotchas

- **Session loading tool call IDs**: When displaying tool calls from a loaded session, the `TagFunctionNotify` messages must include the `[:tool_call_id:]` prefix so the terminal adaptor can create windows with correct IDs. See `displayAssistantMessage` and `displayToolMessage` in `session_persist.go`.

- **Terminal scroll position**: `userMovedCursorAway` must be set for J/K scrolling, not just j/k, or scroll position is lost on focus switch.

- **Session loading scroll position**: When restoring a session with existing messages, `userMovedCursorAway` must be set to `true` so the viewport starts at the top (YOffset=0) instead of scrolling to the bottom. This is handled in `NewTerminalWithTheme` when `WindowBuffer().GetWindowCount() > 0`.

## Architecture Notes

For implementation-specific gotchas, see code comments in:
- `internal/llm/providers/anthropic.go` - Prompt caching details
- `internal/llm/providers/openai.go` - Tool call chunking details
- `internal/llm/agent.go` - Tool result ordering, incomplete tool calls
- `internal/agent/session.go` - SwitchModel deadlock pattern
- `internal/adaptors/terminal/window.go` - markDirty sentinel preservation
- `internal/adaptors/terminal/output.go` - ANSI styling non-recursive