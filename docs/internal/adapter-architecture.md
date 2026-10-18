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

Plain stdin/stdout mode, activated with `--plainio`: prompts arrive as lines on
stdin, the session transcript leaves on stdout, and no terminal feature is used —
stdin's TTY-ness is consulted for exactly one decision, the MCP OAuth fallback.
The pipe stays open after `CE` so an OAuth callback can still submit a command
through it, and the adapter closes it only once the session has exited.

Behavior — input rules, output layout, exit codes, MCP — is
[plainio.md](../plainio.md)'s subject, and is stated there once.

### TerseIO Adapter (`internal/adapters/terseio/`)

Answer-only mode, activated with `--terseio`: all of stdin becomes one prompt or
one command, and stdout carries nothing but the final answer, with diagnostics and
command results on stderr where the exit code can speak too. The `CE`-then-close
ordering is plainio's, for the same reason.

Behavior, including the exit-code contract, is [terseio.md](../terseio.md)'s
subject.

### RawIO Adapter (`internal/adapters/rawio/`)

Raw frame stdin/stdout mode, activated with `--rawio`. Reads and writes raw frames directly, with no text parsing or formatting. Designed for parent programs that speak the frame protocol to control AlayaCore programmatically.

### Siblings, not variants

The four adapters are parallel modes, not four implementations of one thing, and
they are deliberately not factored into a shared "adapter core". Each is a
complete answer to *how this mode behaves* — what it reads, where output goes,
what an error does to the exit code, whether a confirmation can be answered at
all — and those answers are the mode.

plainio and terseio look alike today, since both render frames as plain text over
stdin/stdout, but they differ at every point a mode is defined by:

| | plainio | terseio |
|---|---|---|
| input | one prompt per line, read continuously | all of stdin, as one prompt or one command |
| stdout | the transcript | the final answer only |
| errors | stdout, exit code unaffected | stderr, exit code 1 |
| task errors | reported, the session continues | discard the buffered answer and exit 1 |
| tool confirmations | answerable (`:tool_confirm`) | rejected at startup (`--tool-confirm` conflict) |
| MCP OAuth fallback | manual commands on a terminal, decline on a pipe | declined — there is nowhere to type a code |

A shared implementation would make one mode's next decision also a decision about
the other's, and a fix for one would silently change what the other claims to do.
That is the same trade this tree refuses for the MCP protocol revisions
(`internal/mcp/protocol_adapter.go`, on the `Adapter` interface). Similarity
between two modes is a fact about today's code, not a rule about the design.

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

The session persists the active theme to `runtime.conf` and tells the terminal
*which* theme is active as a `TagSystemMsg` of type `"theme"` — the name only. The
palettes travel in `theme_list`, which the terminal caches on startup, so one frame
names the entry and the cache supplies its colors. On startup the terminal draws
the built-in default until the session's first `"theme"` frame arrives, and from
then on that name, resolved against the cache, is the active theme.

Theme broadcasting is skipped entirely for plainio, terseio and rawio (`NoTheme` —
they have no visual rendering): no `theme`/`theme_list` messages are sent, theme
errors are never emitted, and `:theme_set` is refused as unavailable.

Theme changes flow through the session to keep a single source of truth:

1. `:theme_set <name>` (typed by the user) or a theme selector confirmation both
   send the command to the session — a CI frame, whose result arrives as a CO
   frame.
2. The session persists the name to `runtime.conf` and broadcasts
   `{"type":"theme","data":{"name":"..."}}`.
3. The terminal's `syncThemeFromSession` resolves that name against the cached
   `theme_list` and applies the palette, recording the name as the applied one so
   the same broadcast cannot re-apply it.

Both paths converge: the selector's live preview applies a palette through a
debounced tick for smooth navigation, but the commit that sticks always goes
through the session.

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
        → runTaskNormal() (task goroutine)
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
