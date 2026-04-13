# AlayaCore

A fast, minimal AI coding agent that runs in your terminal.

AlayaCore connects to any OpenAI-compatible or Anthropic-compatible LLM and gives it the tools to read, write, and edit files, execute shell commands, and activate skills — all from an interactive TUI with streaming output, session persistence, and multi-step agentic tool-calling loops.

## What It Does

You give AlayaCore a task in natural language. It calls an LLM, which reasons about the task and invokes tools — reading files to understand context, editing them to make changes, running shell commands to verify results — in an autonomous loop until the task is done. You watch the work happen in real time and can intervene at any point.

Built with [Bubble Tea](https://charm.land/) for a responsive terminal UI with vim-like keybindings, virtual scrolling, and a windowed display for concurrent streams.

## Quick Start

**Install:**

```sh
go install github.com/alayacore/alayacore@latest
```

**Run:**

```sh
alayacore
```

On first run, AlayaCore auto-creates a default model config at `~/.alayacore/model.conf` configured for Ollama. Edit it to point at your preferred provider — press `Ctrl+L` then `e` in the terminal, or edit the file directly.

**Use:**

```
> read all the .go files in this project and explain the architecture
```

The agent reads files, reasons about them, calls more tools if needed, and streams the answer back.

## Features

- **Agentic tool-calling loop** — The LLM autonomously calls tools across multiple steps (read files → reason → edit files → run tests) until the task is complete. Configurable max-step limit (default: 100).
- **Five built-in tools** — `read_file`, `edit_file`, `write_file`, `shell`, `activate_skill`. Type-safe implementations with auto-generated JSON schemas.
- **Multi-provider support** — Works with any OpenAI-compatible API (OpenAI, DeepSeek, Qwen, LM Studio) and any Anthropic-compatible API (Anthropic, Ollama). Multiple models can coexist in one config file.
- **Interactive TUI** — Bubble Tea terminal UI with vim-like keybindings, virtual scrolling for large outputs, foldable windows, model selector, theme selector, and a task queue manager.
- **Streaming output** — Real-time streaming with styled text, reasoning blocks, and tool-call indicators. No waiting for complete responses.
- **Session persistence** — Save and load conversations. Auto-save support. Sessions use a TLV-encoded binary format with YAML frontmatter.
- **Skills system** — Compatible with the [Agent Skills](https://agentskills.io) specification. Skills are packages of instructions and scripts that extend the agent's capabilities.
- **Plain IO mode** — `--plainio` flag for scripting and piping. No TUI, just stdin/stdout.
- **Auto-summarization** — Automatically summarizes conversation history when context grows too large.
- **Proxy support** — HTTP, HTTPS, and SOCKS5 proxies.
- **Customizable themes** — Color themes with live switching. Ships with dark and light defaults.

## Architecture

AlayaCore is built in four layers connected by a lightweight TLV (Tag-Length-Value) binary protocol:

```
┌─────────────────────────────────────────────────┐
│  Adaptors   │  Terminal (TUI)  │  PlainIO       │  ← User interaction
├─────────────┼──────────────────┼────────────────┤
│             │   TLV Protocol   │                │  ← Inter-layer messages
├─────────────╨──────────────────╨────────────────┤
│  Session    │  Task Queue · Model Manager        │  ← Conversation state
├──────────────────────────────────────────────────┤
│  Agent      │  Provider Interface · Tool Loop    │  ← LLM interaction
├──────────────────────────────────────────────────┤
│  Tools      │  read_file · edit_file · write_file│  ← File & shell operations
│             │  shell · activate_skill            │
└──────────────────────────────────────────────────┘
```

The TLV protocol cleanly separates the UI layer from the session/agent layer. Adaptors emit user input as TLV messages; the session processes them, runs the agent loop, and emits output TLV messages back. This means the terminal TUI and the plain-IO mode share all the same session and agent logic.

```
User types "refactor the config parser"
  → Terminal adaptor emits TLV(UserPrompt)
  → Session submits to task queue
  → Agent loop: LLM → tool_call → execute → tool_result → LLM → ...
  → Session emits TLV(Text), TLV(ToolCall), TLV(ToolResult)
  → Terminal renders windows with streaming output
```

## CLI Flags

```
Usage: alayacore [flags]

Flags:
  --model-config string    Model config file (default: ~/.alayacore/model.conf)
  --runtime-config string  Runtime config file (default: ~/.alayacore/runtime.conf)
  --system string          Extra system prompt (repeatable)
  --skill strings          Skill directory path (repeatable)
  --session string         Session file to load/save
  --proxy string           HTTP/SOCKS5 proxy URL
  --themes string          Themes directory (default: ~/.alayacore/themes)
  --max-steps int          Max agent loop steps (default: 100)
  --auto-summarize         Auto-summarize when context exceeds 80%
  --auto-save              Auto-save session after each response (default: enabled)
  --plainio                Plain stdin/stdout mode (no TUI)
  --debug-api              Log raw API requests/responses
  --version                Show version
  --help                   Show help
```

## Configuration

### Model Config (`~/.alayacore/model.conf`)

Define one or more models. The first model is active on startup. Separate models with `---`:

```
name: "Ollama / Qwen3 30B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "qwen3:30b-a3b"
context_limit: 128000
---
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "sk-..."
model_name: "gpt-4o"
context_limit: 128000
```

**Fields:**

| Field | Description |
|-------|-------------|
| `name` | Display name for the model |
| `protocol_type` | `openai` or `anthropic` |
| `base_url` | API server URL |
| `api_key` | Your API key |
| `model_name` | Model identifier |
| `context_limit` | Max context length in tokens (optional, 0 = unlimited) |
| `prompt_cache` | Enable Anthropic prompt caching (optional) |

Switch models at runtime with `Ctrl+L`.

### Runtime Config (`~/.alayacore/runtime.conf`)

Auto-managed. Persists your active model and theme selections across sessions.

### Themes (`~/.alayacore/themes/`)

Color theme files with values like `primary: #89d4fa`, `text: #cdd6f4`, etc. Ships with dark and light defaults. Switch at runtime with `Ctrl+P`.

## Terminal UI

### Keybindings

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input |
| `Enter` | Submit prompt |
| `j` / `k` | Navigate between windows |
| `J` / `K` | Scroll up/down one line |
| `g` / `G` | Go to first/last window |
| `Space` | Toggle window fold (expand/collapse) |
| `:` | Enter command mode |
| `Ctrl+S` | Save session |
| `Ctrl+O` | Open external editor for multi-line input |
| `Ctrl+L` | Model selector |
| `Ctrl+P` | Theme selector |
| `Ctrl+Q` | Task queue manager |
| `Ctrl+G` | Cancel current request |
| `Ctrl+C` | Clear input |

### Commands

| Command | Action |
|---------|--------|
| `:save [file]` | Save session |
| `:cancel` | Cancel current request |
| `:cancel_all` | Cancel request and clear queue |
| `:retry` | Retry the last prompt |
| `:summarize` | Summarize conversation to reduce tokens |
| `:model_set <id>` | Switch model |
| `:model_load` | Reload model configs from file |
| `:quit` | Exit |

## Skills

Skills are packages of instructions and optional scripts that extend the agent's capabilities. They follow the [Agent Skills](https://agentskills.io) specification.

```sh
alayacore --skill ~/skills/weather
```

A skill directory contains:

```
weather/
├── SKILL.md          # Instructions + metadata (required)
└── scripts/          # Executable scripts (optional)
```

Skills are discovered at startup and injected into the system prompt. The LLM activates them on demand using the `activate_skill` tool.

## Examples

```sh
# Interactive coding session with a local model
alayacore --model-config ~/.alayacore/ollama.conf

# Pipe a question and get an answer
echo "explain the error handling in this project" | alayacore --plainio

# Session with persistence and a remote model
alayacore --session ~/sessions/refactor.md --model-config ~/.alayacore/openai.conf

# With skills and proxy
alayacore --skill ~/skills --proxy socks5://127.0.0.1:1080

# Headless: pipe a prompt, get the result
echo "list all TODO comments in the codebase" | alayacore --plainio
```

## Development

```sh
make build          # Build binary
make test           # Run tests
make lint           # Run golangci-lint
make check          # fmt + vet + lint + test
make run            # Build and run
make help           # Show all make targets
```

## Documentation

### User Docs

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | Installation, CLI flags, and usage examples |
| [Configuration](docs/configuration.md) | Model config, runtime config, and themes |
| [Terminal UI](docs/terminal-ui.md) | Keybindings, commands, windows, task queue, plain IO mode |
| [Skills System](docs/skills.md) | Agent Skills specification, directory structure, SKILL.md format |

### Architecture Docs

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | Layered architecture, TLV protocol, data flow, system prompt, design decisions |
| [Sequential Tool Execution](docs/sequential-tool-execution.md) | Why tools execute one at a time |
| [Error Handling](docs/error-handling.md) | How LLM API errors are detected and propagated |
| [Context Token Tracking](docs/context-tracking.md) | How context size is tracked across providers |
| [Virtual Rendering Performance](docs/virtual-rendering-performance.md) | Performance analysis of the virtual scrolling system |
| [External Editor & Window Size](docs/external-editor-windowsize.md) | How Bubble Tea handles resize after external editor |
| [Schema Improvements](docs/schema-improvements.md) | Type-safe tools with auto-generated JSON schemas |

## License

MIT
