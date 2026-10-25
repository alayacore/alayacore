# Tool Execution: Concurrent or Serial, with Per-Tool Confirmation

AlayaCore runs tool calls in one of two modes, chosen per model by
`serial_tool_calls` in `model.conf` (see
[configuration.md](configuration.md#tool-calling-mode-serial_tool_calls)).
Both modes share one implementation of what a tool call does; they differ only
in when calls start.

| Mode | Selected by | Calls run |
|---|---|---|
| **Concurrent** (default) | `serial_tool_calls` omitted or `false` | Each starts as soon as its arguments complete, so calls overlap |
| **Serial** | `serial_tool_calls: true` | One at a time, in the order the model made them, after the stream has ended |

Serial mode exists because a great many models and servers have no notion of
parallel tool calls: their side effects have to land in the sequence the model
asked for. Keeping two calls from writing the same file at once is no longer
part of its job — that is now a property of the tools themselves, in either mode
(see [Writes Exclude All Access, and Order
Nothing](#writes-exclude-all-access-and-order-nothing)).

Whether a call can start while later calls are still streaming is the provider's
doing, not the mode's: Anthropic closes a tool block mid-message, so there the
overlap includes the stream; OpenAI delivers every closure together after its
stream ends, so there the overlap is only between tools (see
[providers.md](providers.md#complete-event-order)).

## One Lifecycle, Two Drivers

The life of a single call — ask for confirmation when the tool needs it,
execute, produce the part that answers the call — lives in `llm.Agent.runToolCall`
and nowhere else. The two drivers behind the `toolRunner` interface differ only in
when a call may start: the concurrent one launches a goroutine per call from
inside the streaming loop, the serial one waits for the stream to end. So the two
modes cannot disagree about what a denied, failed, or canceled call means, and a
fix to one cannot silently miss the other.

`streamEvents` itself contains no mode check at all. It calls `runner.add(...)`
when a call's arguments complete and `runner.finish()` after the stream, and
`newToolRunner` is the single place that reads the setting.

`runToolCall` reports a second value: whether the call ever ran. A call canceled
while awaiting confirmation ran nowhere and produced no side effect, so it must
stay out of history — an assistant `tool_use` without the `tool_result` that
answers it is a conversation the next request cannot build. Both drivers drop it
identically, because both are the same function.

Anything that changes what a tool call does belongs in `runToolCall`, never in a
driver.

## How It Works (concurrent mode)

When a `ToolInputPart` event arrives during streaming, the agent calls `ToolNeedsConfirm(toolName)`:

1. **No confirmation needed** — The tool executes immediately in a goroutine. Results flow back through a channel and are appended in receive order.
2. **Confirmation needed** — A goroutine is started that obtains a per-tool confirm channel and blocks until the user responds. Once confirmed, the tool executes in the same goroutine.

All results are collected and then attached by tool call ID, in the order the calls were made.

## How It Works (serial mode)

Completed calls are queued instead of launched — nothing runs, so nothing can
have a side effect. The queue is built in the order the provider declared the
calls (OpenAI sorts by `tool_calls[].index`, Anthropic by block index), which is
the order the model made them and not necessarily the order their argument
fragments arrived in.

Once the stream has ended, the driver runs the queue front to back, each call
finishing before the next begins, and their results go straight into the same
list the concurrent collector fills. History is therefore identical in both
modes: same parts, same order.

Two properties follow from starting after the stream:

- **A stream that fails runs nothing.** In concurrent mode a call could already
  be mid-side-effect when the stream died. Serial mode has no such window.
- **Cancellation between two calls keeps both truths.** The calls that ran are
  salvaged into history with their results; the calls that never started are
  dropped rather than recorded as unanswered. This is the same forgiving pairing
  the concurrent error path uses, reached by returning the context error.

Declining one tool answers that call with an error and lets the remaining calls
run: a refusal is the user's answer to one call, not a stop order for the step.

See `internal/llm/agent.go` → `Stream()`, `streamEvents()` (which contains no
mode check at all), and `internal/llm/agent_execution.go` → `toolRunner`,
`serialToolRunner`, `parallelToolRunner`, `runToolCall`, `executeTool`.

## Writes Exclude All Access, and Order Nothing

Concurrent mode launches a goroutine per call, so a model that emits two
`edit_file` calls for the same file in one step runs them at once. Both are
read-modify-write operations that end in a rename, so the second rename would
silently discard the first edit — a lost update neither the model nor the user
is told about, and one the user cannot prevent, since the model chose to emit
the pair.

That is fixed in the tools, not in a driver: `edit_file` and `write_file` take a
single package-level read-write mutex (`fileMutationMu`) around their whole
mutation, so at most one in-process file mutation runs at a time — against any
reader as well as against another writer. `write_file` is in the same lock
chiefly to protect `edit_file`: a full write that landed between an edit's read
and its rename would be swallowed by that rename. The runner is unchanged and
stays concurrent; only the file writes exclude anything, and they are a few
syscalls, so nothing that matters loses parallelism.

`read_file` takes the read side of that lock for the length of its read, for a
reason the writers do not have. Against `edit_file` a reader would need no lock
at all: the commit is a rename, so it sees the old file or the new one, never a
torn one. `write_file`'s in-place fallback is not atomic — `writeInPlace`,
reachable when the directory cannot hold a temp file, truncates the target and
then writes into it, so a read inside that window returns a prefix of the new
content as if it were the whole file: shorter, plausible, and with nothing in it
to say it was cut. That is the one read-side tear a lock can prevent, and the
model could not detect it if it happened.

The lock is an `RWMutex`, and reads take the read side, because excluding reads
from a write is the entire requirement and excluding reads from each other is
not part of it. A model that reads eight files in one step still reads them in
parallel; only a writer waits for them all. Measured on this machine with eight
concurrent `read_file` calls, the alternative — one mutex for all three tools —
cost 2.2x on 8x8MB (21ms vs 9.7ms) and 2.4x on 8x2KB (160µs vs 67µs), all of it
pure loss. Go's `RWMutex` prefers a waiting writer over newly arriving readers,
so a run of reads cannot starve an `edit_file`.

The price of the read side is that a read which blocks — a FIFO, a slow network
mount — holds it while it blocks, and every mutation queues behind it (other
reads do not). Such a read already keeps the step from finishing, so nothing
that could have run is held up by it. The one read that is routinely long is a
full read of a file over the 64KB text budget: `readLargeFileTruncated` scans to
EOF so its header can report the total line count, which makes "one file's worth
of reads" the whole file. Left as is — the total is what makes the header useful,
and the fix, if it is ever wanted, is to report the count as a floor ("at least N
lines") instead of reading the rest to learn it.

It is one coarse lock rather than a map keyed by path because a path is not a
sound file identity (hard links and case-insensitive filesystems both map two
paths to one file), and the critical sections — a few syscalls for a write, one
file's worth of reads for `read_file` — have nothing to gain from finer
granularity.

**The lock removes interleaving; it does not impose order.** Two calls that take
it run one at a time, but which goes first is whichever the scheduler reaches
first, and a mutex has no way to say "the order the model made them in". For two
edits the gap is usually audible — when one's `old_string` lives in what the
other introduced, the one that ran too early fails, and the model is told. For a
`write_file` followed by an `edit_file` on the same file it is silent: the edit
reads and rewrites the pre-write content, the write then replaces the whole
file, and both calls report success while the file holds neither result. Order
is a mode, not a lock — only `serial_tool_calls: true` runs a step's calls in
the order the model declared them (see
[configuration.md](configuration.md#tool-calling-mode-serial_tool_calls)).

The system prompt therefore states the order contract the model can act on:
calls made together may run concurrently and in any order, and a call that needs
another's result belongs in a later step (`systemPromptBase` in
`internal/app/app.go`). That is the **weak** guarantee — the one every
`model.conf` satisfies — and deliberately not the serial one, even though serial
mode would make a stronger statement true. The prompt is one string, built once
at startup and handed unchanged to whichever model the session switches to, so
a promise written for serial mode would be broken by `:model_set` to a model
that does not set the option, and the history carries the consequences of that
reliance across the switch. The cost of the weak wording is bounded and one-way:
a serial-mode model that would have chained a dependent call inside a single
step spends one more step instead — never a wrong result, because the rule it
follows is always safe. The wording also has to keep batching attractive, which
is why it says "batch independent calls" rather than leaving the model to infer
that one call per step is what "may run in any order" wants.

Scope: this serializes access **within this process** only. A competing write
from outside it — another instance, an editor, `git`, a sync client, ripgrep
under `search_content` — is not detected and still wins last, exactly as it
would for any editor. Neither is a shell command run by `execute_command` that
writes the same file, which the lock cannot cover. Detecting those would need a
compare-and-swap at commit time.

## Execution Strategy

| Phase | Mode | Execution |
|-------|-------|-----------|
| **During streaming** | Concurrent, no confirmation needed | Goroutine per call, results appended and re-ordered by ID |
| **During streaming** | Concurrent, confirmation needed | Goroutine blocks on per-tool confirm channel, then executes |
| **During streaming** | Serial | Calls are queued; nothing executes |
| **After the stream ends** | Serial | One call at a time, in declared order, each awaited before the next |
| **Final** | Both | Re-ordered by tool call ID to match LLM response order |

## Confirmation

`ToolNeedsConfirm` filters which tools need user approval. When a tool needs confirmation, `OnToolConfirm` is called **per tool** and returns a per-tool channel. The caller blocks on this channel — a per-tool goroutine in concurrent mode, the serial driver itself in serial mode. The session stores the channel in a map keyed by tool call ID. When the user responds via `:tool_confirm <call_id>` or `:tool_decline <call_id>`, the session looks up the channel in the map and writes the response, unblocking the waiter which then executes the tool.

Each tool has its own confirm channel (buffered, capacity 1), following the same pattern used by MCP OAuth confirmation. This keeps the tool lifecycle continuous — no artificial segmentation between streaming, confirmation collection, and execution.

The TUI adapter processes confirmations sequentially (one dialog at a time). Other adapters can process them in parallel. In serial mode there is at most one confirmation outstanding per step anyway, so a parallel-capable adapter sees the same one-at-a-time flow the TUI enforces.

## Implementation

In concurrent mode all results (from both confirmed and unconfirmed tools) flow through a single shared channel, which is what turns a set of senders finishing in arbitrary order into one list. In serial mode the driver already holds that list in call order. Both feed the same pairing step:

```go
stepContents = attachToolResults(stepContents, results, strict)
```

`attachToolResults` matches each `ToolOutputPart` to its `ToolInputPart` by ID and appends the results in call order, so the record matches the sequence the model asked for. On a step that ended early it runs in forgiving mode and drops calls whose results never arrived, rather than recording a `tool_use` no one answered.

## Error Handling

Tool goroutines run under a **per-stream context** and are tracked with a
`WaitGroup`. If the stream fails mid-execution (provider error, callback
failure, reorder failure), `streamEvents` cancels that context and **waits
for all in-flight tool goroutines to terminate before returning**:

- A tool that is still executing (e.g. `execute_command`, `search_content`)
  is canceled via its context — no tool keeps running and no side effects
  happen after the stream has errored.
- A tool awaiting user confirmation is unblocked by the cancellation — a
  late `:tool_confirm` response can no longer execute the tool against a
  stale context, and the tool produces no result (it never executed).
- Results of tools that already **executed** are **salvaged**: `streamEvents`
  pairs each delivered result with its tool call and folds the
  `[tool_use, tool_result]` pairs into the conversation history (via
  `OnStepFinish`) before returning the error, so a retry or `:continue`
  sees a state consistent with the side effects that happened. A tool
  that never completed (still running when the stream died, dropped by a
  racing result send, or never confirmed) produces no history entry — an
  assistant `tool_use` never appears without its `tool_result`. See
  `attachToolResults` in forgiving mode.

This guarantees `Stream()` never returns while a tool goroutine is still
alive, preventing goroutine leaks and post-error side effects.

The serial mode needs none of that machinery and inherits its guarantees
instead: no goroutine is ever started, so there is nothing to wait for, and a
stream that errors before the driver runs leaves no side effect at all. What
both modes share is the per-stream context handed to each tool — the tool is
still the only thing that decides what cancellation means — and the salvage
path, which is reached from serial by returning the context error like the
concurrent collector does.

## What the Model Sees When a Tool Fails

A tool result reaches the model as text plus an error flag: `is_error` on the
wire — the `UF` payload and Anthropic name it the same — and a
`{"status":"error",...}` content wrapper for OpenAI. The flag says *that*
something failed and never *why*, so the reason has to be in the text. One
question decides where a tool puts it: does the tool produce output of its own?

- **A tool that fails with nothing else to say returns `(nil, err)`**, and its
  error string becomes the result text (`newToolOutput` in
  `internal/llm/agent_execution.go`). `read_file`, `edit_file`, `write_file`,
  `search_content` and the MCP tools all take this path, so whatever they report
  reaches the model as the error's own words — `timed out: context deadline
  exceeded` for a stop they classify with a sentinel, `context canceled` for
  `read_file`'s raw context error, `file not found: x` for `edit_file` — without
  any of them writing that text a second time. The substitution is a fallback
  for that case, not a general error channel: it fires only when the result is
  empty, because nothing at that layer can tell which part of a non-empty result
  is output and which is the reason.
- **A tool that produces text of its own must say why it stopped in that text.**
  `execute_command` is the only one. A command may have printed half its output
  before it was stopped, and that output has to be shown, so its result is never
  empty and the fallback above never applies to it. It states the reason in the
  header `commandHeader` builds, because the exit status cannot: a command this
  tool stops surfaces as an ordinary signal kill (128+signal on Unix, 1 on
  Windows), indistinguishable from any other kill. The same header is written
  into the saved file when the output is large, so a model that has to read the
  full output back through `read_file` is told the same thing in the same words
  (see [truncation.md](truncation.md#execute_command)).

Both paths deliver the same fact through the same channel — the text — from
whichever layer knows it. The wording is deliberately not shared: `search_content`
surfaces the error string, `execute_command` writes a sentence, and neither is
asked to imitate the other.

## What a Tool's Text May Say

That text is the only channel, so every word in it is paid for on every call.
One rule keeps it honest: **it may carry only what the caller cannot derive from
its own call.**

- **Nothing the caller sent comes back.** The arguments are in the model's own
  history, so echoing them adds bytes without adding information. `edit_file`
  used to append the whole `old_string` to a failed match — re-sending, escaped,
  a payload the model had just written, in a message whose only reader already
  knew its contents. For the same reason a failure does not repeat the path it
  was given: the model sent it, and the call that failed carries it too.
- **What the caller could not know is stated.** This is the inverse defect, and
  the more damaging one, because silence reads as an answer. `search_content`
  resolves a relative — or omitted — `path` against the process directory, which
  the model cannot see, so a bare "No matches found" is a false negative it has
  no way to detect: the root that was searched travels with the result
  (`noMatches` in `internal/tools/search_content.go`). That root is not enough on
  its own, because it is not what the search covered — ripgrep's defaults skip
  hidden, gitignored and binary files and never follow symlinks, so the answer
  names those too, or "searched X" would claim a coverage X does not have.
  `execute_command` names the resolved absolute `workdir` for the same reason,
  and `edit_file` tells a dangling symlink apart from a missing file rather than
  reporting "not found" for a path that exists.
- **A next step is not a diagnosis.** `old_string not found` has several causes —
  a stale read, a mis-transcription, differing whitespace — and every one of them
  has the same remedy, so the message states the remedy (read the file, copy from
  what came back) instead of guessing at a cause. A guess that is wrong sends the
  model to fix something that was never broken.
- **A partial result is still a result.** ripgrep exits 2 for a run that hit an
  error and kept going — an unreadable subdirectory beside readable ones — with
  the permission complaint on stderr and every match it could read on stdout.
  Reporting that as the whole answer would discard matches the model can check
  for one it cannot: `search_content` delivers the output with the warning beside
  it and reserves the error for a run that has nothing to show. The same rule is
  why `execute_command` prints a partial stream and a reason rather than nothing.
