# AlayaCore

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.

## Installation

```sh
go install github.com/alayacore/alayacore@latest
```

## Flags

- `--model-config string` - Model config file path (default: `~/.alayacore/model.conf`)
- `--runtime-config string` - Runtime config file path (default: `<model-config-dir>/runtime.conf`, or `~/.alayacore/runtime.conf`)
- `--system string` - Extra system prompt (can be specified multiple times)
- `--skill strings` - Skill path (can be specified multiple times)
- `--session string` - Session file path to load/save conversations
- `--proxy string` - HTTP proxy URL (e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`)
- `--themes string` - Themes folder path (default: `~/.alayacore/themes`)
- `--max-steps int` - Maximum agent loop steps (default: 100)
- `--auto-summarize` - Automatically summarize conversation when context exceeds 80% of limit
- `--auto-save` - Automatically save session after each response when `--session` is specified (default: enabled)
- `--plainio` - Use plain stdin/stdout mode instead of terminal UI
- `--text-only` - Only show user prompts and assistant text (requires `--plainio`)
- `--debug-api` - Write raw API requests and responses to log file
- `--version` - Show version information
- `--help` - Show help information

## Features

- Tools: read_file, edit_file, write_file, activate_skill, posix_shell
- Multi-step conversations with tool calls
- Token usage tracking
- Error handling for command execution
- Multi-provider support (OpenAI, Anthropic, DeepSeek, Qwen, Ollama, LM Studio)
- Interactive mode
- Real-time streaming output
- Color-styled output
- Custom system prompts
- Read prompts from files
- API debug mode for HTTP requests and responses
- Skills system (agentskills.io compatible)
- Session file persistence
- Auto-save session (`--auto-save`)
- HTTP/HTTPS/SOCKS5 proxy support
- Plain IO mode (stdin/stdout) for scripting and piping (`--plainio`)
- Text-only mode for clean output (`--plainio --text-only`)

## Model Configuration

AlayaCore uses a model configuration file to store model configurations.

- **Default location**: `~/.alayacore/model.conf`
- **Custom location**: Use `--model-config /path/to/model.conf` to specify a different file

**Auto-initialization**: If the config file doesn't exist or is empty, AlayaCore automatically creates it with a default Ollama configuration.

**Note**: After auto-initialization, the program NEVER writes to this file automatically. You must edit it manually with a text editor.

### Model Config File Format

The config file uses a simple key-value format with `---` as a separator between models:

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
```

**Fields:**
- `name`: Display name for the model
- `protocol_type`: "openai" or "anthropic"
- `base_url`: API server URL
- `api_key`: Your API key
- `model_name`: Model identifier
- `context_limit`: Maximum context length (optional, 0 means unlimited)
- `prompt_cache`: Enable prompt caching for Anthropic APIs (optional, adds `cache_control` markers)

### Model Selection Logic

1. On startup, AlayaCore reads the model config file (from `--model-config` or default location)
2. If the config file doesn't exist or is empty, it's auto-initialized with a default Ollama configuration
3. The **first model** in the config file becomes the active model (unless `runtime.conf` has a saved preference)

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
| `Ctrl+P` | Open theme selector UI |
| `Ctrl+Q` | Open task queue manager UI |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `J` | Scroll down one line (when display focused) |
| `K` | Scroll up one line (when display focused) |
| `g` | Go to first window and top of display (when display focused) |
| `G` | Go to last window and bottom of display (when display focused) |
| `H` | Move cursor to window at top of visible area (when display focused) |
| `L` | Move cursor to window at bottom of visible area (when display focused) |
| `M` | Move cursor to window at center of visible area (when display focused) |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle fold mode for active window (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |
| `:cancel` | Cancel current request (with confirmation) |
| `:quit`, `:q` | Exit with confirmation (press y/n) |

## Plain IO Mode

Use `--plainio` to run AlayaCore as a plain stdin/stdout process with no terminal UI. This is useful for scripting, piping, or headless environments.

### Input

- Each line from stdin is treated as a separate prompt.
- A trailing backslash (`\`) before newline continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin. The program waits for queued tasks to finish, then exits with code `0`.
- **Ctrl-C** (SIGINT): sends `:cancel_all` and exits with code `1`.
- Errors cause exit with code `1`.

### Output

All output is printed to stdout in plain text (no ANSI codes):

- Assistant text and reasoning are printed directly.
- User prompts are prefixed with `> `.
- Tool calls are shown as `[tool_name]`.
- Tool results are printed as-is (unescaped).
- Errors are prefixed with `Error: `.
- Notifications are prefixed with `[...]`.
- A blank line separates completed tasks from the next prompt.

### Example

```sh
echo "what is 2+2?" | alayacore --plainio
```

### Text-only mode

Add `--text-only` to suppress everything except user prompts and assistant text:

```sh
echo "what is 2+2?" | alayacore --plainio --text-only
```

This hides tool calls, tool results, reasoning, errors, and notifications.

## Window Container

The terminal organizes concurrent streams into separate windows with synchronized widths. Stream IDs include monotonic suffixes to prevent collisions across conversation turns.

### Window Cursor

A Window Cursor highlights one window with a bright border. Use `j`/`k` to navigate. The cursor stays visible during scrolling and defaults to the newest window. Press `Space` to toggle fold mode on the active window, which collapses content to first line + indicator + last 3 lines.

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
- `:cancel_all` - Cancel current request and clear the task queue
- `:summarize` - Summarize conversation to reduce token usage
- `:quit`, `:q` - Exit with confirmation
- `:taskqueue_get_all` - Get all queued tasks (internal use)
- `:taskqueue_del <id>` - Delete a queued task by ID (internal use)

## Model Management Commands

- `:model_set <id>` - Switch to a saved model configuration
- `:model_load` - Load model configurations from default config file

## Architecture

AlayaCore follows a layered architecture with clean separation via the TLV protocol. For details, see [docs/architecture.md](docs/architecture.md) and [docs/cli-reference.md](docs/cli-reference.md).

## License

MIT
