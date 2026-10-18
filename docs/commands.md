# Commands

AlayaCore provides colon-prefixed commands (`:command`) that work across all adapters — TUI, Plain IO, Terse IO, and Raw IO.

## Protocol

Commands travel over a dedicated control plane, mirroring the tool control
plane (AF/UF):

```
CI{"command_id":"<command-id>","name":"<command>","args":"<args-string>"}   adapter → agent
CO{"command_id":"<command-id>","result":<result>}                         agent → adapter
CO{"command_id":"<command-id>","is_error":true,"result":{"code":"...","message":"..."}}  ← failure
```

- The adapter generates the `command_id` and the agent echoes it back — every
  CI the agent can read receives exactly one CO. It is the command plane's own
  id: a tool call's `call_id` (the provider's `tool_call_id`) is a different
  value in a different plane, and the two are never compared.
- The id is required, and its absence is a protocol violation, not a request:
  a CI without one, or with JSON the agent cannot read, is answered with an SM
  `error` instead — there is no id to echo, so a CO would be a frame no adapter
  could correlate. The same goes for frame-level failures that are not commands
  at all (a malformed frame, a bad content frame, an unknown tag).
- `result` is structured JSON defined per command (see table below); on
  failure it is a uniform error object `{code, message}`. Rendering is the
  adapter's job: CO carries no display text — adapters correlate the CO `command_id`
  with the CI they sent (which carries the `name`) to render results.
- Adapters translate the human-facing `:command args` text into CI frames;
  the agent never sees colon-text. The TUI, Plain IO, and Terse IO adapters
  do this translation automatically (terseio: the whole stdin starting
  with `:` is one command; see [terseio.md](terseio.md)).
- Async commands (`:continue`, `:summarize`) reply `{"status":"started"}`
  immediately; completion is reported via the `task` SM message carrying
  `command_id`.
- Command results never travel as SM `error`/`notify` — those are reserved
  for non-command events (task errors, MCP status, etc.).

## Arguments

`args` is text: the agent decides how each command reads it, and every command
is one of two shapes.

- **Tokens** — whitespace-separated, with an exact count. There is no quoting,
  so a value containing a space cannot be one token: `:mcp_confirm <server> …`
  answers `REQUEST_INVALID_ARGS` for a server name with a space in it, rather than
  looking up the first half. Too many tokens is `REQUEST_INVALID_ARGS` too — never
  silently ignored.
- **A free-text tail** — the rest of the line, spaces included, with
  surrounding whitespace trimmed: `:save [filename]`, `:fork <history_id>
  <filename>`, `:theme_set <name>`, `:model_sync <content>`.

A command whose usage line is empty (`:quit`, `:cancel`, `:summarize`,
`:continue`, `:model_load`, `:mcp_skip`) takes no arguments at all; a stray word
is `REQUEST_INVALID_ARGS`.

## Error codes

A failed CO carries `{"code": ..., "message": ...}`. `message` is for display
and may change; `code` is the contract, and these are all of them — a client can
branch on it without reading the source. `protocol.CmdErrorCodes` is the same
list on the Go side, and `make check` fails if the two disagree.

One rule builds every code: **the first segment says what a caller can do about
it**, the rest names the cause. Match the prefix for the coarse answer, or the
whole string for the detail.

| Prefix | What a caller can do |
|--------|----------------------|
| `REQUEST_` | the request cannot be satisfied as sent — change what is asked |
| `CONFIG_` | the installation or model setup is at fault — fix the configuration |
| `STATE_` | nothing is wrong; the session is not in a position to act — wait |
| `IO_` | a file operation failed — check the path and permissions |
| `INTERNAL_` | a bug: the state a command needs was never built — report it |

| Code | Meaning |
|------|---------|
| `REQUEST_INVALID_ARGS` | the arguments do not match the command's usage line |
| `REQUEST_UNKNOWN_COMMAND` | the name is not a command |
| `REQUEST_NOT_FOUND` | the thing named does not exist, or is not pending: a history id, a tool confirmation, a theme, an MCP authorization |
| `REQUEST_MODEL_UNKNOWN` | no model has that id |
| `REQUEST_INVALID_STATE` | the request cannot be satisfied by this history, e.g. forking across an unpaired tool call |
| `CONFIG_MISSING` | nothing is configured to act on: no model, no MCP server, no session file |
| `CONFIG_MODEL_INVALID` | the config parsed but models were rejected; `message` lists why |
| `CONFIG_MODEL_FAILED` | the model subsystem failed: switching, rebuilding, or creating the provider |
| `CONFIG_UNSUPPORTED` | the feature does not exist in this adapter mode |
| `STATE_BUSY` | a task is already running |
| `STATE_SHUTTING_DOWN` | the session is ending |
| `STATE_MCP_NOT_READY` | MCP initialization or authorization is still pending |
| `STATE_NOT_IN_PROGRESS` | nothing is in flight to act on |
| `STATE_NOTHING_TO_CANCEL` | no task and no held prompt |
| `IO_READ_FAILED` | a file could not be read: the model config, a theme file |
| `IO_WRITE_FAILED` | a file could not be written: the session, the runtime settings |
| `INTERNAL_NOT_INITIALIZED` | a subsystem that should exist does not |
| `INTERNAL_UNCLASSIFIED` | a handler returned a bare error: a bug, since every refusal above names its cause |

Commands fall into three categories:

- **Immediate commands** (`cmdImmediate`) — run synchronously in the main loop, always allowed
- **Idle commands** (`cmdIdle`) — run synchronously, but rejected while a task is in progress
- **Task commands** — require LLM calls, run in a separate goroutine

## Immediate Commands

| Command | Action | CO result |
|---------|--------|-----------|
| `:quit` | End the session. A task that is already running is allowed to finish first. | `null` |
| `:cancel` | Cancel current task | `null` |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. | `{"path"}` |
| `:theme_set <name>` | Switch to a different theme. TUI only — rejected with `CONFIG_UNSUPPORTED` in Plain IO, Terse IO, and Raw IO (`NoTheme`) | `{"name"}` |
| `:fork <history_id> <filename>` | Fork session — save all content up to a history ID to a file | `{"path","count","history_id"}` |
| `:tool_confirm <call_id>` | Confirm a pending tool execution | `{"call_id"}` |
| `:tool_decline <call_id>` | Decline a pending tool execution | `{"call_id"}` |
| `:mcp_skip` | Skip MCP server initialization | `null` |

Two commands carry a short form a user may type instead — `:q` for `:quit` and
`:r` for `:reasoning_level_set <0|1|2>`. An adapter resolves a short form where a
typed line becomes a frame, so the session (and a Raw IO client) sees the full
name; the wire has no aliases, and a client that sends `q` is told the command
is unknown. A command with a keybinding does not get one: `Ctrl+S`, `Ctrl+L`,
`Ctrl+P`, `Ctrl+G` and `Ctrl+F` already cover `:save`, `:model_set`,
`:theme_set`, `:cancel` and `:fork`.

`:cancel` cancels the running task, and it takes the task's *queued* steering with
it: words that have not yet been spliced into a step are dropped, with a
`steering` message (phase `dropped`, reason `canceled`) saying so. Leaving them queued would start a fresh task on its own the
moment the canceled one unwinds — a stop button that keeps going. Words the task
had already spliced were sent to the model, so they stay in the conversation —
a cancel stops the task, it cannot unsend what the model was given. A prompt that
arrives *after* the cancel is a fresh intent and is delivered normally (see
[architecture.md](architecture.md#steering--a-prompt-that-arrives-mid-task)).

## Idle Commands

These commands are rejected with an error if a task is currently running:

| Command | Action | CO result |
|---------|--------|-----------|
| `:mcp_confirm <server> <code> <redirect_uri> [iss]` | Confirm MCP OAuth authorization with auth code | `{"server"}` |
| `:mcp_decline <server>` | Decline MCP OAuth authorization | `{"server"}` |
| `:model_set <id>` | Switch to a model by numeric ID | `{"active_id","active_name"}` |
| `:model_load` | Reload model configs from the config file | `{"models"}` |
| `:model_sync <content>` | Apply edited model config (sent by UI, not user-facing) | `{"models"}` |
| `:reasoning_level_set <0\|1\|2>` | Set reasoning level (0=off, 1=normal, 2=max). Default: 1 — set the startup level with `--reasoning-level <0\|1\|2>` (CLI flag wins over the session file's saved `reasoning_level`) | `{"level"}` |
| `:video_config <fps> <res>` | Set the default video FPS and resolution (`res`: 0=default, 1=max) applied to videos ingested after it — each video keeps the settings it was ingested with | `{"fps","res"}` |

## Task Commands

These commands require LLM calls and run in a separate goroutine:

| Command | Action | CO result |
|---------|--------|-----------|
| `:continue` | Retry the last prompt | `{"status":"started"}` (async; completion via `task` SM with `command_id`) |
| `:summarize` | Summarize conversation to reduce token usage ⚠️ **Replaces entire conversation history with a summary** — see [context-tracking.md](context-tracking.md) | `{"status":"started"}` (async; completion via `task` SM with `command_id`) |

## Adapter-Specific Commands

These commands are intercepted by the adapter before they reach the session
command dispatch. An interception either adds interaction and then forwards
the command (the TUI confirmation dialogs), or handles it entirely locally:

| Command | TUI | Plain IO | Terse IO | Raw IO |
|---------|-----|----------|----------|--------|
| `:quit` / `:q` | Shows confirmation dialog, then sends `quit` and exits when the session ends | Sends `quit`, then exits (code 0) | Sends `quit`, then exits (code 0) | Not interpreted — raw CI/CO pass-through |
| `:cancel` | Shows confirmation dialog, then forwards `:cancel` to the session | Sent to session | Sent to session | Not interpreted — raw CI/CO pass-through |
| `:help` | Opens help overlay | Sent to session (unknown command) | Sent to session (unknown command) | Not interpreted — raw CI/CO pass-through |
| `:suspend` | Suspends process (Ctrl+Z) | Not supported | Not supported | Not interpreted — raw CI/CO pass-through |

`:quit` appears in both tables: the session owns what quitting means — a `quit`
CI frame ends the session, which is how a rawio client quits — while each
adapter intercepts the user-facing `:quit`/`:q` to keep control of the
confirmation and of the process exit code.

## :fork Details

The `:fork` command saves all session content from the beginning up to (and including) a specific history ID to a new file. This is useful for extracting a conversation segment into a standalone session file.

```
:fork 42 ./extract.alaya
```

The two arguments are the history ID and the filename, and **the filename is
everything after the ID** — a path may contain spaces, so `:fork 42 my
notes.alaya` writes `my notes.alaya`. Surrounding whitespace is trimmed from
both (a trailing space would name a file the user cannot see), and inner
whitespace is kept. There is no quoting: whatever follows the ID is the path,
verbatim. `:save [filename]` takes its path the same way, and so does
`:theme_set <name>` — a theme is named by its file (`my theme.conf` →
`my theme`), so its name may contain spaces.

In the TUI, you can also press `Ctrl+F` at a window to pre-fill the `:fork` command with that window's history ID.

**The cut never separates a tool call from its result.** A file ending in a `tool_use` with no matching `tool_result` loads without complaint and is rejected by the provider on the *next* prompt, so `forkEndIndex` extends the cut forward until every tool call inside the prefix has its result inside it too. Because a step's results follow all of its calls (`call1, call2, result1, result2`), forking a two-call step at the first call writes both calls and both results — the whole group moves, rather than the named entry being quietly dropped. Forking at anything that carries no tool call (a prompt, reasoning, text) cuts exactly there.

The reported `count` is the number of parts written, so a group extension is visible in the command result. When a call's result does not exist anywhere in the session, widening cannot help. A trailing one — what a cancel leaves behind — is dropped, reusing the same guard the task path applies. A dangling call in the **middle** of the history cannot be dropped without inventing a rule about whose content disappears, and writing it would ship a file that only fails later, so `:fork` refuses with `REQUEST_INVALID_STATE` naming the call and writes nothing; fork at a point before that call instead.

Note that a forked file stores no history IDs (see [architecture.md](architecture.md): IDs are re-issued sequentially on load), so the new file's IDs start from 1 regardless of where the fork landed.

## :continue

See [error-handling.md](error-handling.md) for details on error recovery with `:continue`.

## :summarize

The `:summarize` command asks the LLM to produce a concise summary of the conversation, then replaces the entire message history with that summary. This is the only way to reduce context usage manually when auto-summarize is disabled.

> ⚠️ **Destructive** — The conversation history is replaced by the summary. Previous tasks are lost. Consider saving first with `:save`.

See [context-tracking.md](context-tracking.md) for details.
