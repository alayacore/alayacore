# Session Persistence

AlayaCore allows you to save and restore conversations manually using session files.

## Behavior

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Load**: On startup, AlayaCore creates a new empty session unless you specify `--session` to load an existing one
- **Path expansion**: Paths like `~/mysession.md` are expanded to your home directory

## What's Saved

- Message history (user prompts and assistant responses)
- Token usage statistics (total and context)
- Provider configuration (BaseURL, ModelName)

## What's Not Saved

- Reasoning/thinking content (streamed output not stored)
- Tool call details (only message history is preserved)

## Session Commands

```sh
:save                    # Save to current session file (if set with --session)
:save ~/mysession.md    # Save to specific file
:cancel                  # Cancel current request and clear todos (with confirmation)
:summarize              # Summarize the entire conversation to a single message
```

## Loading Sessions

You can load an existing session using the `--session` flag:

```sh
# Load a specific session file
alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --session ~/mysession.md

# Start fresh with a new session (default behavior)
alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o
```
