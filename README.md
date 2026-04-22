# AlayaCore

A fast, minimal AI coding agent that runs in your terminal.

AlayaCore connects to any OpenAI-compatible or Anthropic-compatible LLM and gives it the tools to read, write, and edit files, execute commands, and activate skills — all from an interactive TUI with streaming output, session persistence, and multi-step agentic tool-calling loops.

You give AlayaCore a task in natural language. It calls an LLM, which reasons about the task and invokes tools — reading files to understand context, editing them to make changes, running commands to verify results — in an autonomous loop until the task is done. You watch the work happen in real time and can intervene at any point.

Built with [Bubble Tea](https://charm.land/) for a responsive terminal UI with vim-like keybindings, virtual scrolling, and a windowed display for concurrent streams.

## Quick Start

```sh
go install github.com/alayacore/alayacore@latest
alayacore
```

On first run, AlayaCore auto-creates a default model config at `~/.alayacore/model.conf` configured for Ollama. Edit it to point at your preferred provider — press `Ctrl+L` then `e` in the terminal, or edit the file directly.

## Features

- **Autonomous tool-calling loop** — The LLM plans, calls tools, and iterates until the task is done. Up to 100 steps per prompt.
- **Five built-in tools** — `read_file`, `edit_file`, `write_file`, `execute_command`, `search_content`.
- **Cross-platform** — Runs on Linux, macOS, and Windows. The `execute_command` tool auto-detects the shell (bash/zsh/sh on Unix, PowerShell/cmd on Windows).
- **Any LLM provider** — OpenAI, Anthropic, DeepSeek, Qwen, Ollama, LM Studio. Multiple models in one config, switch at runtime.
- **Streaming TUI** — Real-time output with virtual scrolling, foldable windows, and vim-like keybindings.
- **Plain IO mode** — `--plainio` for scripting and piping. No TUI, just stdin/stdout.
- **Session persistence** — Save and resume conversations with auto-save.
- **Skills system** — Extend the agent with instruction packages following the [Agent Skills](https://agentskills.io) spec.
- **Themes** — Customizable color schemes with live switching.

## Documentation

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | Installation, CLI flags, and usage examples |
| [Configuration](docs/configuration.md) | Model config, runtime config, and themes |
| [Terminal UI](docs/terminal-ui.md) | Keybindings, commands, windows, task queue, plain IO mode |
| [Skills System](docs/skills.md) | Agent Skills specification, directory structure, SKILL.md format |
| [Architecture](docs/architecture.md) | Layered architecture, TLV protocol, data flow, design decisions |

## License

MIT
