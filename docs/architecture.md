# Architecture

AlayaCore follows a layered architecture with clear separation of concerns. The layers communicate via a lightweight TLV (Tag-Length-Value) binary protocol.

## Layer Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                            Entry Point                                  │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  main.go → config.Parse() → app.Setup() → terminal.NewAdaptor()  │   │
│  └──────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         Adaptors Layer                                  │
│  ┌─────────────────────┐            ┌──────────────────────────┐        │
│  │   Terminal Adaptor  │            │     PlainIO Adaptor      │        │
│  │   (Bubble Tea TUI)  │            │   (plain stdin/stdout)   │        │
│  └──────────┬──────────┘            └──────────────┬───────────┘        │
└─────────────┼──────────────────────────────────────┼────────────────────┘
              │                                      │
              │         TLV Protocol                 │
              │  (Tag-Length-Value Messages)         │
              │                                      │
┌─────────────┼──────────────────────────────────────┼────────────────────┐
│             ▼                                      ▼                    │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                       Session Layer                               │  │
│  │  ┌───────────────────┐  ┌───────────────┐  ┌────────────────────┐ │  │
│  │  │ Task Queue (FIFO) │  │ Model Manager │  │ Runtime Manager    │ │  │
│  │  └───────────────────┘  └───────────────┘  └────────────────────┘ │  │
│  └───────────────────────────┬───────────────────────────────────────┘  │
│                              │                                          │
└──────────────────────────────┼──────────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         Agent Layer                                     │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                        LLM Package                                │  │
│  │   ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐   │  │
│  │   │   Agent     │  │  Provider   │  │       Factory           │   │  │
│  │   │ (Tool Loop) │  │  Interface  │  │  (Provider Creation)    │   │  │
│  │   └─────────────┘  └─────────────┘  └─────────────────────────┘   │  │
│  └──────────────────────────┬────────────────────────────────────────┘  │
│                             │                                           │
└─────────────────────────────┼───────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         Tools Layer                                     │
│ ┌───────────┐ ┌────────────┐ ┌───────────┐ ┌───────┐ ┌────────────────┐ │
│ │ read_file │ │ write_file │ │ edit_file │ │ shell │ │ activate_skill │ │
│ └───────────┘ └────────────┘ └───────────┘ └───────┘ └────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

## Components

### Entry Point (`main.go`, `internal/config/`, `internal/app/`)

The entry point wires together all components:

1. **`config.Parse()`** — Parses CLI flags into `config.Settings`
2. **`app.Setup()`** — Initializes shared components:
   - Skills manager (loads skill metadata from `--skill` directories)
   - Tools (`read_file`, `edit_file`, `write_file`, `shell`, `activate_skill`)
   - System prompt (default + skills fragment + AGENTS.md + current working directory)
3. **Adaptor creation** — Starts either the terminal or PlainIO adaptor

### Adaptors Layer (`internal/adaptors/`)

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

#### Terminal Adaptor (`internal/adaptors/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main Bubble Tea model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling |
| `InputModel` | Handles user text input with external editor support |
| `ModelSelector` | Modal for switching between AI models |
| `QueueManager` | Modal for managing the task queue |
| `ThemeSelector` | Modal for switching between color themes |
| `OutputWriter` | Parses TLV from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

#### PlainIO Adaptor (`internal/adaptors/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, and tool call headers. Suppresses tool result content. Reads prompts from stdin (one per line, backslash continuation).

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

| Component | Description |
|-----------|-------------|
| `Session` | Main struct managing conversation state and message history |
| `Task Queue` | FIFO queue for pending prompts and commands |
| `ModelManager` | Loads and manages AI model configurations from `model.conf`. Never writes to the file. |
| `RuntimeManager` | Persists runtime settings (active model, active theme) to `runtime.conf` |
| `CommandRegistry` | Declarative registration of session commands (`:save`, `:cancel`, etc.) |

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

| Tool | Description | Safety |
|------|-------------|--------|
| `read_file` | Read file contents with optional line ranges | Safe |
| `edit_file` | Search/replace edits on existing files | Medium |
| `write_file` | Create or overwrite files | Dangerous |
| `shell` | Execute shell commands (bash) | Most Dangerous |
| `activate_skill` | Load and execute Agent Skills | Medium |

Each tool is implemented with type-safe input structs and auto-generated JSON schemas. See [schema-improvements.md](schema-improvements.md) for the pattern.

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

## System Prompt Architecture

The system prompt is built in layers:

```
Default Prompt (identity + rules)
    ↓
+ Skills Fragment (if skills configured — XML format with name, description, location)
    ↓
+ Current working directory
    ↓
+ Extra System Prompt (from --system flag, repeatable)
```

For Anthropic APIs with `prompt_cache: true`, `cache_control` markers are applied to the default and extra system prompts separately for optimal caching.

## Data Flow

### Startup Flow

```
main.go → config.Parse() → Settings
                ↓
        app.Setup(Settings)
                ↓
        ├── skills.NewManager(skillPaths)
        ├── tools.NewReadFileTool(), etc.
        └── Build system prompt
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
2. **Task Queue** — Async task processing with cancellation support. Queued tasks execute sequentially.
3. **Virtual Scrolling** — Only visible windows are rendered. 3.5x faster than naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md).
4. **Domain Errors** — Structured error types with operation context for consistent error handling.
5. **Command Registry** — Declarative command registration for extensibility.
6. **Interface Abstraction** — OutputWriter interface for testability.
7. **Provider Factory** — Decoupled provider creation from session logic.
8. **Typed Tools** — `TypedExecute[T]` wrapper for type-safe tool implementations with auto-generated schemas.
9. **Lazy Agent Init** — Agent and provider are created on first use, not at startup.
10. **Sequential Tool Execution** — Tools execute one at a time. See [sequential-tool-execution.md](sequential-tool-execution.md).

## Gotchas

Non-obvious patterns that have caused bugs. Read carefully when modifying related code.

### Agent step messages must not be reconstructed

The provider's `StepCompleteEvent.Messages` contains the complete assistant message — text, reasoning, and tool calls all together. The agent must use these messages as-is, not reconstruct a new message from just the tool calls. Reconstruction loses text content that the LLM returned alongside tool calls. See `agent.go` → `processStreamEvents`.

### Sentinel values must never be overwritten

`WindowBuffer.dirtyIndex` uses a sentinel (`fullRebuild = -2`) to signal that all windows need recalculation. State transitions must check whether the sentinel is already set before overwriting — an `else` branch that blindly assigns a new index can downgrade a full-rebuild to a single-window update, silently dropping windows from the display. See `window.go` → `markDirty`.

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
- Uses **automatic caching**: single `cache_control: {"type": "ephemeral"}` applied to system prompts
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

## File Organization

```
alayacore/
├── main.go                         # Entry point
├── go.mod / go.sum                 # Go module
├── Makefile                        # Build targets
├── docs/                           # Documentation
├── misc/samples/skills/            # Example skills
└── internal/
    ├── adaptors/
    │   ├── terminal/               # Bubble Tea TUI (28 files)
    │   └── plainio/                # Plain stdin/stdout mode (5 files)
    ├── agent/                      # Session, task queue, model/runtime management (12 files)
    ├── app/                        # Shared initialization and system prompt (1 file)
    ├── config/                     # CLI flag parsing and config types (4 files)
    ├── debug/                      # Debug HTTP server (1 file)
    ├── errors/                     # Structured domain errors (3 files)
    ├── llm/                        # Agent loop, providers, types, schemas (17 files)
    ├── skills/                     # Skill loading and management (4 files)
    ├── stream/                     # Stream utilities (4 files)
    └── tools/                      # Built-in tool implementations (8 files)
```
