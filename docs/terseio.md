# Terse IO Mode (`--terseio`)

`--terseio` is the scripting-focused counterpart of `--plainio`: it reads
**all of stdin as a single prompt** — or, if the input starts with `:`, as a
**single command** — and prints **only the final answer** to
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
| stdin | The entire input, read until EOF, treated as **one** message. If it starts with `:`, it is **one command** (sent as a CI frame); otherwise it is **one prompt** (inner newlines preserved, trailing newlines trimmed) |
| stdout | Only the final assistant text answer (with a trailing newline) |
| stderr | Errors (`[error: ...]`), notifications (`[...]`), informative command results (e.g. `[Session saved to ...]`) |
| exit code | `0` success, `1` session or command error, `2` flag conflict (`--tool-confirm`), `130` SIGINT |

**`Ctrl-C`** (SIGINT) cancels the running task through the session
instead of killing the process: the task (and its tool processes) is
aborted cleanly, the buffered final answer is **discarded** (no partial
output on stdout), and the process exits `130` (128+SIGINT) — scripts
still detect the interruption by exit code. Ctrl-C during the stdin read
phase (interactive use without EOF) aborts the read; Ctrl-C after the
task finished only forces the exit code.

Any SM error sets exit code `1` — including failed persistence (auto-save, pre-summarize backup) and failed auto-summarization, since these put the session at risk. Such errors also discard the buffered final answer: a script should retry or inspect the session file.

Empty stdin produces no prompt and exits `0` with empty output.

If the final assistant message contains **no text** (e.g. reasoning-only or
tool-call-only), stdout is empty — an intermediate text from an earlier
message is never printed as if it were the answer. Use the exit code and the
session file to diagnose such cases.

## Commands

If stdin (after trimming trailing newlines) starts with `:`, the **whole
input is sent as one command** — `:continue`, `:cancel`, `:save /tmp/x`,
`:model_set <id>`, ... all work. The command name is the text up to the
first whitespace (space, tab, or newline); the rest is the argument text,
so multi-line command input is fine:

```sh
# Retry the last prompt (after a max-steps / API error)
alayacore --terseio --session my-convo.alaya <<< ":continue"

# Save the session, then check the exit code
alayacore --terseio --session my-convo.alaya <<< ":save backup.alaya"
```

- Command **errors** go to stderr and set exit code `1` — a failed command
  is a failure signal, so scripts can react.
- Command **successes** print informative results to stderr (e.g. `:save` →
  `[Session saved to backup.alaya]`) and stay silent for self-evident ones
  (`:cancel`) and async task commands (`:continue`, `:summarize`) whose real
  feedback is the final answer on stdout.
- `:quit` / `:q` exit cleanly (code `0`) without sending anything, like in
  plainio.
- ⚠️ **The `:` prefix is not escapable** — a prompt that genuinely starts
  with `:` must be run via `--plainio` (or a leading space added).

> ⚠️ **`--terseio` and `--tool-confirm` are mutually exclusive.** terseio
> consumes all of stdin as the prompt/command, so tool confirmations (normally
> answered via subsequent stdin lines) could never be resolved. The
> conflict is rejected at startup with exit code `2`. Use `--plainio`
> if you need interactive tool confirmation.

## Differences from `--plainio`

| | `--plainio` | `--terseio` |
|--|-------------|-------------|
| stdin | line-based prompts (backslash continuation for multi-line) | all of stdin = one prompt or one command |
| stdout | full transcript: prompts, reasoning, tool JSON, results | final answer only |
| errors | printed to stdout, never affect exit code | printed to stderr; session **and command** errors set exit code 1 |
| tool confirmations | interactive (`:tool_confirm <id>` / `:tool_decline <id>`) | rejected at startup (`--tool-confirm` conflict) |
| MCP OAuth authorization | automatic (callback server + browser) with manual fallback | not supported — prompts are rejected (`MCP_NOT_READY`) while authorization is pending; use `--plainio` for OAuth-protected servers |
| commands (`:save`, `:continue`, ...) | any line starting with `:` is a command | whole stdin starting with `:` is one command |

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
- **Error recovery** — after a task error (`--max-steps`, API failure, ...)
  the script can retry without re-sending the prompt: `alayacore --terseio
  --session my-convo.alaya <<< ":continue"`. The error itself is never
  persisted into the session, so the retry is clean.
- **`--tool-confirm`** — rejected at startup (`exit 2`): terseio consumes
  stdin, so confirmations could never be answered. Use `--plainio` if you
  need interactive confirmation.
- **Ctrl-D / EOF** ends the input; the process exits after the task or
  command finishes. **Ctrl-C** exits immediately (exit code 130).
- **Not a TUI** — no terminal features are used, so it works on any terminal, including teletype,
  or in pipelines, just like `--plainio`.
