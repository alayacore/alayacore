# CLI Reference

## Usage

AlayaCore loads models from a configuration file. Create `~/.alayacore/model.conf`:

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

Running with skills:
```sh
alayacore --skill ~/playground/alayacore/misc/samples/skills/
```


## CLI Flags

| Flag | Description |
|------|-------------|
| `-model-config string` | Model config file path (default: `~/.alayacore/model.conf`) |
| `-runtime-config string` | Runtime config file path (default: same dir as model-config/runtime.conf) |
| `-system string` | Extra system prompt (can be specified multiple times) |
| `-skill string` | Skills directory path (can be specified multiple times) |
| `-session string` | Session file path to load/save conversations |
| `-proxy string` | HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5 proxies, e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) |
| `-max-steps int` | Maximum agent loop steps (default: 100) |
| `-debug-api` | Write raw API requests and responses to log file |
| `-version` | Show version information |
| `-help` | Show help information |


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

# With multiple skill directories
alayacore --skill ./skills1 --skill ./skills2

# With HTTP proxy
alayacore --proxy http://127.0.0.1:7890

# With SOCKS5 proxy
alayacore --proxy socks5://127.0.0.1:1080

# Debug API requests
alayacore --debug-api

# Show version
alayacore --version
```


## Model Config File

The model config file uses a simple YAML-like format:

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
| `:summarize` | Summarize conversation to reduce token usage |
| `:quit`, `:q` | Exit with confirmation (press y/n) |
| `:model_set <id>` | Switch to a saved model configuration |
| `:model_load` | Load model configurations from default config file |


## Session Persistence

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Load**: On startup, AlayaCore creates a new empty session unless you specify `--session` to load an existing one
- **Auto-summarize**: When `context_limit` is set in the model config, AlayaCore automatically triggers `:summarize` when context reaches 80% of the limit

Session files use TLV-encoded binary format with YAML frontmatter for metadata. See [architecture.md](architecture.md) for format details.


## Window Container

The terminal organizes concurrent streams into separate windows with synchronized widths:

- **Window Cursor**: Use `j`/`k` to navigate between windows. The cursor defaults to the newest window.
- **Auto-follow**: When new windows appear, cursor moves to them automatically. Pressing `k`, `g`, `H`, `L`, or `M` disables follow; returning to the last window re-enables it.
- **Wrap mode**: Press `Space` to toggle wrap mode on the active window, showing only the last 3 lines.


## Web Server

`alayacore-web` runs a WebSocket server with a built-in chat UI:

```sh
# Start server on default port (:8080)
alayacore-web

# Custom port
alayacore-web --addr :9090
```

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`
- Each browser tab gets its own independent agent session
