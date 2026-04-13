# Terminal UI

AlayaCore's terminal UI is built with [Bubble Tea](https://charm.land/) and uses vim-like keybindings throughout.

## Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down |
| `k` | Move window cursor up |
| `J` | Scroll down one line |
| `K` | Scroll up one line |
| `g` | Go to first window, scroll to top |
| `G` | Go to last window, scroll to bottom |
| `H` | Move cursor to top window in visible area |
| `M` | Move cursor to middle window in visible area |
| `L` | Move cursor to bottom window in visible area |

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt |
| `Ctrl+S` | Save session |
| `Ctrl+O` | Open external editor (`$EDITOR`) for multi-line input |
| `Ctrl+L` | Open model selector |
| `Ctrl+P` | Open theme selector |
| `Ctrl+Q` | Open task queue manager |
| `Ctrl+G` | Cancel current request (with confirmation) |
| `Ctrl+C` | Clear input |
| `:` | Switch to input with `:` prefix (command mode) |
| `Space` | Toggle window fold (expand/collapse) |

## Session Commands

| Command | Action |
|---------|--------|
| `:save [filename]` | Save session. Uses `--session` path if no filename given. |
| `:cancel` | Cancel current request (with confirmation) |
| `:cancel_all` | Cancel current request and clear the task queue |
| `:retry` | Retry the last prompt. Re-sends history; appends "Please continue." if latest message is from the assistant. |
| `:summarize` | Summarize conversation to reduce token usage |
| `:model_set <id>` | Switch to a model by name |
| `:model_load` | Reload model configs from the config file |
| `:quit`, `:q` | Exit with confirmation (y/n) |

## Window Container

The display area organizes content into separate windows — one per message or tool call. Windows have synchronized widths and can be navigated independently.

### Auto-Follow

When new windows appear, the cursor automatically moves to the newest one. Pressing `k`, `g`, `H`, `L`, `M`, `J`, or `K` disables auto-follow. Returning to the last window re-enables it.

### Fold Mode

Press `Space` on any window to collapse it — the window shows the first line, a fold indicator, and the last 3 lines. Press `Space` again to expand.

### Virtual Scrolling

The display uses virtual scrolling to handle large outputs efficiently. Only visible windows are rendered, giving a 3.5x speedup over naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md) for details.

## Task Queue Manager

When you submit prompts or commands while a previous task is running, they are queued. Press `Ctrl+Q` to manage the queue:

| Key | Action |
|-----|--------|
| `q`, `Esc` | Close queue manager |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `d` | Delete selected task |

Each queued task shows its queue ID (Q1, Q2, …), type (`P` for prompt, `C` for command), and a truncated content preview.

## Session Persistence

- **Auto-save** — Enabled by default when `--session` is specified. The session is saved after each task completes.
- **Manual save** — `:save [file]` or `Ctrl+S` at any time.
- **Load** — On startup, AlayaCore starts a new empty session unless you specify `--session` to load an existing one.
- **Auto-summarize** — When `--auto-summarize` is enabled and `context_limit` is set, AlayaCore automatically triggers `:summarize` when context reaches 80% of the limit.

Session files use TLV-encoded binary format with YAML frontmatter. See [architecture.md](architecture.md) for format details.

## Plain IO Mode

`--plainio` runs AlayaCore as a plain stdin/stdout process with no terminal UI. Useful for scripting, piping, and headless environments.

### Input

- Each line from stdin is a separate prompt
- A trailing backslash (`\`) continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin, waits for queued tasks to finish, exits with code `0`
- **Ctrl-C** (SIGINT): sends `:cancel_all`, exits with code `1`

### Output

All output is plain text with no ANSI escape codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `> prompt` |
| Tool calls | `[tool_name: args]` |
| Tool results | Suppressed |
| Errors | `Error: message` |
| Notifications | `[message]` |

A blank line separates messages of different types.

### Examples

```sh
# Pipe a single question
echo "what is 2+2?" | alayacore --plainio

# Interactive plain session
alayacore --plainio
> read the Makefile and list the build targets
> now explain the architecture
> :quit

# Use in scripts
alayacore --plainio < questions.txt > answers.txt
```
