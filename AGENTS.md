# AlayaCore

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.


## Project
- Module: `github.com/alayacore/alayacore`
- Binary: `alayacore`
- Dependencies:
  - `charm.land/fantasy` - Agent framework
  - `charm.land/bubbletea/v2` - Terminal UI framework v2
  - `charm.land/lipgloss/v2` - Styling framework v2
  - `charm.land/bubbles/v2` - Bubble Tea components v2
  - `github.com/gorilla/websocket` - WebSocket server
  - `mvdan.cc/sh/v3` - Shell interpreter


## Installation

```sh
go install github.com/alayacore/alayacore@latest
go install github.com/alayacore/alayacore/cmd/alayacore-web@latest
```

Or build from source:

```sh
git clone https://github.com/alayacore/alayacore.git
cd alayacore
go build
go build ./cmd/alayacore-web/
```


## Usage

Create a model config file at `~/.alayacore/model.conf`:

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

The program will load models from the config file and use the last model as default.

Running with skills:
```sh
alayacore --skill ~/playground/alayacore/misc/samples/skills/
```

See [CLI Reference](docs/cli-reference.md) for all flags and usage examples.


## Tools

AlayaCore provides the following tools (ordered from safest to most dangerous):

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file. Supports optional line range. |
| `edit_file` | Apply search/replace edit to a file |
| `write_file` | Create a new file or replace entire file content |
| `activate_skill` | Load and execute a skill |
| `posix_shell` | Execute shell commands |


## Documentation

- [CLI Reference](docs/cli-reference.md) - All CLI flags and usage examples
- [Session Persistence](docs/sessions.md) - Save and restore conversations
- [Terminal Controls](docs/terminal-controls.md) - Keyboard shortcuts and navigation
- [Window Container](docs/window-container.md) - UI organization and cursor behavior
- [Skills System](docs/skills.md) - Agent Skills specification and usage
- [Web Server](docs/web-server.md) - WebSocket server with built-in chat UI


## Agent Instructions
- **Do NOT commit automatically** - wait for explicit user command
- **Read STATE.md** at the start of every conversation
- **Update STATE.md** after completing any meaningful work (features, bug fixes, etc.)
- **Keep AGENTS.md and README.md in sync** - update both files together before commits
- Keep STATE.md as the single source of truth for project status

### Tool Ordering

Tools must be ordered from safest to most dangerous:

1. `read_file` - Read file contents
2. `edit_file` - Apply search/replace edit to a file
3. `write_file` - Create or replace files
4. `activate_skill` - Load and execute skills
5. `posix_shell` - Execute shell commands (most dangerous)
