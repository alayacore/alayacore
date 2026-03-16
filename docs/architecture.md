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
│  │  │              Fantasy Framework                      │ │   │
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

### Agent Layer

The agent layer interfaces with the Fantasy framework for language model interaction.

- Tool calling orchestration
- Streaming response handling
- Context management

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
| `TagFunctionShow` | FS | Output | Function call for display |
| `TagFunctionCall` | FC | Output | Function call for persistence |
| `TagFunctionResult` | FR | Output | Function result for persistence |
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
processPrompt() → Fantasy Agent
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

## Key Design Decisions

1. **TLV Protocol**: Simple binary protocol for clean separation between adaptors and session
2. **Task Queue**: Async task processing with cancellation support
3. **Virtual Scrolling**: Handle large outputs efficiently without performance degradation
4. **Domain Errors**: Structured error types with operation context for consistent error handling
5. **Command Registry**: Declarative command registration for extensibility
6. **Interface Abstraction**: OutputWriter interface for testability

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
