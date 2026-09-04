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
> folders are project-specific and rarely live inside the config directory.
> Each `--skill` value is a **container**: all of its immediate subdirectories
> holding a `SKILL.md` are loaded, so one flag is normally enough. Repeat it to
> add further containers (e.g. project plus personal) — the first one listed
> wins a name collision. Relative and `~` paths are resolved at startup.
> Pointing it at a single skill's own directory loads nothing. See
> [skills.md](skills.md#what-discovery-guarantees).

## Model Config

**Location**: `<config-path>/model.conf`

This file defines one or more LLM models that AlayaCore can use. It is auto-created with a default Ollama configuration on first run. Models replaced via `:model_sync` are persisted back to this file automatically.

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
| `reasoning_field` | No | **OpenAI-protocol only.** Which key this endpoint uses for reasoning text — read from responses and used again when replaying reasoning. Omitted (or empty) → `reasoning_content`. See below. |
| `serial_tool_calls` | No | `false` (default) or `true`. When `true`, alayacore runs a step's tool calls one at a time in the order the model made them, and the Chat Completions request asks for the same. See below. |

### Tool-calling mode (`serial_tool_calls`)

Many models and servers have no notion of parallel tool calls. This option is
for them. It decides two things at once:

| | `false` — omitted (default) | `true` |
|---|---|---|
| **Execution** | Each call starts as soon as its arguments finish streaming, so several run at once | One at a time, in the order the model made them |
| **Chat Completions request** | `"parallel_tool_calls": true` | `"parallel_tool_calls": false` |

**Why the name is negative.** Omitting the line must mean what alayacore has
always done, and in this format an omitted key is a `false`. So the option is
spelled for the new behavior, not the old one: `serial_tool_calls: true` is the
thing you are opting into. The positive spelling (`parallel_tool_calls: false`)
would have described the same setting but could not have been defaulted — an
absent line would have read as `false`, and every existing `model.conf` would
have silently switched modes.

The request field keeps Chat Completions' own name, which is positive, so the
request body says the opposite word from the config line. That inversion happens
exactly once, in the OpenAI provider, and nowhere else.

The request field is sent on **every** request that carries tools — never
omitted. What default a given endpoint would have applied is not something
alayacore can observe — an unstated field and an explicit `true` arrive as the
same request from here — so relying on it would let a server-side setting nobody
can read decide which mode this client is in. Stating it makes the request say
what the client is doing.

The field exists only in OpenAI Chat Completions. `protocol_type: "anthropic"`
endpoints have no equivalent, and nothing is invented onto that wire — but the
**execution order still applies**, since that part lives in the agent rather than
in the protocol. Setting `serial_tool_calls: true` on an Anthropic model is
therefore meaningful, not a no-op.

Ordering is by the index the model declared, not by the order argument fragments
happened to arrive in. A model that asks for three things gets them run in the
sequence it asked for.

Two consequences worth knowing:

- **A serial turn is slower** when the model asks for several things at once,
  because the calls queue instead of overlapping. The option is a correctness
  setting, not a performance one.
- **Tool confirmations become one-at-a-time by construction.** A confirmation is
  requested when its own turn comes, so a later call will not have its window
  appear before an earlier one is answered.

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

### Reasoning response key (`reasoning_field`)

`reasoning_0/1/2` decide which thinking fields go into the request body; this
decides **which key reasoning text travels under** — in the response alayacore
reads and in the assistant messages it replays. It needs declaring at all
because that key is not standardized either: OpenAI's `ChatCompletionStreamResponseDelta` schema has
only `content`, `role`, `tool_calls` and `function_call`; every server that
ships reasoning invented a key for it:

| Value | Served by |
|---|---|
| `reasoning_content` (default) | DeepSeek (originator), GLM, MiniMax, Qwen/DashScope |
| `reasoning` | vLLM (renamed from `reasoning_content`, old name no longer emitted), OpenRouter |

```yaml
name: "Local LLM / vLLM"
protocol_type: "openai"
base_url: "http://127.0.0.1:8000/v1"
reasoning_field: "reasoning"
```

Notes:

- **The key belongs to the deployment, not the model.** The same deepseek
  weights answer with `reasoning_content` on the DeepSeek API and `reasoning`
  on a self-hosted vLLM, so declare it per entry (each entry already carries
  its own `base_url`).
- **A configured value is used exclusively** — nothing is read from any other
  spelling. A wrong value therefore shows up as *reasoning silently missing*,
  with text streaming normally and no error; check this field first when the
  `REASONING` window never appears.
- **One field covers both directions.** Replayed reasoning in a tool-call
  chain is sent under the same key: unset keeps `reasoning_content` (what the
  DeepSeek family requires), `reasoning_field: "reasoning"` sends `reasoning`
  (vLLM's canonical input field).
- **Non-string values are ignored.** OpenRouter's `reasoning_details` is an
  array of typed blocks; reading those needs type-aware parsing, not a name, so
  pointing this field at it yields no reasoning rather than garbage.
- No `protocol_type: anthropic` equivalent is needed: thinking arrives as a
  `{"type":"thinking"}` block inside `content[]`, which is standard for that API.

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

`reasoning_field` and the contents of `reasoning_0/1/2` are **not** checked against the server. They name wire-format keys, and a name is only right or wrong relative to what a given deployment emits — alayacore has no way to know, and guessing would override a deliberate declaration. A mismatch therefore loads fine and reads as empty reasoning: the `REASONING` window never appears, text streams normally, nothing logs. When reasoning seems broken on an OpenAI-protocol model, compare this field against what the endpoint actually returns.

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

Body text (assistant messages, reasoning, user input, tool input/output) is rendered without an explicit foreground color — it uses the terminal's default. When an overlay (model selector, help window, confirm dialog, …) is open, the body dims to the theme's `dim` color together with the rest of the background content. Selected/active items in overlay lists are emphasized with **bold** weight only, not color. The terminal cursor uses the emulator's default color (the theme does not control it).

### Glyphs

| Key | Used for |
|-----|----------|
| `fold_arrow` | Arrow prefix on collapsed (folded) window header lines. Single codepoint |
| `unfold_arrow` | Arrow prefix on expanded window header lines. Single codepoint |

Switch themes at runtime with `Ctrl+P`.
