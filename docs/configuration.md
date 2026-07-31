# Configuration

AlayaCore has three configuration files: model config, runtime config, and theme files. All live under a single config directory — `~/.alayacore/` by default — making it easy to manage, back up, or share your entire configuration.

```
~/.alayacore/
├── model.conf        # LLM provider and model definitions
├── runtime.conf      # Active model/theme selections (auto-managed)
└── themes/
    ├── theme-dark.conf
    ├── theme-light.conf
    └── theme-redpanda.conf
```

## Config Directory

**Default location**: `~/.alayacore/`
**Override**: `--config-path /path/to/config-dir`

Use a single `--config-path` flag to point to any directory with the same layout.
This replaces the old per-file overrides (`--model-config`, `--runtime-config`, `--themes`).

```bash
# Use a custom config directory
alayacore --config-path ./my-project-config

# The directory should contain:
#   model.conf       (required — auto-created if missing)
#   runtime.conf     (auto-managed)
#   themes/          (auto-created with defaults if missing)
```

> **Exception — Skills**: `--skill` is still a separate flag because skill
> directories are project-specific and rarely live inside the config directory.
> You can pass `--skill` multiple times for different paths.

## Model Config

**Location**: `<config-path>/model.conf`

This file defines one or more LLM models that AlayaCore can use. It is auto-created with a default Ollama configuration on first run. Model edits made via the UI (pressing `e` in the model selector) are sent to the session via `:model_sync` and persisted back to this file automatically.

### Format

```
name: "Display Name"
protocol_type: "openai"
base_url: "https://api.example.com/v1"
api_key: "your-api-key"
model_name: "model-identifier"
context_limit: 128000
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
| `max_tokens` | No | Maximum output tokens per response. `0` means use the default (131072). Sent as `max_tokens` for Anthropic, `max_completion_tokens` for OpenAI. Set explicitly for models with lower output limits. |

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
---
name: "Ollama / Qwen3 30B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "qwen3:30b-a3b"
context_limit: 128000
```

### Validation

Models are validated at load time (startup and after `:model_load`). A model is **rejected** if:

- `protocol_type` is missing or not `"openai"` / `"anthropic"`
- `base_url` is missing or not a valid URL
- `model_name` is missing

Rejected models are skipped — they won't appear in the model selector. Errors are printed at startup and shown after `:model_load` as a command failure (`CO` with `is_error:true`, code `MODEL_VALIDATION`). Other valid models in the same file are unaffected.

If two or more models share the same `name`, the first occurrence is kept and subsequent duplicates are **rejected** with an error message. This prevents ambiguity in model selection.

If a field value has the wrong type (e.g. `context_limit: abc`), an error is printed but the model is still loaded with the zero value for that field.

### Switching Models at Runtime

Press `Ctrl+L` to open the model selector. From there:

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate models |
| `Enter` | Select model |
| `e` | Edit models in `$EDITOR` (temp file with model.conf format) |
| `Ctrl+R` | Reload models from config file |

When you select a model:

- **Sessions loaded from a file** (`--session`) store the switch in the session file's frontmatter on next `:save`. The global `runtime.conf` is left untouched — each session keeps its own preference.
- **Sessions without a file** update `runtime.conf` so the choice persists across sessions.

## Runtime Config

**Location**: `<config-path>/runtime.conf`

Auto-managed by AlayaCore. Persists your active model and theme selections across sessions.

```
active_model: "OpenAI GPT-4o"
active_theme: "theme-dark"
```

### Model Selection Priority

When a session starts (or reloads via `:model_load`), the active model is resolved using this priority chain:

1. **`--model` CLI flag** — highest priority. If specified and the name exists in `model.conf`, it overrides everything else.
2. **Session file frontmatter** — if loading a saved session (via `--session`), the `active_model` field in the file's frontmatter is applied next.
3. **Runtime config** — `<config-path>/runtime.conf`. Persisted across sessions. Updated only when switching models in sessions without a file-specified model.
4. **First model** — if none of the above are set or match, the first model in `model.conf` is used.

## Theme Configuration

**Location**: `<config-path>/themes/`

Themes are `.conf` files that define the TUI color scheme. If the themes directory doesn't exist, AlayaCore creates it with three defaults: `theme-dark.conf` (Catppuccin Mocha, the default), `theme-light.conf` (Catppuccin Latte), and `theme-redpanda.conf` (warm reddish-brown palette from [Redpanda](https://github.com/redpanda-data/redpanda-terminal-themes)).

### Theme File Format

```
# ~/.alayacore/themes/theme-dark.conf
primary: #89d4fa
dim: #313244
muted: #6c7086
text: #cdd6f4
warning: #f77923
error: #f38ba8
success: #a6e3a1
selection: #fab387
cursor: #cdd6f4
added: #a6e3a1
removed: #f38ba8
tool: #f9e2af
fold_indicator: "⁝"
```

### Color Roles

| Color | Used for |
|-------|----------|
| `primary` | User input text, prompt display, emphasis, focused borders |
| `dim` | Window borders, separators, status bar |
| `muted` | Secondary text, system messages, reasoning, tool content |
| `text` | Body text |
| `warning` | Confirm dialogs, multi-line prompt hints, attachment labels |
| `error` | Errors |
| `success` | Success messages, completed states |
| `selection` | Selected items in lists, cursor border highlight |
| `cursor` | Cursor indicator |
| `tool` | Tool call headers/labels |
| `added` | Diff additions |
| `removed` | Diff removals |
| `fold_indicator` | Character repeated to form the fold splitter row (e.g. `⁝`) |

Switch themes at runtime with `Ctrl+P`.
