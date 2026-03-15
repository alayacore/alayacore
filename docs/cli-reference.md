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
| `-system string` | Override system prompt |
| `-skill string` | Skills directory path (can be specified multiple times) |
| `-session string` | Session file path to load/save conversations |
| `-proxy string` | HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5 proxies, e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) |
| `-context-limit string` | Provider context window size in tokens. Supports K/M suffixes (e.g., `200K`, `1M`). When set, status bar shows `Context: N / LIMIT (X.X%)` instead of just `Context: N` |
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

# With context limit
alayacore --context-limit 200K

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

The last model in the file becomes the active model on startup.


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
