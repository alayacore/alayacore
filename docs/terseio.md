# Terse IO Mode (`--terseio`)

`--terseio` is the scripting-focused counterpart of `--plainio`: it reads
**all of stdin as a single prompt** and prints **only the final answer** to
stdout. Everything in between — reasoning, tool calls, tool results, prompts,
notifications — is suppressed. The full conversation is still persisted in the
session file (`--session`), so follow-up invocations see the complete history
even though it was never printed.

Use it when you want a clean, pipeable answer:

```sh
echo "what is 2+2?" | alayacore --terseio
alayacore --terseio < question.txt > answer.txt
```

## Contract

| Stream | Content |
|--------|---------|
| stdin | The entire input, read until EOF, treated as **one** prompt (inner newlines preserved, trailing newlines trimmed) |
| stdout | Only the final assistant text answer (with a trailing newline) |
| stderr | Errors (`[error: ...]`), notifications (`[...]`) |
| exit code | `0` success, `1` session error, `2` flag conflict (`--tool-confirm`), `130` SIGINT |

Empty stdin produces no prompt and exits `0` with empty output.

If the final assistant message contains **no text** (e.g. reasoning-only or
tool-call-only), stdout is empty — an intermediate text from an earlier
message is never printed as if it were the answer. Use the exit code and the
session file to diagnose such cases.

> ⚠️ **`--terseio` and `--tool-confirm` are mutually exclusive.** terseio
> consumes all of stdin as the prompt, so tool confirmations (normally
> answered via subsequent stdin lines) could never be resolved. The
> conflict is rejected at startup with exit code `2`. Use `--plainio`
> if you need interactive tool confirmation.

## Differences from `--plainio`

| | `--plainio` | `--terseio` |
|--|-------------|-------------|
| stdin | line-based prompts (backslash continuation for multi-line) | all of stdin = one prompt |
| stdout | full transcript: prompts, reasoning, tool JSON, results | final answer only |
| errors | printed to stdout | printed to stderr (stdout stays clean) |
| tool confirmations | interactive (`:tool_confirm <id>` / `:tool_decline <id>`) | rejected at startup (`--tool-confirm` conflict) |
| MCP OAuth authorization | automatic (callback server + browser) with manual fallback | not supported — prompts are rejected (`MCP_NOT_READY`) while authorization is pending; use `--plainio` for OAuth-protected servers |
| commands (`:save`, `:cancel`, ...) | supported | not supported (stdin is prompt text) |

## Examples

```sh
# One-shot answer — the file contains ONLY the answer
echo "explain gravity in one sentence" | alayacore --terseio > answer.txt

# Multi-line prompts work naturally (no backslash continuations)
alayacore --terseio < prompt.txt

# Pipe the answer into another tool
alayacore --terseio < question.txt | jq -r .

# Multi-turn: persist the conversation, print only the new answer each time
alayacore --terseio --session my-convo.alaya <<< "my name is Alice"
alayacore --terseio --session my-convo.alaya <<< "what is my name?"

# Check success without mixing diagnostics into the answer
if alayacore --terseio < question.txt > answer.txt 2> err.log; then
  echo "answer: $(cat answer.txt)"
else
  echo "failed: $(cat err.log)"
fi
```

## Notes

- **`--session`** — intermediate content (tool calls, reasoning) is saved to
  the session file, so a follow-up invocation sees the full history even
  though it was never printed.
- **`--tool-confirm`** — rejected at startup (`exit 2`): terseio consumes
  stdin, so confirmations could never be answered. Use `--plainio` if you
  need interactive confirmation.
- **Ctrl-D / EOF** ends the prompt; the process exits after the task
  finishes. **Ctrl-C** exits immediately (exit code 130).
- **Not a TUI** — no terminal features are used, so it works on any terminal, including teletype,
  or in pipelines, just like `--plainio`.
