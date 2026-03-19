# AlayaCore Architecture

This document describes the architecture of AlayaCore, an AI assistant with terminal and web interfaces.

## Overview

AlayaCore follows a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────────┐
│                         Adaptors Layer                          │
│  ┌─────────────────────┐     ┌──────────────────────────────┐   │
│  │   Terminal Adaptor  │     │     WebSocket Adaptor        │   │
│  │   (Bubble Tea TUI)  │     │     (HTTP/WebSocket)         │   │
│  └──────────┬──────────┘     └───────────────┬──────────────┘   │
└─────────────┼────────────────────────────────┼──────────────────┘
              │                                │
              │         TLV Protocol           │
              │  (Tag-Length-Value Messages)   │
              │                                │
┌─────────────┼────────────────────────────────┼──────────────────┐
│             ▼                                ▼                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Session Layer                         │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐   │   │
│  │  │ Task Queue  │  │   Model     │  │    Command      │   │   │
│  │  │ (FIFO)      │  │  Manager    │  │    Registry     │   │   │
│  │  └─────────────┘  └─────────────┘  └─────────────────┘   │   │
│  └──────────────────────────┬───────────────────────────────┘   │
│                             │                                   │
└─────────────────────────────┼───────────────────────────────────┘
                              │
                              ▼
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Agent Layer                           │   │
│  │  ┌─────────────────────────────────────────────────────┐ │   │
│  │  │              LLM Package                            │ │   │
│  │  │  (Language Model + Tool Calling)                    │ │   │
│  │  └─────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────┬───────────────────────────────┘   │
│                             │                                   │
└─────────────────────────────┼───────────────────────────────────┘
                              │
                              ▼
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Tools Layer                           │   │
│  │  ┌──────────┐ ┌───────────┐ ┌──────────┐ ┌────────────┐  │   │
│  │  │read_file │ │write_file │ │edit_file │ │posix_shell │  │   │
│  │  └──────────┘ └───────────┘ └──────────┘ └────────────┘  │   │
│  │  ┌──────────────┐                                        │   │
│  │  │activate_skill│                                        │   │
│  │  └──────────────┘                                        │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Component Descriptions

### Adaptors Layer

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

#### Terminal Adaptor (`internal/adaptors/terminal/`)
- **Terminal**: Main Bubble Tea model composing all UI components
- **DisplayModel**: Renders assistant output with virtual scrolling
- **InputModel**: Handles user text input with external editor support
- **StatusModel**: Shows session status (tokens, queue, model info)
- **ModelSelector**: Modal for switching between AI models
- **QueueManager**: Modal for managing the task queue
- **OutputWriter**: Parses TLV from session and renders styled content
- **WindowBuffer**: Virtual scrolling buffer for display windows

#### WebSocket Adaptor (`internal/adaptors/websocket/`)
- HTTP server with WebSocket upgrade
- Each client gets its own session
- Embedded HTML chat UI

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

- **Session**: Main session struct managing conversation state
- **Task Queue**: FIFO queue for pending prompts/commands
- **ModelManager**: Loads and manages AI model configurations
- **RuntimeManager**: Persists runtime settings (active model)
- **CommandRegistry**: Declarative command registration

### Agent Layer (`internal/llm/`)

The agent layer handles language model interaction and tool-calling orchestration.

- **Agent**: Tool-calling loop orchestration
- **Provider interface**: Streaming LLM abstraction
- **Providers**: Anthropic, OpenAI implementations
- **Types**: Message, ContentPart, StreamEvent definitions

**Key Pattern - Callback Streaming:**
```go
Agent.Stream(ctx, messages, llm.StreamCallbacks{
    OnTextDelta:    func(delta string) error { ... },
    OnToolCall:     func(id, name string, input json.RawMessage) error { ... },
    OnStepFinish:   func(msgs []Message, usage Usage) error { ... },
})
```
Messages are appended incrementally in `OnStepFinish` so they're preserved even if user cancels.

**Stream ID Format:** Content is tagged with IDs like `[:0-1-abc123:]`:
- `0` = prompt counter (increments per user message)
- `1` = step number within that prompt
- `abc123` = tool call ID or `t` (text) / `r` (reasoning)

This allows the terminal to route content to correct windows across multi-step tool calls.

### Tools Layer (`internal/tools/`)

Tools are functions the AI can call to interact with the system.

| Tool | Description | Safety |
|------|-------------|--------|
| `read_file` | Read file contents | Safe |
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
| `TagFunctionNotify` | FN | Output | Function call for display |
| `TagFunctionCall` | FC | Output | Function call for persistence |
| `TagFunctionResult` | FR | Output | Function result for persistence |
| `TagFunctionState` | FS | Output | Function state indicator (pending/success/error/default) |
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

## Data Flow

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
processPrompt() → LLM Agent
                                    ↓
writeColored(TA, response) → Output
                                    ↓
OutputWriter.Write() → parse TLV
                                    ↓
WindowBuffer.AppendOrUpdate() → Render
                                    ↓
DisplayModel.View() → Terminal UI
```

### Command Flow

```
User types ":model_set gpt-4"
                ↓
InputModel → ChanInput.EmitTLV(TU, ":model_set gpt-4")
                ↓
Session.readFromInput() → detects ":" prefix
                ↓
submitTask(CommandPrompt) → Task Queue
                ↓
taskRunner() → handleCommandSync("model_set gpt-4")
                ↓
dispatchCommand() → CommandRegistry → handleModelSet()
                ↓
ModelManager.SetActive() → RuntimeManager.Persist()
                ↓
writeNotifyf("Switched to model...") → Output
```

## Configuration

### Model Configuration (`~/.alayacore/model.conf`)

```yaml
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

### Runtime Configuration (`~/.alayacore/runtime.conf`)

```yaml
active_model: "OpenAI GPT-4o"
```

The active model is determined by:
1. If `runtime.conf` has a saved `active_model`, that model is used
2. Otherwise, the **first model** in `model.conf` becomes the active model

## Key Design Decisions

1. **TLV Protocol**: Simple binary protocol for clean separation between adaptors and session
2. **Task Queue**: Async task processing with cancellation support
3. **Virtual Scrolling**: Handle large outputs efficiently without performance degradation
4. **Domain Errors**: Structured error types with operation context for consistent error handling
5. **Command Registry**: Declarative command registration for extensibility
6. **Interface Abstraction**: OutputWriter interface for testability

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
- Uses **automatic caching**: single `cache_control: {"type": "ephemeral"}` at top level of request
- Anthropic automatically applies cache breakpoint to the last cacheable block and moves it forward as conversations grow
- Enabled per-model via `prompt_cache: true` in model.conf (other providers ignore)
- Best for multi-turn conversations where growing message history should be cached automatically

### Terminal Scroll Position
`userMovedCursorAway` must be set for J/K (page scroll), not just j/k (line scroll), or scroll position is lost on focus switch.

### Incomplete Tool Calls on Cancel
When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.

## File Organization

```
alayacore/
├── internal/
│   ├── adaptors/
│   │   ├── terminal/        # Terminal UI adaptor
│   │   │   ├── terminal.go  # Main model
│   │   │   ├── keys.go      # Keyboard handling
│   │   │   ├── commands.go  # Command processing
│   │   │   ├── output.go    # TLV parsing
│   │   │   ├── window*.go   # Virtual scrolling
│   │   │   ├── constants.go # Layout/colors
│   │   │   └── styles.go    # Lipgloss styles
│   │   └── websocket/       # WebSocket adaptor
│   ├── agent/
│   │   ├── session.go       # Session management
│   │   ├── session_*.go     # Session components
│   │   ├── command_registry.go
│   │   └── model_manager.go
│   ├── stream/              # TLV protocol
│   ├── errors/              # Domain errors
│   └── tools/               # Agent tools
├── cmd/
│   └── alayacore-web/       # Web server binary
└── docs/
    └── architecture.md      # This document
```
