# Getting Started

## Installation

```sh
go install github.com/alayacore/alayacore@latest
```

Or build from source:

```sh
git clone https://github.com/alayacore/alayacore.git
cd alayacore
make install
```

## Quick Start

```sh
alayacore
```

On first run, AlayaCore auto-creates a default model config at `~/.alayacore/model.conf` configured for Ollama:

```
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
```

To use other providers, edit the config file — press `Ctrl+L` then `e` in the terminal, or edit it directly. See [configuration.md](configuration.md) for the full format.

## First Steps

1. **Start a conversation** — Type a prompt and press `Enter`. The agent will stream a response.
2. **Give it a task** — Try `"read main.go and explain what this project does"`. The agent will use the `read_file` tool (or `ripgrep` to find content first), then answer.
3. **Switch models** — Press `Ctrl+L` to open the model selector. Press `e` to edit your config, `r` to reload, `Enter` to select.
4. **Save your session** — Type `:save my-session.md` or press `Ctrl+S`.

## Cross-Platform Support

AlayaCore runs on Linux, macOS, and Windows. The `execute_command` tool automatically detects the best available shell on startup:

| OS | Detection order |
|----|----------------|
| **Linux / macOS** | bash → zsh → sh |
| **Windows** | pwsh → powershell → cmd |

The tool description is dynamically adapted so the LLM knows which shell syntax to use. You can override the detection with the `ALAYACORE_SHELL` environment variable:

```sh
# Force PowerShell Core (must be a known shell name)
export ALAYACORE_SHELL=pwsh

# Use zsh
export ALAYACORE_SHELL=zsh
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--model-config` | `~/.alayacore/model.conf` | Path to model configuration file |
| `--runtime-config` | `~/.alayacore/runtime.conf` | Path to runtime configuration file |
| `--system` | *(none)* | Extra system prompt text. Repeatable: `--system "rule 1" --system "rule 2"` |
| `--skill` | *(none)* | Path to a skill directory. Repeatable: `--skill ./skills1 --skill ./skills2` |
| `--session` | *(none)* | Path to session file for loading/saving conversations |
| `--proxy` | *(none)* | Proxy URL. Supports `http://`, `https://`, and `socks5://` schemes |
| `--themes` | `~/.alayacore/themes/` | Path to themes directory |
| `--max-steps` | `100` | Maximum number of agent loop iterations per prompt |
| `--auto-summarize` | `false` | Automatically summarize when context exceeds 80% of `context_limit` |
| `--auto-save` | `true` | Auto-save session after each response (requires `--session`) |
| `--plainio` | `false` | Plain stdin/stdout mode — no TUI, for scripting and piping |
| `--debug-api` | `false` | Write raw API requests and responses to a log file |
| `--version` | — | Print version and exit |
| `--help` | — | Print help and exit |

## Examples

```sh
# Interactive session with default config
alayacore

# Custom model config
alayacore --model-config ./my-model.conf

# Session persistence
alayacore --session ~/sessions/refactor.md

# Multiple skill directories
alayacore --skill ./skills/weather --skill ./skills/pdf

# Behind a proxy
alayacore --proxy http://127.0.0.1:7890

# Plain IO — pipe a question, get an answer
echo "what is 2+2?" | alayacore --plainio

# Multi-turn plain IO session
alayacore --plainio
> read the Makefile and explain the build targets
> now add a target for cross-compiling to Windows
> :quit
```

## Next Steps

- **[Configuration](configuration.md)** — Set up multiple models, API keys, and themes
- **[Terminal UI](terminal-ui.md)** — Learn the keybindings and commands
- **[Skills System](skills.md)** — Extend the agent with custom skill packages
