# AlayaCore

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.

## Installation

```sh
go install github.com/alayacore/alayacore@latest
go install github.com/alayacore/alayacore/cmd/alayacore-web@latest
```

Or build from source:

```sh
git clone https://github.com/alayacore/alayacore.git
cd alayacore
go build
go build ./cmd/alayacore-web/
```

## Usage

Create a model config file at `~/.alayacore/model.conf`:

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768
```

Then simply run:

```sh
alayacore
```

The program will load models from the config file. The active model is determined by `runtime.conf` (persisted across sessions). If no active model is set, the first model in the list is used.

Running with skills:
```sh
alayacore --skill ~/playground/alayacore/misc/samples/skills/
```

## Web Server

`alayacore-web` runs a WebSocket server with a built-in chat UI.

```sh
# Start WebSocket server
alayacore-web

# Custom address
alayacore-web --addr :9090
```

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`
- Each browser tab gets its own independent agent session

## Flags

- `-model-config string` - Model config file path (default: `~/.alayacore/model.conf`)
- `-runtime-config string` - Runtime config file path (default: same dir as model-config/runtime.conf)
- `-system string` - Override system prompt
- `-skill string` - Skills directory path (can be specified multiple times)
- `-session string` - Session file path to load/save conversations
- `-proxy string` - HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5, e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`)
- `-context-limit string` - Provider context window size in tokens. Supports K/M suffixes (e.g., `200K`, `1M`). When set, status bar shows `Context: N / LIMIT (X.X%)` instead of just `Context: N`
- `-debug-api` - Write raw API requests and responses to log file
- `-version` - Show version information
- `-help` - Show help information

## Features

- Tools: read_file, edit_file, write_file, activate_skill, posix_shell
- Multi-step conversations with tool calls
- Token usage tracking
- Error handling for command execution
- Multi-provider support (OpenAI, Anthropic, DeepSeek, ZAI)
- Interactive mode
- Real-time streaming output
- Color-styled output
- Custom system prompts
- Read prompts from files
- API debug mode for HTTP requests and responses
- Skills system (agentskills.io compatible)
- Web server with WebSocket support and chat UI
- Session file persistence
- HTTP/HTTPS/SOCKS5 proxy support

## Model Configuration

AlayaCore uses a model configuration file to store model configurations.

- **Default location**: `~/.alayacore/model.conf`
- **Custom location**: Use `--model-config /path/to/model.conf` to specify a different file

**Important: The program NEVER writes to this file automatically.** You must edit it manually with a text editor.

### Model Config File Format

The config file uses a simple YAML-like format with `---` as a separator between models:

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768
```

**Fields:**
- `name`: Display name for the model
- `protocol_type`: "openai" or "anthropic"
- `base_url`: API server URL
- `api_key`: Your API key
- `model_name`: Model identifier
- `context_limit`: Maximum context length (optional, 0 means unlimited)

### Model Selection Logic

1. On startup, AlayaCore reads the model config file (from `--model-config` or default location)
2. The **last model** in the config file becomes the active model
3. If no models are available, the program exits with instructions

### Editing Models

- Press `Ctrl+L` to open the model selector
- Press `e` to open the config file in your editor ($EDITOR or vi)
- Press `r` to reload models after editing
- Press `enter` to select a model

## Terminal Controls

When running the Terminal version:

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
| `Ctrl+L` | Open model selector UI |
| `Ctrl+Q` | Open task queue manager UI |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `J` | Move screen down (when display focused) |
| `K` | Move screen up (when display focused) |
| `g` | Go to first window and top of display (when display focused) |
| `G` | Go to last window and bottom of display (when display focused) |
| `H` | Move cursor to window at top of visible area (when display focused) |
| `L` | Move cursor to window at bottom of visible area (when display focused) |
| `M` | Move cursor to window at center of visible area (when display focused) |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle wrap mode for active window (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |
| `:cancel` | Cancel current request (with confirmation) |
| `:quit`, `:q` | Exit with confirmation (press y/n) |

## Window Container

The terminal organizes concurrent streams into separate windows with synchronized widths. Stream IDs include monotonic suffixes to prevent collisions across conversation turns.

### Window Cursor

A Window Cursor highlights one window with a bright border. Use `j`/`k` to navigate. The cursor stays visible during scrolling and defaults to the newest window. Press `Space` to toggle wrap mode on the active window, which shows only the last 3 lines of content with a `Wrapped - Space to expand` indicator.

## Task Queue Manager

When tasks (prompts or commands) are submitted while a previous task is still running, they are added to a queue. Press `Ctrl+Q` to open the task queue manager:

| Key | Action |
|-----|--------|
| `q`, `esc` | Close queue manager |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `d` | Delete selected task |

Each queued task displays:
- Queue ID (Q1, Q2, etc.)
- Type: `P` (prompt) or `C` (command)
- Truncated content preview

Queue manager shows real-time queue status and allows you to remove pending tasks before they execute.

## Session Commands

- `:save [filename]` - Save session to file (uses `--session` path if no filename)
- `:cancel` - Cancel current request (with confirmation)
- `:summarize` - Summarize conversation to reduce token usage
- `:quit`, `:q` - Exit with confirmation
- `:taskqueue_get_all` - Get all queued tasks (internal use)
- `:taskqueue_del <id>` - Delete a queued task by ID (internal use)

## Model Management Commands

- `:model_set <id>` - Switch to a saved model configuration
- `:model_load` - Load model configurations from default config file

## Architecture

For detailed architecture documentation, see [docs/architecture.md](docs/architecture.md).

### Quick Overview

AlayaCore follows a layered architecture with clean separation via the TLV protocol:

```
┌─────────────────┐                    ┌─────────────────┐
│    Adaptors     │   TLV Messages     │     Session     │
│ (Terminal/Web)  │ ◄────────────────► │   (Agent Core)  │
└────────┬────────┘                    └────────┬────────┘
         │                                      │
         │  Tags: TU (user), TA (assistant),    │
         │        FS (function), SN (notify)    │
         └──────────────────────────────────────┘
```

See [STATE.md](STATE.md) for implementation status.

## License

MIT
