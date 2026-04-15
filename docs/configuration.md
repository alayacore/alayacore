# Configuration

AlayaCore has three configuration files: model config, runtime config, and theme files. All live under `~/.alayacore/` by default.

```
~/.alayacore/
├── model.conf        # LLM provider and model definitions
├── runtime.conf      # Active model/theme selections (auto-managed)
└── themes/
    ├── theme-dark.conf
    └── theme-light.conf
```

## Model Config

**Default location**: `~/.alayacore/model.conf`
**Override**: `--model-config /path/to/model.conf`

This file defines one or more LLM models that AlayaCore can use. It is auto-created with a default Ollama configuration on first run. **AlayaCore never writes to this file after initialization** — you must edit it manually.

### Format

```
name: "Display Name"
protocol_type: "openai"
base_url: "https://api.example.com/v1"
api_key: "your-api-key"
model_name: "model-identifier"
context_limit: 128000
prompt_cache: true
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Display name shown in the model selector |
| `protocol_type` | Yes | `openai` or `anthropic` — determines the API format |
| `base_url` | Yes | API server base URL |
| `api_key` | Yes | API key for authentication |
| `model_name` | Yes | Model identifier sent to the API |
| `context_limit` | No | Maximum context window in tokens. `0` means unlimited. Used for context display and auto-summarization. |
| `prompt_cache` | No | Enable prompt caching for Anthropic APIs. Adds `cache_control` markers to system prompts for reduced latency and cost. Ignored by OpenAI providers. |

### Multiple Models

Separate models with `---`. The first model becomes active on startup (unless `runtime.conf` has a saved preference):

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "sk-..."
model_name: "gpt-4o"
context_limit: 128000
---
name: "Anthropic Claude Sonnet"
protocol_type: "anthropic"
base_url: "https://api.anthropic.com"
api_key: "sk-ant-..."
model_name: "claude-sonnet-4-20250514"
context_limit: 200000
prompt_cache: true
---
name: "Ollama / Qwen3 30B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "qwen3:30b-a3b"
context_limit: 128000
```

### Provider Examples

| Provider | `protocol_type` | `base_url` | Notes |
|----------|----------------|------------|-------|
| OpenAI | `openai` | `https://api.openai.com/v1` | |
| Anthropic | `anthropic` | `https://api.anthropic.com` | Supports `prompt_cache` |
| DeepSeek | `openai` | `https://api.deepseek.com/v1` | OpenAI-compatible |
| Qwen (DashScope) | `openai` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | OpenAI-compatible |
| Ollama | `anthropic` | `http://127.0.0.1:11434` | Anthropic-compatible mode |
| LM Studio | `openai` | `http://127.0.0.1:1234/v1` | OpenAI-compatible |

### Switching Models at Runtime

Press `Ctrl+L` to open the model selector. From there:

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate models |
| `Enter` | Select model |
| `e` | Open config file in `$EDITOR` (or `vim`) |
| `r` | Reload models after editing |

## Runtime Config

**Default location**: `~/.alayacore/runtime.conf`
**Override**: `--runtime-config /path/to/runtime.conf`

Auto-managed by AlayaCore. Persists your active model and theme selections across sessions.

```
active_model: "OpenAI GPT-4o"
active_theme: "theme-dark"
```

### Model Selection Priority

1. If `runtime.conf` has a saved `active_model`, that model is used
2. Otherwise, the **first model** in `model.conf` is active

## Theme Configuration

**Default location**: `~/.alayacore/themes/`
**Override**: `--themes /path/to/themes`

Themes are `.conf` files that define the TUI color scheme. If the themes directory doesn't exist, AlayaCore creates it with `theme-dark.conf` and `theme-light.conf`.

### Theme File Format

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

### Color Roles

| Color | Used for |
|-------|----------|
| `primary` | Tool call headers, skill names, emphasis |
| `dim` | Window borders, separators |
| `muted` | Status bar, secondary text |
| `text` | Body text |
| `warning` | Warnings, pending states |
| `error` | Errors |
| `success` | Success messages, completed states |
| `selection` | Selected items in lists |
| `cursor` | Cursor indicator |
| `added` | Diff additions |
| `removed` | Diff removals |

Switch themes at runtime with `Ctrl+P`.
