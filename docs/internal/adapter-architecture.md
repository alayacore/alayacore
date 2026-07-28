# Adapter Architecture

The adapter layer handles user interaction and translates between user actions and the TLV protocol.

## Adapters

### Terminal Adapter (`internal/adapters/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main Bubble Tea model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling |
| `PromptInput` | Handles user text input with attachment display |
| `InputField` | Reusable text input component (filter, search, command entry) |
| `FilteredListCore` | Shared filtering, navigation, and overlay logic |
| `ModelSelector` | Modal for switching between AI models |
| `ThemeSelector` | Modal for switching between color themes |
| `HelpWindow` | Overlay listing keybindings and commands with search |
| `AttachmentWindow` | File/URL attachment picker with directory browser |
| `ConfirmDialog` | Confirmation dialog for tool execution and other actions |
| `OutputWriter` | Parses TLV from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

### PlainIO Adapter (`internal/adapters/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, tool call JSON, and tool result JSON. Displays tool_confirm system messages as plain text prompts. Reads prompts from stdin (one per line, backslash continuation for multi-line prompts).

### RawIO Adapter (`internal/adapters/rawio/`)

Raw TLV stdin/stdout mode, activated with `--rawio`. Reads and writes raw TLV-encoded frames directly, with no text parsing or formatting. Designed for parent programs that speak the TLV protocol to control AlayaCore programmatically.

### File Naming Convention

Files in the adapter packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel via TLV-encoded messages.
- **`output.go`** — handles the **output from the session**. Receives TLV messages from the session and renders them to the user (TUI windows, stdout).

The rawio adapter is an exception — it's a single `adapter.go` since both directions are trivial: `os.Stdin` is passed directly to the session, and output writes to `os.Stdout`.

```
User IO ──▶ input.go ──▶ TLV (stdin) ──▶ Session ──▶ TLV (stdout) ──▶ output.go ──▶ User IO
```

## Communication Pattern

All adapters communicate with the session **exclusively** through the TLV stream protocol:

```
Adapter ──▶ TLV frames (stdin) ──▶ Session
Session ──▶ TLV frames (stdout) ──▶ Adapter
```

This is the **only** runtime channel. The session must not call adapter functions,
and the adapter must not call session functions — not even for built-in adapters
that share the Go module. See [development-principles.md](../development-principles.md)
for the full isolation rules.

### Theme Persistence

The session persists the active theme via `RuntimeManager` and communicates it to the terminal adapter through TLV as a `TagSystemMsg` with type `"theme"`. The plainio and rawio adapters ignore it since they have no visual rendering. On startup, the terminal reads the initial theme from the first `"theme"` message (defaulting to `"theme-dark"`).

Theme changes flow through the session to keep a single source of truth:

1. `:theme_set <name>` (typed by user) or theme selector confirm both send the command to the session
2. Session persists the theme name via `RuntimeManager.SetActiveTheme()` and broadcasts the updated theme via a `TagSystemMsg` TLV message (`{"type":"theme","data":{"name":"...","theme":{...}}}`)
3. The terminal detects the theme change in `updateStatus()` and calls `applyTheme()` with the full theme data from the TLV message, updating all UI component styles

This ensures both paths converge: the theme selector's live preview applies
themes via a debounced tick for smooth navigation, but the final commit
always goes through the session.

## TLV Protocol

See [`adapter-guide/README.md`](../adapter-guide/README.md) for the full TLV
protocol spec — wire format, tags, delta messages, function lifecycle,
and binary samples for every message type.

## Data Flow

### Startup Flow

```
main.go → config.Parse() → Settings
                ↓
        app.Setup(Settings)
                ↓
        ├── skills.NewManager(skillPaths)
        ├── tools.DefaultTools(cfg.BuiltinTools)  → filtered built-in tools
        │   (all tools: read_file, write_file, edit_file, execute_command, search_content)
        │   (controlled by --builtin-tools flag; empty = no builtin tools)
        └── Build system prompt
                ↓
        terminal.NewAdapter(appConfig)  or  plainio.NewAdapter(appConfig)  or  rawio.NewAdapter(appConfig)
                ↓
        Session created with tools and system prompt
```

### User Prompt Flow

```
User types prompt
  → PromptInput captures input
    → Emit TLV(UT, prompt), TLV(UE)
      → inputPump reads TLV, stages content, flushes on UE
        → runTask() (task goroutine)
          → processPrompt()
            → Agent.Stream()
              → Delta callbacks emit TLV(At), TLV(Ar), TLV(Af) (streaming deltas)
              → Complete callbacks emit TLV(AT), TLV(AR), TLV(AF) (authoritative)
              → Tool output callbacks emit TLV(UF)
                → OutputWriter parses TLV
                  → WindowBuffer.AppendOrUpdate()
                    → DisplayModel.View()
                      → Terminal renders output
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
  → OnToolInputStart callback → TLV(AF, {"id":"<id>","name":"<tool>"}) → UI shows tool name immediately
    → OnToolInputDelta callback → TLV(Af, {"id":"<id>","delta":"..."}) → UI shows truncated one-line preview
    → OnToolInputComplete callback → TLV(AF, {"id":"<id>","input":{...}}) → UI fills in full arguments
      → Agent executes tool: tool.Execute(ctx, input)
        → OnToolOutput callback → TLV(UF, {"id":"<id>","output":[{"type":"text","text":"..."}]}) → UI shows output and indicator
          → Tool result added to messages
            → Agent continues to next step (if under max_steps)
```

## Adapter Bootstrap

The `StartSession()` function in `app/session.go` handles shared initialization for all adapters:

- Creates an `io.Pipe()` internally, returning the `PipeWriter` to the adapter so it can feed TLV messages to the session
- `session.InitError()` — fatal initialization check (--model flag validation)
- `session.ModelManager.GetLoadErrors()` — print config errors
- `session.HasModels()` — abort if no models configured

This is setup code, not runtime coupling. Once the program starts, the adapter only interacts with the session via TLV.
