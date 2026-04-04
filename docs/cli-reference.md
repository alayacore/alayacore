# CLI Reference

## Usage

Simply run:

```sh
alayacore
```

On first run, AlayaCore automatically creates a default model config at `~/.alayacore/model.conf` configured for Ollama:

```
---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
---
```

To use other providers, edit the config file (press `Ctrl+L` then `e` in the terminal, or edit directly).

Running with skills:
```sh
alayacore --skill ~/playground/alayacore/misc/samples/skills/
```


## CLI Flags

| Flag | Description |
|------|-------------|
| `--model-config string` | Model config file path (default: `~/.alayacore/model.conf`) |
| `--runtime-config string` | Runtime config file path (default: `~/.alayacore/runtime.conf`) |
| `--system string` | Extra system prompt (can be specified multiple times) |
| `--skill strings` | Skill path (can be specified multiple times) |
| `--session string` | Session file path to load/save conversations |
| `--proxy string` | HTTP proxy URL (e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) |
| `--themes string` | Themes folder path (default: `~/.alayacore/themes`) |
| `--max-steps int` | Maximum agent loop steps (default: 100) |
| `--auto-summarize` | Automatically summarize conversation when context exceeds 80% of limit |
| `--auto-save` | Automatically save session after each response when `--session` is specified (default: enabled) |
| `--plainio` | Use plain stdin/stdout mode instead of terminal UI |
| `--text-only` | Only show user prompts and assistant text (requires `--plainio`) |
| `--debug-api` | Write raw API requests and responses to log file |
| `--version` | Show version information |
| `--help` | Show help information |

## Examples

```sh
# Basic usage (loads models from ~/.alayacore/model.conf)
alayacore

# With custom model config
alayacore --model-config ./my-model.conf

# With custom runtime config
alayacore --runtime-config ./my-runtime.conf

# With session persistence
alayacore --session ~/my-session.md

# With session auto-save
alayacore --session ~/my-session.md --auto-save

# With multiple skill directories
alayacore --skill ./skills1 --skill ./skills2

# With HTTP proxy
alayacore --proxy http://127.0.0.1:7890

# With SOCKS5 proxy
alayacore --proxy socks5://127.0.0.1:1080

# With custom themes folder
alayacore --themes ./my-themes

# Debug API requests
alayacore --debug-api

# Plain IO mode (stdin/stdout, no terminal UI)
alayacore --plainio

# Piped input
echo "what is 2+2?" | alayacore --plainio

# Text-only mode (just user prompts and assistant text)
echo "what is 2+2?" | alayacore --plainio --text-only

# Show version
alayacore --version
```


## Model Config File

The model config file uses a simple key-value format. If the file doesn't exist or is empty, AlayaCore automatically creates it with a default Ollama configuration.

```
name: "Display Name"
protocol_type: "openai"        # or "anthropic"
base_url: "https://api.example.com/v1"
api_key: "your-api-key"
model_name: "model-identifier"
context_limit: 128000          # optional, 0 = unlimited
prompt_cache: true             # optional, enables cache_control for Anthropic
```

Separate multiple models with `---`:

```
name: "Model 1"
protocol_type: "openai"
base_url: "https://api1.example.com/v1"
api_key: "key1"
model_name: "model-1"
---
name: "Model 2"
protocol_type: "anthropic"
base_url: "https://api2.example.com"
api_key: "key2"
model_name: "model-2"
```

The first model in the file becomes the active model on startup (unless `runtime.conf` has a saved preference).


## Terminal Controls

### Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `J` | Move screen down (when display focused) |
| `K` | Move screen up (when display focused) |
| `g` | Go to first window and top of display (when display focused) |
| `G` | Go to last window and bottom of display (when display focused) |
| `H` | Move cursor to window at top of visible area (when display focused) |
| `L` | Move cursor to window at bottom of visible area (when display focused) |
| `M` | Move cursor to window at center of visible area (when display focused) |

### Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
| `Ctrl+L` | Open model selector UI |
| `Ctrl+P` | Open theme selector UI |
| `Ctrl+Q` | Open task queue manager UI |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle wrap mode for active window (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |

### Commands

| Command | Action |
|---------|--------|
| `:save [filename]` | Save session to file (uses `--session` path if no filename) |
| `:cancel` | Cancel current request (with confirmation) |
| `:cancel_all` | Cancel current request and clear the task queue |
| `:summarize` | Summarize conversation to reduce token usage |
| `:quit`, `:q` | Exit with confirmation (press y/n) |
| `:model_set <id>` | Switch to a saved model configuration |
| `:model_load` | Load model configurations from default config file |


## Session Persistence

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Auto-save**: Enabled by default. When `--session` is specified, the session is automatically saved to the session file after each task completes (user prompt or command). If `--session` is not provided, auto-save is silently skipped. Use `--auto-save=false` to disable.
- **Load**: On startup, AlayaCore creates a new empty session unless you specify `--session` to load an existing one
- **Auto-summarize**: When `--auto-summarize` is enabled and `context_limit` is set in the model config, AlayaCore automatically triggers `:summarize` when context reaches 80% of the limit. This is disabled by default; use `:summarize` to manually reduce context at any time.

Session files use TLV-encoded binary format with YAML frontmatter for metadata. See [architecture.md](architecture.md) for format details.


## Window Container

The terminal organizes concurrent streams into separate windows with synchronized widths:

- **Window Cursor**: Use `j`/`k` to navigate between windows. The cursor defaults to the newest window.
- **Auto-follow**: When new windows appear, cursor moves to them automatically. Pressing `k`, `g`, `H`, `L`, or `M` disables follow; returning to the last window re-enables it.
- **Wrap mode**: Press `Space` to toggle wrap mode on the active window, showing only the last 3 lines.


## Plain IO Mode

Use `--plainio` to run AlayaCore as a plain stdin/stdout process with no terminal UI. This is useful for scripting, piping, or headless environments.

### Input

- Each line from stdin is a separate prompt
- A trailing backslash (`\`) continues the prompt on the next line:

```sh
# Multi-line prompt
alayacore --plainio
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin, waits for queued tasks to finish, exits with code `0`
- **Ctrl-C** (SIGINT): sends `:cancel_all`, exits with code `1`
- Errors cause exit with a negative return code

### Output

All output is plain text with no ANSI codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `> prompt` |
| Tool calls | `[tool_name]` |
| Tool results | Printed as-is |
| Errors | `Error: message` |
| Notifications | `[message]` |

A blank line separates completed tasks from the next prompt.

### Text-only mode

Add `--text-only` to suppress everything except user prompts and assistant text:

```sh
echo "what is 2+2?" | alayacore --plainio --text-only
```

This hides tool calls, tool results, reasoning, errors, and notifications.

### Piped Example

```sh
echo "what is 2+2?" | alayacore --plainio
```
