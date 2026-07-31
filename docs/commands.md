# Commands

AlayaCore provides colon-prefixed commands (`:command`) that work across all adapters — TUI, Plain IO, and Raw IO.

## Protocol

Commands travel over a dedicated control plane, mirroring the tool control
plane (AF/UF):

```
CI {"id":"<call-id>","name":"<command>","input":"<args-string>"}   adapter → agent
CO {"id":"<call-id>","output":<result>}                            agent → adapter
CO {"id":"<call-id>","is_error":true,"output":{"code":"...","message":"..."}}  ← failure
```

- The adapter generates the call ID and echoes it back — every CI receives
  exactly one CO.
- `output` is structured JSON defined per command (see table below); on
  failure it is a uniform error object `{code, message}`.
- Adapters translate the human-facing `:command args` text into CI frames;
  the agent never sees colon-text. The TUI and Plain IO adapters do this
  translation automatically.
- Async commands (`:continue`, `:summarize`) reply `{"status":"started"}`
  immediately; completion is reported via the `task` SM message carrying
  `command_id`.
- Command results never travel as SM `error`/`notify` — those are reserved
  for non-command events (task errors, MCP status, etc.).

Commands fall into three categories:

- **Immediate commands** (`CmdImmediate`) — run synchronously in the main loop, always allowed
- **Idle commands** (`CmdIdle`) — run synchronously, but rejected while a task is in progress
- **Task commands** — require LLM calls, run in a separate goroutine

## Immediate Commands

| Command | Action | CO result |
|---------|--------|-----------|
| `:cancel` | Cancel current task | `null` |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. | `{"path"}` |
| `:reason [0\|1\|2]` | Set reasoning level (0=off, 1=normal, 2=max). Default: 1 | `{"level"}` |
| `:theme_set <name>` | Switch to a different theme | `{"name"}` |
| `:fork <id> <filename>` | Fork session — save all content up to a history ID to a file | `{"path","count"}` |
| `:tool_decline <id>` | Decline a pending tool execution | `{"tool_id"}` |
| `:mcp_cancel` | Cancel MCP server initialization | `null` |

## Idle Commands

These commands are rejected with an error if a task is currently running:

| Command | Action | CO result |
|---------|--------|-----------|
| `:tool_confirm <id>` | Confirm a pending tool execution | `{"tool_id"}` |
| `:mcp_confirm <server> <code> <redirect_uri>` | Confirm MCP OAuth authorization with auth code | `{"server"}` |
| `:mcp_decline <server>` | Decline MCP OAuth authorization | `{"server"}` |
| `:model_set <id>` | Switch to a model by numeric ID | `{"active_id","active_name"}` |
| `:model_load` | Reload model configs from the config file | `{"models"}` |
| `:model_sync` | Apply edited model config (sent by UI, not user-facing) | `{"models"}` |
| `:video_config <fps> <0\|1>` | Set video FPS and resolution (0=default, 1=max) | `{"fps","res"}` |

## Task Commands

These commands require LLM calls and run in a separate goroutine:

| Command | Action | CO result |
|---------|--------|-----------|
| `:continue` | Retry the last prompt | `{"status":"started"}` (async; completion via `task` SM with `command_id`) |
| `:summarize` | Summarize conversation to reduce token usage ⚠️ **Replaces entire conversation history with a summary** — see [context-tracking.md](context-tracking.md) | `{"status":"started"}` (async; completion via `task` SM with `command_id`) |

## Adapter-Specific Commands

Some commands are handled directly by each adapter and never reach the session command dispatch:

| Command | TUI | Plain IO | Raw IO |
|---------|-----|----------|--------|
| `:quit` / `:q` | Shows confirmation dialog | Exits immediately | Not interpreted — raw CI/CO pass-through |
| `:help` | Opens help window | Exits immediately (no TUI) | Not interpreted — raw CI/CO pass-through |
| `:suspend` | Suspends process (Ctrl+Z) | Not supported | Not interpreted — raw CI/CO pass-through |

## :fork Details

The `:fork` command saves all session content from the beginning up to (and including) a specific history ID to a new file. This is useful for extracting a conversation segment into a standalone session file.

```
:fork 42 ./extract.alaya
```

In the TUI, you can also press `Ctrl+F` at a window to pre-fill the `:fork` command with that window's history ID.

## :continue

See [error-handling.md](error-handling.md) for details on error recovery with `:continue`.

## :summarize

The `:summarize` command asks the LLM to produce a concise summary of the conversation, then replaces the entire message history with that summary. This is the only way to reduce context usage manually when auto-summarize is disabled.

> ⚠️ **Destructive** — The conversation history is replaced by the summary. Previous turns are lost. Consider saving first with `:save`.

See [context-tracking.md](context-tracking.md) for details.
