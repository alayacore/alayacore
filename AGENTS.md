# CoreClaw

A minimal AI Agent that can handle toolcalling, powered by Large Language Models. It provides multiple tools for file operations and shell execution.

CoreClaw supports all OpenAI-compatible or Anthropic-compatible API servers.

For this project, simplicity is more important than efficiency.


## Project
- Module: `github.com/wallacegibbon/coreclaw`
- Binary: `coreclaw`
- Dependencies:
  - `charm.land/fantasy` - Agent framework
  - `charm.land/bubbletea/v2` - Terminal UI framework v2
  - `charm.land/lipgloss/v2` - Styling framework v2
  - `charm.land/bubbles/v2` - Bubble Tea components v2
  - `github.com/gorilla/websocket` - WebSocket server
  - `mvdan.cc/sh/v3` - Shell interpreter


## Installation

```sh
go install github.com/wallacegibbon/coreclaw@latest
go install github.com/wallacegibbon/coreclaw/cmd/coreclaw-web@latest
```

Or build from source:

```sh
git clone https://github.com/wallacegibbon/coreclaw.git
cd coreclaw
go build
go build ./cmd/coreclaw-web/
```

## Usage

All configuration must be specified via command line flags:

```sh
# Local Ollama OpenAI-compatible server
coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3

# Local Ollama Anthropic-compatible server
coreclaw --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b

# MiniMax (Anthropic-compatible)
coreclaw --type anthropic --base-url $MINIMAXI_API_URL --api-key $MINIMAXI_API_KEY --model MiniMax-M2.5

# DeepSeek (OpenAI-compatible)
coreclaw --type openai --base-url $DEEPSEEK_API_URL --api-key $DEEPSEEK_API_KEY --model deepseek-chat

# ZAI (OpenAI-compatible)
coreclaw --type openai --base-url $ZAI_API_URL --api-key $ZAI_API_KEY --model GLM-4.7
```

Running with skills
```sh
coreclaw --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b --skill ~/playground/coreclaw/misc/samples/skills/
```


## CLI Flags

- `-type string` - Provider type: `anthropic` or `openai` (required)
- `-base-url string` - API endpoint URL (required)
- `-api-key string` - API key (required)
- `-model string` - Model name to use
- `-version` - Show version information
- `-help` - Show help information
- `-debug-api` - Write raw API requests and responses to log file
- `-system string` - Override system prompt
- `-skill string` - Skills directory path (can be specified multiple times)
- `-session string` - Session file path to load/save conversations


## Session Persistence

CoreClaw allows you to save and restore conversations manually using session files.

### Behavior

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Load**: On startup, CoreClaw creates a new empty session unless you specify `--session` to load an existing one
- **Path expansion**: Paths like `~/mysession.json` are expanded to your home directory

### What's Saved

- Message history (user prompts and assistant responses)
- Token usage statistics (total and context)
- Provider configuration (BaseURL, ModelName)

### What's Not Saved

- Reasoning/thinking content (streamed output not stored)
- Tool call details (only message history is preserved)

### Session Commands

```sh
:save                    # Save to current session file (if set with --session)
:save ~/mysession.json    # Save to specific file
:cancel                  # Cancel current request and clear todos (with confirmation)
:summarize              # Summarize the entire conversation to a single message
```

### Loading Sessions

You can load an existing session using the `--session` flag:

```sh
# Load a specific session file
coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --session ~/mysession.json

# Start fresh with a new session (default behavior)
coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o
```


## Web Server

`coreclaw-web` runs a WebSocket server with a built-in chat UI.

```sh
# Start WebSocket server
coreclaw-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Custom address
coreclaw-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4 --addr :9090
```

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`
- Each browser tab gets its own independent agent session

## Tools

CoreClaw provides the following tools (ordered from safest to most dangerous):

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file. Supports optional line range using start_line and end_line parameters (1-indexed). |
| `todo_read` | Read the current todo list |
| `todo_write` | Write or update the todo list |
| `edit_file` | Apply changes to a file using unified diff format. Requires reading file first to generate accurate diffs with matching context lines. |
| `write_file` | Create a new file or replace entire file content |
| `activate_skill` | Load and execute a skill |
| `posix_shell` | Execute shell commands |


## Terminal Controls

When running the Terminal version:

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `g` | Go to top of display (when display focused) |
| `G` | Go to bottom of display (when display focused) |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle wrap mode for active window (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |
| `:cancel` | Cancel current request (with confirmation) |
| `:quit`, `:exit` | Exit with confirmation (press y/n) |
## Window Container

CoreClaw's terminal adaptor organizes concurrent streams into separate windows with synchronized widths. Each stream (reasoning, text, tool outputs) appears in its own window with dimmed borders.

- **Stream ID suffix**: To prevent collisions across conversation turns, stream IDs include a monotonic suffix (e.g., `0-1`, `1-1` for first turn; `0-2`, `1-2` for second turn). This ensures each turn's content appears in distinct windows while keeping related deltas grouped within a turn.
- **Width synchronization**: All windows match the input box width for consistent layout.
- **Delta routing**: Content with stream ID prefix `[:id:]` is routed to the appropriate window via `parseStreamID()`.

### Window Cursor

The Window Cursor highlights one window in the display area with a brighter border. Use `j` and `k` keys to navigate between windows.

- **Default position**: The cursor defaults to the last window and updates automatically when new windows are created.
- **Scroll synchronization**: When using `g` or `G`, the cursor follows the scroll movement to remain visible.
- **Highlighted border**: The selected window displays a bright blue border (`#89b4fa`) instead of the dimmed default (`#6c7086`).
- **Wrap mode**: Press `Space` to toggle the active window between normal and wrap mode. In wrap mode, the window shows only the last 3 lines of content, displaying the newest content. Wrapped windows display a `Wrapped - Space to expand` indicator with a subtle background color.

## Skills System

CoreClaw supports the Agent Skills specification from [agentskills.io](https://agentskills.io). Skills are packages of instructions, scripts, and resources that agents can discover and use to perform specific tasks.

### Usage

```sh
# With skills directory
coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --skill ./skills "extract text from document.pdf"
```

### Skill Directory Structure

```
skills/
├── SKILL.md          # Required: instructions + metadata
├── scripts/          # Optional: executable code
├── references/      # Optional: documentation
└── assets/          # Optional: templates, resources
```

### SKILL.md Format

Skills use YAML frontmatter followed by Markdown content:

```yaml
---
name: pdf-processing
description: Use this skill whenever the user wants to do anything with PDF files. This includes reading or extracting text/tables from PDFs, combining or merging multiple PDFs into one, splitting PDFs apart, rotating pages, adding watermarks, creating new PDFs, filling PDF forms, encrypting/decrypting PDFs, extracting images, and OCR on scanned PDFs to make them searchable.
license: Apache-2.0
---

# PDF Processing Skill

Instructions for the agent...
```

### How Skills Work

1. **Discovery**: At startup, CoreClaw scans the skills directory and loads only skill names and descriptions
2. **Activation**: When a task matches a skill's description, the agent can activate it to load full instructions
3. **Execution**: The agent follows the instructions, optionally running bundled scripts

Skills metadata is injected into the system prompt using XML format:

```xml
<available_skills>
  <skill>
    <name>pdf-processing</name>
    <description>Extract text and tables from PDF files...</description>
    <location>/path/to/skills/pdf/SKILL.md</location>
  </skill>
</available_skills>
```

### Skill Specification

- **name**: 1-64 characters, lowercase letters, numbers, and hyphens only
- **description**: 1-1024 characters, describes what the skill does AND when to use it
- **license**: Optional, license name or reference
- **compatibility**: Optional, environment requirements
- **allowed-tools**: Optional, space-delimited list of pre-approved tools


## Agent Instructions
- **Do NOT commit automatically** - wait for explicit user command
- **Read STATE.md** at the start of every conversation
- **Update STATE.md** after completing any meaningful work (features, bug fixes, etc.)
- **Keep AGENTS.md and README.md in sync** - update both files together before commits
- Keep STATE.md as the single source of truth for project status

### Tool Ordering

Tools must be ordered from safest to most dangerous:

1. `read_file` - Read file contents (supports line range with start_line and end_line parameters, 1-indexed)
2. `todo_read` - Read todo list
3. `todo_write` - Write/update todo list
4. `edit_file` - Apply changes to a file using unified diff format
5. `write_file` - Create or replace files (full overwrite)
6. `activate_skill` - Load and execute skills
7. `posix_shell` - Execute shell commands (most dangerous)
