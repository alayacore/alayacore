# Plain IO Mode

`--plainio` runs AlayaCore as a plain stdin/stdout process with no terminal UI — no ANSI codes, no TTY detection, so it works on any terminal, including teletype. It is designed for **interactive use without a TUI**: it shows the full conversation (echoed prompts, reasoning, tool calls, results). For scripts and pipes that only need the final answer, use [`--terseio`](terseio.md) instead.

## Input

- Each line from stdin is treated as a separate prompt (one prompt per invocation — see note below)
- A trailing backslash (`\`) continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **`Ctrl-D`** (EOF): closes stdin. After EOF, the program waits for the
  current task to finish, then exits with code `0` — like `:quit`,
  regardless of whether any task errored during the session.
- **`Ctrl-C`** (SIGINT): terminates immediately with default signal handling
  (exit code 130).
- **`:quit` / `:q`**: stops reading input; the program waits for any running
  task, then exits with code `0`.

> Task errors (API failures, max steps, ...) are reported as `[error: ...]`
> and the session **continues** — plainio is interactive, so you can keep
> typing prompts after an error. Errors never terminate the session and
> never affect the exit code. Scripts that need a failure signal on task
> errors should use [`--terseio`](terseio.md).

> **⚠️ One task at a time.** Plain IO processes prompts **one at a time**
> and has no task queue. If you pipe multiple prompts into stdin, only the
> **first** one is executed. Subsequent prompts are rejected with:
> ```
> Error: A task is already running. Wait for it to complete or cancel it.
> ```
> For scripting multiple questions, use `--terseio` (one message per
> invocation) or launch `alayacore --plainio` once per prompt (the process
> spawn cost is negligible).

## Output

All output is plain text with no ANSI escape codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `User: prompt` (own line; blank line after, blank line before when the previous message ended with a newline) |
| Tool calls | Raw JSON (id, name, input) |
| Tool results | Raw JSON (id, output, is_error) |
| Command results | Success: rendered from the structured result (e.g. `Session saved to <path>`); commands whose effect is self-evident (e.g. `:cancel`, `:reason`) are silent; failure: `[error: message]` (does not affect exit code) |
| Errors | `[error: message]` |
| Notifications | `[message]` |
| Tool confirmations | `[tool_confirm: allow tool "id" to run?]` |
| MCP init progress | `[mcp: connecting "server"]`, `[mcp: connected "server"]`, `[mcp: failed "server": error]`, `[mcp: waiting for authorization for "server"…]` |

Respond with `:tool_confirm <id>` to allow or `:tool_decline <id>` to deny.

A blank line separates messages of different types.

### MCP Support

MCP servers work the same as in the TUI: configured servers connect at
startup and their tools (and `tool_confirm` prompts) behave identically.
When a server requires OAuth authorization, plainio prints the
authorization URL, starts a local callback server, and opens the browser
automatically — once you authorize, the code is submitted for you. If
the browser doesn't open, visit the printed URL and type
`:mcp_confirm <server> <code> <redirect_uri>`; use `:mcp_decline
<server>` to skip a server or `:mcp_cancel` to abort MCP init. See
[OAuth](oauth.md) for the full flow.

> 💡 **Just want the final answer?** Use `--terseio` instead — it reads all
> of stdin as one prompt (or one command if it starts with `:`) and prints
> only the final answer (errors to stderr).
> See [Terse IO Mode](terseio.md).

## Session Persistence

> ⚠️ Since plain IO only processes **one prompt per invocation**, saving
> and resuming the conversation across invocations is essential for
> multi-turn interactions. Use `--session` for this.

Plain IO can persist conversations using a **session file**, just like the
TUI mode. The session file records every turn (prompts, assistant replies,
tool calls, tool results) in key-value frontmatter + binary TLV format.

**How it works:**

1. **Auto-save** — After each prompt completes, the conversation is
   automatically saved to the session file. The file is always up to date.
2. **Auto-restore** — When you start with the same session file, the
   previous conversation is loaded and replayed so the assistant sees
   the full history.
3. **Multiple questions, same conversation** — Each `--plainio`
   invocation adds one turn to the conversation.

**Example — multi-turn conversation with session persistence:**

```sh
# First prompt — creates the session file
alayacore --plainio --session my-convo.alaya <<< "my name is Alice"

# Second prompt — loads the previous conversation, appends this turn
alayacore --plainio --session my-convo.alaya <<< "what is my name?"

# Third prompt — session now has 3 turns
alayacore --plainio --session my-convo.alaya <<< "remember this fact: dogs are fluffy"
```

Since the session file contains binary TLV data after the key-value frontmatter, it is not human-readable as plain text. Use `tlvcat.go` (in `misc/`) to inspect the contents, or use the `--plainio` mode to replay the conversation.

## Examples

```sh
# Interactive session on any terminal (type a prompt, Ctrl-D to end)
alayacore --plainio

# Interactive session with a saved conversation
alayacore --plainio --session my-convo.alaya

# Pipe a single question (works, but --terseio is cleaner for scripts)
echo "what is 2+2?" | alayacore --plainio
```

> 💡 **Scripting or piping?** Use [`--terseio`](terseio.md) — it reads all of
> stdin as one prompt (or one command if it starts with `:`) and prints only
> the final answer (errors to stderr).
