# AlayaCore

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

AlayaCore supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.


## Project
- Module: `github.com/wallacegibbon/alayacore`
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
go install github.com/wallacegibbon/alayacore@latest
go install github.com/wallacegibbon/alayacore/cmd/alayacore-web@latest
```

Or build from source:

```sh
git clone https://github.com/wallacegibbon/alayacore.git
cd alayacore
go build
go build ./cmd/alayacore-web/
```


## Quick Start

```sh
# OpenAI-compatible server
alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Anthropic-compatible server
alayacore --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4
```

See [CLI Reference](docs/cli-reference.md) for all flags and usage examples.


## Tools

AlayaCore provides the following tools (ordered from safest to most dangerous):

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file. Supports optional line range. |
| `todo_read` | Read the current todo list |
| `todo_write` | Write or update the todo list |
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
2. `todo_read` - Read todo list
3. `todo_write` - Write/update todo list
4. `edit_file` - Apply search/replace edit to a file
5. `write_file` - Create or replace files
6. `activate_skill` - Load and execute skills
7. `posix_shell` - Execute shell commands (most dangerous)
