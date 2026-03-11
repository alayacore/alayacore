# AlayaCore

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.

## Installation

```sh
go install github.com/wallacegibbon/alayacore@latest
go install github.com/wallacegibbon/alayacore/cmd/alayacore-web@latest
```

Or build from source:

```sh
git clone https://github.com/wallacegibbon/alayacore.git
cd alayacore
go build
go build ./cmd/alayacore-web/
```

## Usage

All configuration must be specified via command line flags:

```sh
# Local Ollama OpenAI-compatible server
alayacore --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3

# Local Ollama Anthropic-compatible server
alayacore --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b

# MiniMax (Anthropic-compatible)
alayacore --type anthropic --base-url $MINIMAXI_API_URL --api-key $MINIMAXI_API_KEY --model MiniMax-M2.5

# DeepSeek (OpenAI-compatible)
alayacore --type openai --base-url $DEEPSEEK_API_URL --api-key $DEEPSEEK_API_KEY --model deepseek-chat

# ZAI (OpenAI-compatible)
alayacore --type openai --base-url $ZAI_API_URL --api-key $ZAI_API_KEY --model GLM-4.7
```

Running with skills
```sh
alayacore --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b --skill ~/playground/alayacore/misc/samples/skills/
```

## Web Server

`alayacore-web` runs a WebSocket server with a built-in chat UI.

```sh
# Start WebSocket server
alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Custom address
alayacore-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4 --addr :9090
```

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`
- Each browser tab gets its own independent agent session

## Flags

- `-type string` - Provider type: `anthropic` or `openai` (required)
- `-base-url string` - API endpoint URL (required)
- `-api-key string` - API key (required)
- `-model string` - Model name to use
- `-system string` - Override system prompt
- `-skill string` - Skills directory path (can be specified multiple times)
- `-session string` - Session file path to load/save conversations
- `-proxy string` - HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5, e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`)
- `-context-limit string` - Provider context window size in tokens. Supports K/M suffixes (e.g., `200K`, `1M`). When set, status bar shows `Context: N / LIMIT (X.X%)` instead of just `Context: N`
- `-debug-api` - Write raw API requests and responses to log file
- `-version` - Show version information
- `-help` - Show help information

## Features

- Tools: read_file, todo_read, todo_write, edit_file, write_file, activate_skill, posix_shell
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
- Todo list management for task tracking
- Session file persistence
- HTTP/HTTPS/SOCKS5 proxy support

## Terminal Controls

When running the Terminal version:

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
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

## Session Commands

- `:save [filename]` - Save session to file (uses `--session` path if no filename)
- `:cancel` - Cancel current request and clear todos (with confirmation)
- `:summarize` - Summarize conversation to reduce token usage
- `:quit`, `:q` - Exit with confirmation

## Project Status

See [STATE.md](STATE.md) for detailed implementation status and architecture.

## License

MIT
