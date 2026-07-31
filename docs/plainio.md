# Plain IO Mode

`--plainio` runs AlayaCore as a plain stdin/stdout process with no terminal UI. Useful for scripting, piping, and headless environments.

## Input

- Each line from stdin is treated as a separate prompt (one prompt per invocation — see note below)
- A trailing backslash (`\`) continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin. After EOF, the program waits for the
  current task to finish, then exits with code `0`.
- **Ctrl-C** (SIGINT): terminates immediately with default signal handling
  (exit code 130).

> **⚠️ One task at a time.** Plain IO processes prompts **one at a time**
> and has no task queue. If you pipe multiple prompts into stdin, only the
> **first** one is executed. Subsequent prompts are rejected with:
> ```
> Error: A task is already running. Wait for it to complete or cancel it.
> ```
> For scripting multiple questions, launch `alayacore --plainio` once per
> prompt (the process spawn cost is negligible).

## Output

All output is plain text with no ANSI escape codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `> prompt` |
| Tool calls | Raw JSON (id, name, input) |
| Tool results | Raw JSON (id, output, is_error) |
| Command results | Success: `[Command completed]`; failure: `[error: message]` (does not affect exit code) |
| Errors | `Error: message` |
| Notifications | `[message]` |
| Tool confirmations | `[tool_confirm: allow tool "id" to run?]` |

Respond with `:tool_confirm <id>` to allow or `:tool_decline <id>` to deny.

A blank line separates messages of different types.

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
# Pipe a single question
echo "what is 2+2?" | alayacore --plainio

# Use in scripts — one prompt per invocation
echo "what is 2+2?" | alayacore --plainio > answer.txt
echo "explain gravity" | alayacore --plainio >> answers.txt

# Read a single prompt from a file
alayacore --plainio < question.txt > answer.txt
```
