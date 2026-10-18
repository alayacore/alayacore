# Adapter Architecture

The adapter layer handles user interaction and translates between user actions and the frame protocol.

## Adapters

### Terminal Adapter (`internal/adapters/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main TUI model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling |
| `PromptInput` | Handles user text input with attachment display |
| `InputField` | Reusable text field: single-line by default (filter, search, path), multi-line only for the prompt |
| `FilteredListCore` | Shared filtering, navigation, and overlay logic |
| `ModelSelector` | Modal for switching between AI models |
| `ThemeSelector` | Modal for switching between color themes |
| `HelpOverlay` | Overlay listing keybindings and commands with search |
| `AttachmentOverlay` | File/URL attachment picker with directory browser |
| `ConfirmDialog` | Confirmation dialog for tool execution and other actions |
| `OutputWriter` | Parses frame from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

### PlainIO Adapter (`internal/adapters/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, tool call JSON, and tool result JSON. Displays tool_confirm system messages as plain text prompts. Reads prompts from stdin (one per line, backslash continuation for multi-line prompts) and writes each one as it is read — a prompt that arrives before MCP init has settled is held by the session and run when it is ready, so nothing here waits for a frame (`STATE_MCP_NOT_READY` is not a race a client has to avoid). When stdin ends, the adapter says so with a `CE` frame instead of closing the pipe, and closes it only after the session has exited: the pipe is what the session reads, and an OAuth callback running beside this goroutine submits its `:mcp_confirm` through it. Supports the full MCP flow: renders init progress (`[mcp: ...]` lines) and runs OAuth authorization automatically (callback server + browser, one flow per server) via the shared `internal/mcpauth` flow; a callback that does not arrive falls back to the manual `:mcp_confirm`/`:mcp_decline` commands on a terminal, or declines the server when stdin is a pipe (nothing to type into). Hooks fired by MCP system messages are deferred (registered under the output lock, executed after unlock) so they may safely print.

### TerseIO Adapter (`internal/adapters/terseio/`)

Minimal stdin/stdout mode, activated with `--terseio`. Reads **all of stdin as a single prompt** — or, if it starts with `:`, as a **single command** (the whole input is sent as one CI frame; `:quit`/`:q` exit cleanly) — and prints **only the final assistant text** to stdout; errors, notifications, and informative command results go to stderr. Buffers the current AT/At message (dropping earlier text on new history IDs) and flushes it on the task-completion system message (`in_progress: false`) — but only if the final message actually contains text (reasoning-only or tool-call-only final messages produce empty stdout). CO frames are parsed: command errors go to stderr and set exit code 1; successful results render human-readable feedback for informative commands (`:save`, `:fork`) and stay silent for self-evident or async ones (`:cancel`, `:continue`, ...) whose feedback is the final answer. SM errors — task failures and session-level failures (auto-save, pre-summarize backup, auto-summarization) — likewise go to stderr, set exit code 1, and discard the buffered final answer. `--tool-confirm` is rejected at startup (conflict), so no tool_confirm frames can arrive and no confirmation machinery is needed. The single prompt is sent as soon as stdin is read: a prompt that arrives before MCP init has settled is held by the session and run when it is ready, so nothing here waits for a frame and `STATE_MCP_NOT_READY` is not a race this adapter has to avoid. When stdin ends, the adapter sends a `CE` frame rather than closing the pipe, and closes it only after the session has exited, since an OAuth callback started by this adapter may still need to submit its `:mcp_confirm`. MCP OAuth is automatic via the shared `internal/mcpauth` flow (progress and the authorization URL go to stderr); a server whose automatic authorization does not complete is declined, since there is nowhere to type a code.

### RawIO Adapter (`internal/adapters/rawio/`)

Raw frame stdin/stdout mode, activated with `--rawio`. Reads and writes raw frames directly, with no text parsing or formatting. Designed for parent programs that speak the frame protocol to control AlayaCore programmatically.

### File Naming Convention

Files in the adapter packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel via frames.
- **`output.go`** — handles the **output from the session**. Receives frames from the session and renders them to the user (TUI windows, stdout).

The rawio adapter is a thin exception — `adapter.go` passes `os.Stdin`
directly to the session, and `writer.go` wraps `os.Stdout` in a
`lockedWriter` that serializes the session's concurrent writes (run +
task goroutines) so frames larger than PIPE_BUF cannot interleave
mid-frame and corrupt the protocol.

```
User IO ──▶ input.go ──▶ frames (stdin) ──▶ Session ──▶ frames (stdout) ──▶ output.go ──▶ User IO
```

## Communication Pattern

All adapters communicate with the session **exclusively** through the frame stream protocol:

```
Adapter ──▶ frames (stdin) ──▶ Session
Session ──▶ frames (stdout) ──▶ Adapter
```

This is the **only** runtime channel. The session must not call adapter functions,
and the adapter must not call session functions — not even for built-in adapters
that share the Go module. See [development-principles.md](../development-principles.md)
for the full isolation rules.

### Theme Persistence

The session persists the active theme to `runtime.conf` and communicates it to the terminal adapter as frames as a `TagSystemMsg` with type `"theme"`. The session skips theme broadcasting entirely for plainio, terseio, and rawio modes (`NoTheme` — they have no visual rendering): no `theme`/`theme_list` messages are sent, theme errors are never emitted, and `:theme_set` is rejected as unavailable. On startup, the terminal reads the initial theme from the first `"theme"` message (defaulting to `"theme-dark"`).

Theme changes flow through the session to keep a single source of truth:

1. `:theme_set <name>` (typed by user) or theme selector confirm both send the command to the session (as a CI frame; the result arrives as a CO frame)
2. Session persists the theme name to `runtime.conf` and broadcasts the updated theme via a `TagSystemMsg` frame (`{"type":"theme","data":{"name":"...","theme":{...}}}`)
3. The terminal detects the theme change in `updateStatus()` and calls `applyTheme()` with the full theme data from the frame, updating all UI component styles

This ensures both paths converge: the theme selector's live preview applies
themes via a debounced tick for smooth navigation, but the final commit
always goes through the session.

## Wire Format

One frame per line: a two-byte tag, a JSON value, and an LF. The tag names the
frame's plane and the value carries the kind; `UE` and `CE` are control frames
whose line is the tag alone. The reader never skips a malformed frame — it ends
the stream, because a frame that is not a frame means the writer lost its place.

See [`adapter-guide/README.md`](../../adapter-guide/README.md) for the full frame
protocol spec — the format, the tags, delta messages, function lifecycle, and a
sample file for every frame type — and `internal/lines` for the boundary itself,
which the MCP and SSE readers share.

## Data Flow

### Startup Flow

```
main.go → config.Parse() → Settings
                ↓
        app.Setup(Settings)
                ↓
        ├── skills.NewManager(skillPaths)   → metadata + load errors + notices
        ├── tools.DefaultTools(cfg.BuiltinTools)  → filtered built-in tools
        │   (all tools: read_file, write_file, edit_file, execute_command, search_content)
        │   (controlled by --builtin-tools flag; empty = no builtin tools)
        └── Build system prompt
                ↓
        terminal.NewAdapter(appConfig)  or  plainio.NewAdapter(appConfig)  or  terseio.NewAdapter(appConfig)  or  rawio.NewAdapter(appConfig)
                ↓
        Session created with tools and system prompt
          (app.StartSession writes the collected startup errors as SM `error`
           frames and the skill discovery outcome as SM `notify` frames, so a
           container that loaded nothing is visible in every adapter)
```

### User Prompt Flow

```
User types prompt
  → PromptInput captures input
    → Emit frame(UX, one JSON content item), frame(UE)
      → inputPump reads frame, stages content, flushes on UE
        → runTask() (task goroutine)
          → processPrompt()
            → Agent.Stream()
              → Delta callbacks emit frame(Ax), frame(Af) (streaming deltas)
              → Complete callbacks emit frame(AX), frame(AF) (authoritative)
              → Tool output callbacks emit frame(UF)
              → Tool preview callbacks emit frame(Uf) (ephemeral snapshots; ignored by non-TUI adapters)
                → OutputWriter parses frames
                  → WindowBuffer.AppendOrUpdate()
                    → DisplayModel.View()
                      → Terminal renders output
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
  → OnToolInputStart callback → frame(AF, call_id + name) → UI shows tool name immediately
    → OnToolInputDelta callback → frame(Af, call_id + delta) → UI shows truncated one-line preview
    → OnToolInputComplete callback → frame(AF, call_id + input) → UI fills in full arguments
      → Agent executes tool: tool.ExecuteStreaming(ctx, input, onDelta) when available
        → OnToolOutputDelta callback → frame(Uf, call_id + text) → UI shows live result preview snapshot
        → OnToolOutput callback → frame(UF, call_id + output) → UI replaces preview with authoritative output
          → Tool result added to messages
            → Agent continues to next step (if under max_steps)
```

## Adapter Bootstrap

The `StartSession()` function in `app/session.go` handles shared initialization for all adapters:

- Creates an `io.Pipe()` internally, returning the `PipeWriter` to the adapter so it can feed frames to the session
- `session.InitError()` — fatal initialization check (--model flag validation)
- `session.GetLoadErrors()` — emit model config errors as system messages
- `session.GetRuntimeLoadErrors()` — emit runtime.conf errors as system messages: parse errors, and a file that is there but cannot be read
- `session.HasModels()` — abort if no models configured

This is setup code, not runtime coupling. Once the program starts, the adapter only interacts with the session as frames.
