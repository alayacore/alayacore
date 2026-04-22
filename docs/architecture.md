# Architecture

AlayaCore follows a layered architecture with clear separation of concerns. The layers communicate via a lightweight TLV (Tag-Length-Value) binary protocol.

## Components

### Entry Point (`main.go`, `internal/config/`, `internal/app/`)

The entry point wires together all components:

1. **`config.Parse()`** — Parses CLI flags into `config.Settings`
2. **`app.Setup()`** — Initializes shared components:
   - Skills manager (loads skill metadata from `--skill` directories)
   - Tools (`read_file`, `edit_file`, `write_file`, `execute_command`, `activate_skill`, and conditionally `search_content`)
   - System prompt (default + skills section/fragment when configured + current working directory)
3. **Adaptor creation** — Starts either the terminal or PlainIO adaptor

### Adaptors Layer (`internal/adaptors/`)

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

#### Terminal Adaptor (`internal/adaptors/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main Bubble Tea model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling. See [virtual-rendering-performance.md](virtual-rendering-performance.md). |
| `InputModel` | Handles user text input with external editor support. See [external-editor-windowsize.md](external-editor-windowsize.md). |
| `ModelSelector` | Modal for switching between AI models |
| `QueueManager` | Modal for managing the task queue |
| `ThemeSelector` | Modal for switching between color themes |
| `OutputWriter` | Parses TLV from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

#### PlainIO Adaptor (`internal/adaptors/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, and tool call headers. Suppresses tool result content. Reads prompts from stdin (one per line, backslash continuation).

#### File Naming Convention

Files in the adaptor packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel.
- **`output.go`** — handles the **output from the session**. Receives TLV messages from the session and renders them to the user (TUI windows, stdout).

```
User IO ──▶ input.go ──▶ input channel ──▶ Session ──▶ output.go ──▶ User IO
             ("input to                       ("output from
              the session")                    the session")
```

Both adaptors follow this convention. Each adaptor provides its own implementation of how user IO maps to and from the session's TLV channels.

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

| Component | Description |
|-----------|-------------|
| `Session` | Main struct managing conversation state and message history |
| `Task Queue` | FIFO queue for pending prompts and commands |
| `ModelManager` | Loads and manages AI model configurations from `model.conf`. Never writes to the file. |
| `RuntimeManager` | Persists runtime settings (active model, active theme) to `runtime.conf` |
| `CommandRegistry` | Declarative registration of session commands (`:save`, `:cancel`, etc.) |
| `ContextTokens` | Tracks conversation context size across API calls. See [context-tracking.md](context-tracking.md). |
| `compactHistory()` | Truncates old tool results to save context. Enabled by default; disable with `--no-compact`. |

### Agent Layer (`internal/llm/`)

The agent layer handles LLM interaction and tool-calling orchestration.

| Component | Description |
|-----------|-------------|
| `Agent` | Tool-calling loop orchestration with configurable max steps |
| `Provider` interface | Streaming LLM abstraction with callback-based event handling |
| `Factory` | Creates the correct provider based on `protocol_type` |
| `Providers` | Anthropic and OpenAI implementations |
| `TypedExecute` | Type-safe tool execution via Go generics |
| `GenerateSchema` | Auto-generates JSON schemas from struct tags |

**Key pattern — Callback Streaming:**

```go
Agent.Stream(ctx, messages, llm.StreamCallbacks{
	OnTextDelta:      func(delta string) error { ... },
	OnReasoningDelta: func(delta string) error { ... },
	OnToolCall:       func(id, name string, input json.RawMessage) error { ... },
	OnToolResult:     func(id string, output ToolResultOutput) error { ... },
	OnStepStart:      func(step int) error { ... },
	OnStepFinish:     func(msgs []Message, usage Usage) error { ... },
})
```

Messages are appended incrementally in `OnStepFinish` so they're preserved even if the user cancels.

### Tools Layer (`internal/tools/`)

| Tool | Description | Safety | Dependency |
|------|-------------|--------|------------|
| `read_file` | Read file contents with optional line ranges. 32KB max for full reads. | Safe | — |
| `edit_file` | Search/replace edits on existing files | Medium | — |
| `write_file` | Create or overwrite files | Dangerous | — |
| `execute_command` | Execute commands in the detected shell (cross-platform). Output truncated at 32KB. | Most Dangerous | — |
| `activate_skill` | Load and execute Agent Skills | Medium | — |
| `search_content` | Search file contents using ripgrep (`rg`). 50 matching lines by default. | Safe | Requires `rg` binary |

Each tool is implemented with type-safe input structs and auto-generated JSON schemas. All tools accept a `context.Context` parameter and respect cancellation — `:cancel` will interrupt long-running tool execution. See [schema-improvements.md](schema-improvements.md) for the pattern.

The `search_content` tool is conditionally registered — it is only available when the `rg` binary is found on the system `PATH` at startup. When available, the system prompt includes a `SEARCH:` section instructing the LLM to prefer `search_content` over reading files chunk by chunk to locate code, definitions, usages, and patterns.

#### Shell Detection (`internal/tools/shell/`)

The `execute_command` tool uses a cross-platform shell detection system. On startup, it probes the OS environment for an available shell and selects the best candidate.

**Detection order:**

1. `ALAYACORE_SHELL` environment variable (matched against known shells; unknown values are ignored)
2. OS-specific `knownShells` list tried in preference order (guaranteed to succeed — `sh` on Unix, `cmd` on Windows)

**Supported shells:**

| Shell | Binary | OS | Invocation | Notes |
|-------|--------|----|------------|-------|
| Bash | `bash` | Unix | `bash -c <cmd>` | Preferred on Unix; LLMs naturally write bash syntax |
| Zsh | `zsh` | Unix | `zsh -c <cmd>` | Second choice on Unix |
| POSIX sh | `sh` | Unix | `sh -c <cmd>` | Guaranteed on all POSIX systems |
| PowerShell Core | `pwsh` | Windows | `pwsh -NoLogo -NonInteractive -Command <cmd>` | Preferred on Windows |
| Windows PowerShell | `powershell` | Windows | `powershell -NoLogo -NonInteractive -Command <cmd>` | Ships with Windows |
| cmd | `cmd` | Windows | `cmd /C <cmd>` | Guaranteed on all Windows machines |

The tool description (shown to the LLM) is dynamically generated based on the detected shell so the LLM uses the correct syntax. Platform-specific process isolation is handled per-OS:

- **Unix**: `setsid` creates a new session; `SIGINT` → `SIGKILL` for cancellation
- **Windows**: `CREATE_NO_WINDOW` isolates the child; `process.Kill()` for cancellation

The package uses Go build tags (`//go:build !windows` / `//go:build windows`) for all OS-specific code.

## TLV Protocol

Communication between adaptors and session uses a simple Tag-Length-Value (TLV) binary protocol.

### Message Format

```
[2-byte tag][4-byte length (big-endian)][value bytes]
```

### Tags

| Tag | Code | Direction | Description |
|-----|------|-----------|-------------|
| `TagTextUser` | TU | Input | User text input |
| `TagTextAssistant` | TA | Output | Assistant text output |
| `TagTextReasoning` | TR | Output | Reasoning/thinking content |
| `TagFunctionCall` | FC | Output | Function call for persistence |
| `TagFunctionResult` | FR | Output | Function result for persistence |
| `TagFunctionState` | FS | Output | Function state indicator (pending/success/error) |
| `TagSystemError` | SE | Output | System error messages |
| `TagSystemNotify` | SN | Output | System notifications |
| `TagSystemData` | SD | Output | System data (JSON) |

### Example Flow

```
1. User types "read main.go" in terminal
2. Terminal adaptor emits: TLV(TU, "read main.go")
3. Session reads TLV, creates UserPrompt task
4. Session processes prompt through the agent loop
5. Agent calls read_file tool → Session emits: TLV(FS, pending) → TLV(FS, success)
6. Agent generates response → Session emits: TLV(TA, "Here's what main.go does...")
7. Terminal adaptor parses TLV, renders styled content in windows
```

## Cross-Platform Architecture

AlayaCore uses Go build tags for all OS-specific code. The only platform-dependent subsystem is shell execution, isolated in the `internal/tools/shell/` package:

| File | Build tag | Provides |
|------|-----------|----------|
| `shell.go` | *(all)* | `Shell` type, `Detect()`, `detect()` |
| `shell_unix.go` | `!windows` | Unix shell defs (`bash`, `zsh`, `sh`), `knownShells` |
| `shell_windows.go` | `windows` | Windows shell defs (`pwsh`, `powershell`, `cmd`), `knownShells` |
| `exec_unix.go` | `!windows` | `SetDetachFlags` (setsid), `OpenDevNull` (/dev/null), `AssignJob` (no-op), `ClearJob` (no-op) |
| `exec_windows.go` | `windows` | `SetDetachFlags` (CREATE_NO_WINDOW + CREATE_NEW_PROCESS_GROUP), `OpenDevNull` (NUL) |
| `terminate_unix.go` | `!windows` | `TerminateProcessGroup` (SIGINT → SIGKILL) |
| `terminate_windows.go` | `windows` | `Job` type, `AssignJob`, `ClearJob`, `TerminateProcessGroup` (Job Object → taskkill /F /T → Kill) |

All other packages (LLM providers, session management, TLV protocol, skills, schema generation) are pure Go with no OS-specific code.

## System Prompt Architecture

The system prompt is sent as **separate messages** (not a single concatenated string):

```
System Message 1: Default Prompt (identity + rules + search preferences)
                  + Skills section + Skills Fragment (only when skills configured)
                  + Current working directory

System Message 2: Extra System Prompt (from --system flag, repeatable)
```

When `rg` is available, the default prompt includes a `SEARCH:` section that instructs the LLM to prefer the `search_content` tool for locating content over reading files chunk by chunk. This section is omitted when `rg` is not installed.

When skill paths are provided via `--skill` and skills are discovered, the prompt includes a `SKILLS:` section (with instructions for using `activate_skill`) followed by an `<available_skills>` XML fragment. Both are omitted entirely when no skills are configured.

Both providers (`openai`, `anthropic`) send these as two independent system
messages. The default prompt and extra prompt are kept separate so the LLM API
can cache them independently.

For Anthropic APIs with `prompt_cache: true`, a top-level `cache_control: {"type": "ephemeral"}` is added to the request body for automatic prompt caching.

## Data Flow

### Startup Flow

```
main.go → config.Parse() → Settings
                ↓
        app.Setup(Settings)
                ↓
        ├── skills.NewManager(skillPaths)
        ├── tools.NewReadFileTool(), etc.
        ├── tools.RGAvailable() → conditionally register search_content tool
        └── Build system prompt (with SEARCH section if rg available)
                ↓
        terminal.NewAdaptor(appConfig)  or  plainio.NewAdaptor(appConfig)
                ↓
        Session created with tools and system prompt
```

### User Prompt Flow

```
User types prompt
  → InputModel captures input
    → Emit TLV(TU, prompt)
      → Session.readFromInput()
        → submitTask(UserPrompt)
          → Task Queue
            → taskRunner()
              → handleUserPrompt()
                → processPrompt()
                  → Agent.Stream()
                    → Callbacks emit TLV(TA), TLV(TR), TLV(FS), etc.
                      → OutputWriter parses TLV
                        → WindowBuffer.AppendOrUpdate()
                          → DisplayModel.View()
                            → Terminal renders output
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
  → OnToolCall callback → TLV(FS, pending) → UI shows tool indicator
    → Agent executes tool: tool.Execute(ctx, input)
      → OnToolResult callback → TLV(FS, result_status) → UI updates indicator
        → Tool result added to messages
          → Agent continues to next step (if under max_steps)
```

## Design Decisions

1. **TLV Protocol** — Simple binary protocol for clean separation between adaptors and session. Both the TUI and plain-IO mode share all session/agent logic.
2. **Task Queue** — Deferred task processing with cancellation support. Queued tasks execute sequentially.
3. **Virtual Scrolling** — Only visible windows are rendered. 3.5x faster than naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md).
4. **Domain Errors** — Structured error types with operation context for consistent error handling. See [error-handling.md](error-handling.md).
5. **Command Registry** — Declarative command registration for extensibility.
6. **Interface Abstraction** — OutputWriter interface for testability.
7. **Provider Factory** — Decoupled provider creation from session logic.
8. **Typed Tools** — `TypedExecute[T]` wrapper for type-safe tool implementations with auto-generated schemas.
9. **Lazy Agent Init** — Agent and provider are created on first use, not at startup.
10. **Sequential Tool Execution** — Tools execute one at a time. See [sequential-tool-execution.md](sequential-tool-execution.md).
11. **Context Efficiency** — Tool descriptions are minimal, outputs are size-capped (32KB), search results limited (50 lines), and old tool results are compacted to save tokens. See [context-tracking.md](context-tracking.md).

## Gotchas

Non-obvious patterns that have caused bugs. Read carefully when modifying related code.

### Agent step messages must not be reconstructed

The provider's `StepCompleteEvent.Messages` contains the complete assistant message — text, reasoning, and tool calls all together. The agent must use these messages as-is, not reconstruct a new message from just the tool calls. Reconstruction loses text content that the LLM returned alongside tool calls. See `agent.go` → `processStreamEvents`.

### Sentinel values must never be overwritten

`WindowBuffer.dirtyIndex` uses a sentinel (`dirtyFullRebuild = -2`) to signal that all windows need recalculation. State transitions must check whether the sentinel is already set before overwriting — an `else` branch that blindly assigns a new index can downgrade a full-rebuild to a single-window update, silently dropping windows from the display. See `window.go` → `markDirty`.

### Mutex deadlock in SwitchModel

Don't hold a mutex while calling methods that may need the same mutex.

```
❌ lock → update fields → call method (needs lock) → deadlock
✅ lock → update fields → unlock → call method
```

### OpenAI tool call chunking

Tool arguments arrive in chunks across multiple delta events:
- First chunk: has `id` and `name`
- Subsequent chunks: `id: ""` but correct `index`
- **Must use `index` (not `id`) to associate chunks** — see `openAIStreamState.appendToolCallArgs()`
- When sending back in history, arguments must be JSON-string (not raw JSON) — see `convertToolCalls()`

### Anthropic prompt caching

- System message must be ≥1024 tokens for caching to activate
- Uses **automatic caching**: top-level `cache_control: {"type": "ephemeral"}` on the request body
- Enabled per-model via `prompt_cache: true` in `model.conf` (other providers ignore)
- Best for multi-turn conversations where growing message history should be cached automatically

### Terminal scroll position

`userMovedCursorAway` must be set for J/K (line scroll) and Ctrl+D/Ctrl+U (half-page scroll), not just j/k (cursor move), or auto-follow is not properly disabled on manual scrolling.

### Incomplete tool calls on cancel

When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.

### Tool result message ordering

`OnStepFinish` callback receives complete step messages. For tool-using steps, this includes both the assistant message (with tool calls) AND the tool result message. The `OnToolResult` callback should only send UI notifications, not append to session messages — the agent loop handles message assembly.

### ANSI escape sequences are not recursive

When styling text with lipgloss, each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work.


