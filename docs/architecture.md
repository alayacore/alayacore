# AlayaCore Architecture

This document describes the architecture of AlayaCore, an AI assistant with terminal and web interfaces.

## Overview

AlayaCore follows a layered architecture with clear separation of concerns:

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
│  ┌─────────────────────┐     ┌──────────────────────────────────────┐   │
│  │   Terminal Adaptor  │     │         WebSocket Adaptor            │   │
│  │   (Bubble Tea TUI)  │     │         (HTTP/WebSocket)             │   │
│  └──────────┬──────────┘     └───────────────┬──────────────────────┘   │
│             │   ┌──────────────────────────┐ │                          │
│             │   │     PlainIO Adaptor      │ │                          │
│             │   │   (plain stdin/stdout)   │ │                          │
│             │   └──────────────┬───────────┘ │                          │
└─────────────┼──────────────────┼─────────────┼──────────────────────────┘
              │                                │
              │         TLV Protocol           │
              │  (Tag-Length-Value Messages)   │
              │                                │
┌─────────────┼────────────────────────────────┼──────────────────────────┐
│             ▼                                ▼                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                      Session Layer                               │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐   │   │
│  │  │ Task Queue  │  │   Model     │  │      Runtime            │   │   │
│  │  │ (FIFO)      │  │  Manager    │  │      Manager            │   │   │
│  │  └─────────────┘  └─────────────┘  └─────────────────────────┘   │   │
│  └──────────────────────────┬───────────────────────────────────────┘   │
│                             │                                           │
└─────────────────────────────┼───────────────────────────────────────────┘
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
│  ┌──────────┐ ┌───────────┐ ┌──────────┐ ┌────────────┐ ┌────────────┐  │
│  │read_file │ │write_file │ │edit_file │ │posix_shell │ │activate_   │  │
│  │          │ │           │ │          │ │            │ │skill       │  │
│  └──────────┘ └───────────┘ └──────────┘ └────────────┘ └────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

## Component Descriptions

### Entry Point (`main.go`, `internal/config/`, `internal/app/`)

The entry point wires together all components:

1. **config.Parse()** - Parses CLI flags into `config.Settings`
2. **app.Setup()** - Initializes shared components:
   - Skills manager (loads skill metadata)
   - Tools (read_file, edit_file, write_file, posix_shell, activate_skill)
   - System prompt (default + skills fragment + AGENTS.md + cwd)
3. **Adaptor creation** - Terminal, WebSocket, or PlainIO adaptor starts

### Adaptors Layer

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

#### Terminal Adaptor (`internal/adaptors/terminal/`)
- **Terminal**: Main Bubble Tea model composing all UI components
- **DisplayModel**: Renders assistant output with virtual scrolling
- **InputModel**: Handles user text input with external editor support
- **StatusModel**: Shows session status (tokens, queue, steps, model info)
- **ModelSelector**: Modal for switching between AI models
- **QueueManager**: Modal for managing the task queue
- **OutputWriter**: Parses TLV from session and renders styled content
- **WindowBuffer**: Virtual scrolling buffer for display windows
- **Theme**: Customizable color scheme (Catppuccin Mocha default)

#### WebSocket Adaptor (`internal/adaptors/websocket/`)
- HTTP server with WebSocket upgrade
- Each client gets its own session
- Embedded HTML chat UI

#### PlainIO Adaptor (`internal/adaptors/plainio/`)
- Plain stdin/stdout mode, activated with `--plainio`
- `--text-only` suppresses everything except user prompts and assistant text
- Reads prompts from stdin (one per line, backslash continuation)
- Prints plain text to stdout (no ANSI codes)
- Ctrl-D exits gracefully (code 0), Ctrl-C sends `:cancel_all` and exits (code 1)

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

- **Session**: Main session struct managing conversation state
- **Task Queue**: FIFO queue for pending prompts/commands
- **ModelManager**: Loads and manages AI model configurations (never writes to file)
- **RuntimeManager**: Persists runtime settings (active model name)

### Agent Layer (`internal/llm/`)

The agent layer handles language model interaction and tool-calling orchestration.

- **Agent**: Tool-calling loop orchestration with max steps limit
- **Provider interface**: Streaming LLM abstraction
- **Factory**: Creates providers based on protocol type
- **Providers**: Anthropic, OpenAI implementations
- **Types**: Message, ContentPart, StreamEvent definitions
- **Typed helpers**: Type-safe tool execution via `TypedExecute`

**Key Pattern - Callback Streaming:**
```go
Agent.Stream(ctx, messages, llm.StreamCallbacks{
    OnTextDelta:      func(delta string) error { ... },
    OnReasoningDelta: func(delta string) error { ... },  // For thinking tokens
    OnToolCall:       func(id, name string, input json.RawMessage) error { ... },
    OnToolResult:     func(id string, output ToolResultOutput) error { ... },
    OnStepStart:      func(step int) error { ... },
    OnStepFinish:     func(msgs []Message, usage Usage) error { ... },
})
```
Messages are appended incrementally in `OnStepFinish` so they're preserved even if user cancels.

### Tools Layer (`internal/tools/`)

Tools are functions the AI can call to interact with the system.

| Tool | Description | Safety |
|------|-------------|--------|
| `read_file` | Read file contents (supports line ranges) | Safe |
| `edit_file` | Search/replace edits | Medium |
| `write_file` | Create/overwrite files | Dangerous |
| `activate_skill` | Load and execute skills | Medium |
| `posix_shell` | Execute shell commands | Most Dangerous |

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
1. User types "Hello" in terminal
2. Terminal adaptor emits: TLV(TU, "Hello")
3. Session reads TLV, creates UserPrompt task
4. Session processes prompt with model
5. Session writes: TLV(TA, "Hi! How can I help?")
6. Terminal adaptor parses TLV, renders styled content
```

## System Prompt Architecture

AlayaCore uses a dual system prompt architecture:

1. **Default System Prompt** (`app.DefaultSystemPrompt`): Base identity and rules
2. **Extra System Prompt** (`--system` flag): User-provided additions

The system prompt is built in layers:
```
Default Prompt
    ↓
+ Skills Fragment (if skills configured)
    ↓
+ Current working directory
    ↓
+ Extra System Prompt (from --system flag)
```

For Anthropic APIs with `prompt_cache: true`, cache_control markers are applied to the default and extra system prompts separately for optimal caching.

## Configuration

### Model Configuration (`~/.alayacore/model.conf`)

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "sk-..."
model_name: "gpt-4o"
context_limit: 128000
prompt_cache: true  # Optional: enables cache_control for Anthropic APIs
---
name: "Ollama Local"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
model_name: "llama3"
context_limit: 32768
```

**Important**: The program NEVER writes to this file. Users must edit it manually.

### Runtime Configuration (`~/.alayacore/runtime.conf`)

```
active_model: "OpenAI GPT-4o"
```

The active model is determined by:
1. If `runtime.conf` has a saved `active_model`, that model is used
2. Otherwise, the **first model** in `model.conf` becomes the active model

### Theme Configuration (`~/.alayacore/themes/`)

Themes are stored as `.conf` files in the themes folder. Each file defines a color scheme:

```
# ~/.alayacore/themes/theme-dark.conf
primary: #89d4fa
dim: #313244
muted: #6c7086
text: #cdd6f4
warning: #f9e2af
error: #f38ba8
success: #a6e3a1
selection: #fab387
cursor: #cdd6f4
added: #a6e3a1
removed: #f38ba8
```

- **Default location**: `~/.alayacore/themes/`
- **Custom location**: Use `--themes /path/to/themes` to specify a different folder
- **Auto-initialization**: If the themes folder doesn't exist, AlayaCore creates it with default `theme-dark.conf` and `theme-light.conf`
- **Switching themes**: Press `Ctrl+P` in the terminal to open the theme selector

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
        terminal.NewAdaptor(appConfig)
                ↓
        Session created with tools, system prompt
```

### User Prompt Flow

```
User Input → InputModel → ChanInput.EmitTLV(TU, prompt)
                                    ↓
Session.readFromInput() ← ReadTLV()
                                    ↓
submitTask(UserPrompt) → Task Queue
                                    ↓
taskRunner() → handleUserPrompt()
                                    ↓
processPrompt() → LLM Agent.Stream()
                                    ↓
Callbacks: OnTextDelta, OnToolCall, etc.
                                    ↓
writeColored(TA, response) → Output
                                    ↓
OutputWriter.Write() → parse TLV
                                    ↓
WindowBuffer.AppendOrUpdate() → Render
                                    ↓
DisplayModel.View() → Terminal UI
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
                ↓
OnToolCall callback → TLV(FN, tool_info) → UI shows pending
                ↓
Agent executes tool: tool.Execute(ctx, input)
                ↓
OnToolResult callback → TLV(FS, result_status) → UI shows result
                ↓
Tool result added to messages
                ↓
Agent continues to next step (if under max_steps)
```

## Key Design Decisions

1. **TLV Protocol**: Simple binary protocol for clean separation between adaptors and session
2. **Task Queue**: Async task processing with cancellation support
3. **Virtual Scrolling**: Handle large outputs efficiently without performance degradation
4. **Domain Errors**: Structured error types with operation context for consistent error handling
5. **Command Registry**: Declarative command registration for extensibility
6. **Interface Abstraction**: OutputWriter interface for testability
7. **Provider Factory**: Decoupled provider creation from session logic
8. **Typed Tools**: `TypedExecute[T]` wrapper for type-safe tool implementations
9. **Lazy Agent Init**: Agent/Provider created on first use, not at startup

## Critical Implementation Gotchas

These are non-obvious patterns that have caused bugs. When modifying related code, read the corresponding section carefully.

### Mutex Deadlock in SwitchModel
Don't hold mutex while calling methods that may need the same mutex.
```
❌ lock → update fields → call method (needs lock) → deadlock
✅ lock → update fields → unlock → call method
```

### OpenAI Tool Call Chunking
Tool arguments arrive in chunks across multiple delta events:
- First chunk: has `id` and `name`
- Subsequent chunks: `id: ""` but correct `index`
- **Must use `index` (not `id`) to associate chunks** - see `openAIStreamState.appendToolCallArgs()`
- When sending back in history, arguments must be JSON-string (not raw JSON) - see `convertMessage()`

### Anthropic Prompt Caching
- System message must be ≥1024 tokens for caching to activate
- Uses **automatic caching**: single `cache_control: {"type": "ephemeral"}` applied to system prompts
- Enabled per-model via `prompt_cache: true` in model.conf (other providers ignore)
- Best for multi-turn conversations where growing message history should be cached automatically

### Terminal Scroll Position
`userMovedCursorAway` must be set for J/K (page scroll), not just j/k (line scroll), or scroll position is lost on focus switch.

### Incomplete Tool Calls on Cancel
When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.

### Tool Result Message Ordering
`OnStepFinish` callback receives complete step messages. For tool-using steps, this includes both the assistant message (with tool calls) AND the tool result message. The `OnToolResult` callback should only send UI notifications, not append to session messages - the agent loop handles message assembly.

### ANSI Escape Sequences Are Not Recursive
When styling text with lipgloss, each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work.

## File Organization

```
alayacore/
├── main.go                    # Entry point
├── internal/
│   ├── adaptors/
│   │   ├── terminal/          # Terminal UI adaptor
│   │   │   ├── adaptor.go     # Adaptor interface and creation
│   │   │   ├── terminal.go    # Main Bubble Tea model
│   │   │   ├── keybinds.go    # Key constants, bindings, and handler
│   │   │   ├── output.go      # TLV parsing and output rendering
│   │   │   ├── window.go      # Virtual scrolling buffer
│   │   │   ├── input_component.go  # Input handling with editor support
│   │   │   ├── interfaces.go  # Interface definitions
│   │   │   ├── model_selector.go   # Model switching UI
│   │   │   ├── queue_manager.go    # Task queue UI
│   │   │   ├── theme_manager.go    # Theme loading/management
│   │   │   ├── theme_selector.go   # Theme switching UI
│   │   │   ├── styles.go      # Theme definitions and lipgloss styles
│   │   │   ├── tool.go        # Tool display helpers
│   │   │   ├── tool_handler.go    # Tool execution handling
│   │   │   ├── warnings.go    # Warning message handling
│   │   │   └── doc.go         # Package documentation
│   │   ├── websocket/         # WebSocket adaptor
│   │   └── plainio/             # Raw stdin/stdout adaptor (--plainio)
│   ├── agent/
│   │   ├── session.go         # Session management
│   │   ├── session_io.go      # Session I/O and task handling
│   │   ├── session_persist.go # Session persistence (TLV format)
│   │   ├── command_registry.go    # Command registration
│   │   ├── model_manager.go   # Model config loading (never writes)
│   │   └── runtime_manager.go # Active model/theme persistence
│   ├── app/
│   │   └── app.go             # App initialization, system prompt
│   ├── config/
│   │   ├── config.go          # CLI flag parsing
│   │   └── version.go         # Version constant
│   ├── debug/
│   │   └── http.go            # HTTP client with proxy/debug support
│   ├── stream/                # TLV protocol
│   ├── errors/                # Domain errors
│   ├── skills/
│   │   ├── loader.go          # Skill discovery/loading
│   │   ├── manifest.go        # Skill metadata parsing
│   │   └── types.go           # Skill types
│   ├── tools/                 # Agent tools
│   │   ├── read_file.go
│   │   ├── edit_file.go
│   │   ├── write_file.go
│   │   ├── posix_shell.go
│   │   └── activate_skill.go
│   └── llm/
│       ├── agent.go           # Tool-calling loop
│       ├── types.go           # Message, ContentPart, StreamEvent
│       ├── helpers.go         # Message constructors and tool builder
│       ├── typed.go           # TypedExecute wrapper
│       ├── schema.go          # JSON schema generation from struct tags
│       ├── factory/           # Provider factory
│       │   └── provider_factory.go
│       └── providers/         # LLM provider implementations
│           ├── anthropic.go
│           └── openai.go
├── cmd/
│   └── alayacore-web/         # Web server binary
└── docs/
    └── architecture.md        # This document
```
