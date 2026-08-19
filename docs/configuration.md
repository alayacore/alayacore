# Configuration

AlayaCore has three configuration files: model config, runtime config, and theme files. All live under a single config directory — `~/.alayacore/` by default — making it easy to manage, back up, or share your entire configuration.

```
~/.alayacore/
├── model.conf        # LLM provider and model definitions
├── runtime.conf      # Active model/theme selections (auto-managed)
└── themes/
    ├── theme-dark.conf
    └── theme-light.conf
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
reasoning_0: {"thinking":{"type":"disabled"}}
reasoning_1: {"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}
reasoning_2: {"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}
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
| `reasoning_0` | No | Raw provider-level JSON merged into the request body when reasoning level is **0** (off). Top-level keys must match the provider's wire format. Omitted (or empty) → no reasoning-related fields are sent for that level. |
| `reasoning_1` | No | Same as `reasoning_0` but for reasoning level **1** (normal). |
| `reasoning_2` | No | Same as `reasoning_0` but for reasoning level **2** (max). |

### Reasoning configuration (`reasoning_0` / `reasoning_1` / `reasoning_2`)

These three fields let the model decide exactly what thinking/reasoning
wire fields the provider sends for each reasoning level. Their values are
**raw provider wire-format JSON** — top-level keys become top-level keys
of the request body. The provider merges them in verbatim, so any field
the provider accepts can be expressed here without alayacore code
changes. Common shapes:

| Provider family | `reasoning_1` example | `reasoning_2` example |
|-----------------|-----------------------|-----------------------|
| Anthropic | `{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}` | `{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}` |
| OpenAI / DeepSeek | `{"thinking":{"type":"enabled"},"reasoning_effort":"high"}` | `{"thinking":{"type":"enabled"},"reasoning_effort":"xhigh"}` |
| Any OpenAI-compatible | the keys documented by that provider | the keys documented by that provider |

If all three `reasoning_*` fields are omitted, **no thinking-related
fields appear in the request body** — the server falls back to its own
defaults. This makes the fields purely additive: existing model.conf
files without them keep working exactly as before.

Reasoning level is independent of provider configuration. The level
(`0`/`1`/`2`) is controlled by `:reason <level>` at runtime or
`--reasoning-level <level>` at startup, and is also persisted in the
session file. Switching levels picks a different `reasoning_*` block at
request time.

### Multiple Models

Separate models with `---`. The first model becomes active on startup (unless `runtime.conf` has a saved preference):

```
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "sk-..."
model_name: "gpt-4o"
context_limit: 128000
reasoning_1: {"thinking":{"type":"enabled"},"reasoning_effort":"high"}
reasoning_2: {"thinking":{"type":"enabled"},"reasoning_effort":"xhigh"}
---
name: "Anthropic Claude Sonnet"
protocol_type: "anthropic"
base_url: "https://api.anthropic.com"
api_key: "sk-ant-..."
model_name: "claude-sonnet-4-20250514"
context_limit: 200000
reasoning_1: {"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}
reasoning_2: {"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}
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

Comments (`#`) are **line-level**: a comment line may appear anywhere inside a model block — including the first line — and the rest of the block is still parsed. A block containing only comments or blank lines is skipped entirely.

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

Themes are `.conf` files that define the TUI color scheme. If the themes directory doesn't exist, AlayaCore creates it with two defaults: `theme-dark.conf` (Catppuccin Mocha, the default) and `theme-light.conf` (Catppuccin Latte).

### Theme File Format

```
# ~/.alayacore/themes/theme-dark.conf
primary: #89d4fa
dim: #313244
muted: #6c7086
warning: #f77923
error: #f38ba8
selection: #fab387
added: #a6e3a1
removed: #f38ba8
tool: #f9e2af
fold_arrow: "▶"
unfold_arrow: "▼"
```

### Color Roles

| Color | Used for |
|-------|----------|
| `primary` | User input text, prompt display, emphasis, focused box rules, running status dot (status bar only — tool windows use the colorless spinner while running) |
| `dim` | Window rules, separators, status bar |
| `muted` | Secondary text, system messages, tool content |
| `warning` | Confirm dialogs, multi-line prompt hints, attachment labels |
| `error` | Errors |
| `selection` | Selected items in lists, cursor arrow highlight |
| `tool` | Tool call headers/labels |
| `added` | Diff additions |
| `removed` | Diff removals |

Body text (assistant messages, reasoning, user input) is rendered without an explicit foreground color — it uses the terminal's default. Selected/active items in overlay lists are emphasized with **bold** weight only, not color. The terminal cursor uses the emulator's default color (the theme does not control it).

### Glyphs

| Key | Used for |
|-----|----------|
| `fold_arrow` | Arrow prefix on collapsed (folded) window header lines. Single codepoint |
| `unfold_arrow` | Arrow prefix on expanded window header lines. Single codepoint |

Switch themes at runtime with `Ctrl+P`.
