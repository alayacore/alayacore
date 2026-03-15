# Session Persistence

AlayaCore allows you to save and restore conversations manually using session files.

## Behavior

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Load**: On startup, AlayaCore creates a new empty session unless you specify `--session` to load an existing one
- **Path expansion**: Paths like `~/mysession.md` are expanded to your home directory
- **Auto-summarize**: When `--context-limit` is set, AlayaCore automatically triggers `:summarize` when context reaches 80% of the limit to prevent context overflow errors

## What's Saved

- Message history (user prompts and assistant responses)
- Reasoning/thinking content
- Tool calls and their results
- Token usage statistics (total and context)
- Provider configuration (BaseURL, ModelName)

## What's Not Saved

- Streamed output display state

## Session Commands

```sh
:save                    # Save to current session file (if set with --session)
:save ~/mysession.md    # Save to specific file
:cancel                  # Cancel current request (with confirmation)
:summarize              # Summarize the entire conversation to a single message
```

## Loading Sessions

You can load an existing session using the `--session` flag:

```sh
# Load a specific session file (uses models from config file)
alayacore --session ~/mysession.md

# Start fresh with a new session (default behavior)
alayacore

# Load a session with custom model config
alayacore --model-config ./my-model.conf --session ~/mysession.md
```

## File Format

Session files use a hybrid format:

1. **YAML frontmatter** for metadata (human-readable)
2. **TLV-encoded binary** for messages (recursion-safe)

```
---
base_url: https://api.openai.com/v1
model_name: gpt-4o
total_tokens: 1234
context_tokens: 500
created_at: 2024-01-15T10:30:00Z
updated_at: 2024-01-15T10:45:00Z
---


TU    Hello, how are you?

TA    I'm doing well, thanks!

TR    User is asking about my state...

FC    {"id":"call1","name":"read_file","input":"..."}

FR    {"id":"call1","output":"file contents..."}
```

Each message part is encoded as:
- Blank line separator (`\n\n`)
- 2 bytes: tag (e.g., `TU`=user text, `TA`=assistant text, `TR`=reasoning, `FC`=tool call, `FR`=tool result)
- 4 bytes: length (big-endian)
- N bytes: content (text or JSON for tool calls/results)

**TLV Tags:**
- `TU` (Text User) - User text input
- `TA` (Text Assistant) - Assistant text output
- `TR` (Text Reasoning) - Reasoning/thinking content
- `FC` (Function Call) - Tool call (JSON encoded)
- `FR` (Function Result) - Tool result (JSON encoded)

The TLV (Tag-Length-Value) encoding prevents recursion issues when session files contain tool results that include session-like content. The blank line separators make the file more readable when opened in a text editor.

Backward compatibility: The parser can still read old NUL-separated format files.
