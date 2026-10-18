# Architecture

AlayaCore follows a layered architecture with **strict adapter-agent isolation**.
The adapter (UI) and agent (session + LLM) communicate **exclusively** via a
lightweight frame protocol — one frame per line, no direct
function calls, no shared mutable state, no bypass. See [development-principles.md](development-principles.md)
for the full rules.

## Components

### Entry Point (`main.go`, `internal/config/`, `internal/app/`)

The entry point wires together all components:

1. **`config.Parse()`** — Parses CLI flags into `config.Settings`
2. **`app.Setup()`** — Initializes shared components:
   - Skills manager (loads skill metadata from the directories given to `--skill`, one level deep)
   - Tools (`read_file`, `edit_file`, `write_file`, `execute_command`, `search_content` — controlled via `--builtin-tools` flag)
   - System prompt (default + skills section/fragment when configured + current working directory)
3. **Adapter creation** — Starts the terminal, PlainIO, TerseIO, or RawIO adapter

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

| Component | Description |
|-----------|-------------|
| `Session` | Main struct managing conversation state, message history, and task execution |
| Model management | Owns model config loading, runtime settings, provider/agent creation, reasoning level, and model resolution |
| MCP lifecycle | Owns MCP initialization (connect, OAuth, discover, ready flag) |
| Session persistence | Handles session file I/O and markdown/frame serialization |
| Command dispatch | Declarative command registration and dispatch for `:save`, `:cancel`, etc. |
| Model config loading | Loads and manages AI model configurations from `model.conf`. Persists edits from `:model_sync` back to the file. |
| Runtime settings | Persists runtime settings (active model, active theme) to `runtime.conf` |
| `ContextTokens` | Tracks conversation context size across API calls. See [context-tracking.md](context-tracking.md). |
| `StepStats` | Measures per-step provider speed (end-to-end tok/s) and time-to-first-token, broadcast via the `task` system message for the status bar. See [speed-tracking.md](speed-tracking.md). |

#### Concurrency Model

The session uses three goroutines for concurrent operation:

| Goroutine | Source | Role |
|-----------|--------|------|
| **Main loop** (`run()`) | `Session.Start()` | Owns all mutable state, processes commands |
| **Input pump** (`inputPump()`) | launched by `run()` | Reads frames from the input stream, sends parsed messages to the main loop |
| **Task worker** (`runTaskNormal` / `runTaskContinue` / `runTaskSummarize`) | spawned by `run()` per task | Executes a single task (LLM streaming + tool calls), returns final messages via `taskResultCh` |

**Cross-goroutine communication:**

| Mechanism | From | To | Purpose |
|-----------|------|----|---------|
| `inputMsgCh` (buffered, cap 100) | inputPump | run() | Parsed user input messages |
| `cancelReqCh` (buffered, cap 1) | any goroutine | run() | Request task cancellation; run() replies with whether a task was canceled |
| `taskResultCh` (buffered, cap 1) | task worker | run() | Return final messages and signal task completion |
| `taskEventCh` (buffered, cap 64) | task worker | run() | Step progress, token counts |
| `outputBroken` (atomic.Bool) | both | — | Output stream failure flag (any goroutine can set) |
| `confirmChs` (map + mutex) | both | — | Per-tool confirmation channel map (MCP-style) |
| `steering` (slice + mutex) | both | — | Prompts that arrive while a task runs, waiting for that task's next step boundary |

**Lifecycle — the end of input:**

Input ends when the adapter's stream ends (EOF: `inputPump` closes `inputMsgCh`)
or when the adapter says so with a `CE` frame — the same fact on a stream that
stays open for commands. Either way `run()` records that its input has ended and
keeps looping: a task that is already running, or a prompt accepted before the
session was ready, is work the user asked for, so the loop goes on serving
cancels, task events, and task results until nothing is in flight. Only then
does it return — after broadcasting the terminal `session{state:"closed"}` frame.

```
input ends (EOF or CE) ──▶ run() records it ──┐
                                              │
                        ┌─────────────────────┴──────────────────────┐
                        │ a task, or a prompt waiting for readiness, │
                        │ is still in flight → keep looping (serves  │
                        │ :cancel and task events) until it is not   │
                        └─────────────────────┬──────────────────────┘
                                              │
                        session{state:"closed"} goes out ──▶ return
```

**State ownership:**
- The input pump goroutine is a pure frame parser. It reads frames from the input stream, builds inputMsg values, and sends them to `run()` via `inputMsgCh`. It has zero knowledge of commands and never touches session state — commands arrive as CI frames (`TagCommandIn`) and are dispatched entirely in `run()`. All command dispatch, cancellation, and output writing happens in `run()`.
- Commands are a request/response control plane: CI `{command_id, name, args}` → CO `{command_id, result, is_error}`. Results never travel as SM `error`/`notify` — those are reserved for non-command events.
- `sendSystemInfo` runs only in the `run()` goroutine; the task goroutine sends state mutations via `taskEventCh` which trigger broadcasts.

**Gotcha — everything is in run():** There is no "fast path" in the input pump for latency-critical commands. The `inputMsgCh` buffer is cap 100 but each message is processed in microseconds — the input channel drains orders of magnitude faster than a human can type or an LLM can stream. If you're tempted to add a special case to the input pump, ask: is the latency measurable? If not, keep it in `run()` where it belongs.

**Design rationale:** Tasks must run in a separate goroutine because LLM streaming is blocking (3-10s per step). If tasks ran synchronously in `run()`, the main loop could not process user input (`:cancel`, new prompts, immediate commands) during task execution. The per-task goroutine keeps the main loop responsive.

#### Steering — a prompt that arrives mid-task

One task runs at a time, and there is no task queue (see the `run()` comment in
`internal/agent/session_loop.go`). A prompt that arrives while a task is in
flight is therefore neither parked as a second task nor refused: it is **spliced
into the running task** at that task's next step boundary, so a correction
reaches the model without canceling the step in flight and paying for a repeat
task. Only a full steering queue is refused (reported as a `steering` message
with phase `refused`, not the `STATE_BUSY` refusal a busy session answers a
command with).

The boundary is the only injection point there can be. It is the one place the
agent loop lets a caller rewrite the history (`OnBeforeSend`) and the one place
the history is a valid request — by then every tool call of the previous step has
its result. Words typed while a tool is running thus reach the model *after* that
tool returns, never between a call and its answer (which the Anthropic Messages
API rejects outright).

Delivery is a single admission at the splice, which is what keeps `Contents` and
the adapter's view of history IDs in step. A part is fitted, given its user role,
numbered, echoed and published to `Contents` the moment it is spliced into a
request — the same treatment a fresh prompt gets in `runTaskNormal`, at the same
point in its life. The echo is *not* deferred to the step's `OnStepFinish`: the
adapter draws windows in the order their frames arrive, so an echo that waited
would land *under* the answer it caused — the transcript showing a prompt the
model had already answered. And the `Contents` entry goes with the echo, not with
the step's delta, so a `:save` or `:fork` during that step sees what the
transcript already shows.

The rule for where a steering message ends up turns on one question — had the
task spliced it in before the task ended?

- **Spliced** — the words were sent to the model and echoed to the adapter, so
  they are part of the conversation: numbered, in `Contents`, and a failure or a
  cancel afterwards cannot take them back.
- **Still queued when the task ended** — delivered as the next prompt if the task
  landed (the model answered without calling another tool, which is what "type
  the next thing while it is working" means, and the common case), or dropped
  with a `steering` message (phase `dropped`) if it did not. These words never reached a step, so a task
  that dies cannot leave them to run on afterwards. An input arriving *after* the
  task ends is untouched by any of this: it was never part of that task.

A canceled task is the clearest case of the second rule, since a queue that
survived it would start a new task on its own the moment the canceled one
unwound — a stop button that keeps going. The words it had already spliced stay,
because the model has been given them.

The take side needs no rule of its own: it falls out of the same distinction.
Only a `userPrompt` is given the hook that can reach the queue, so a
`summarizeCall` — even a multi-step one, tools and all — never takes the words,
and that has to be so: its history is a copy that either replaces the conversation
or is thrown away, so words consumed by it would vanish with nothing to report,
because from the queue's point of view they had been delivered. See
`internal/agent/session_steering.go` and `session_task.go`'s `processPrompt`.


### Session Persistence

- **Auto-save** — Always enabled when `--session` is specified. The session file is written after each task completes (and on `:save`). During a running task, completed steps are synced into the in-memory `Contents` via task events, so a `:save` mid-task captures all steps finished so far — but no disk write happens per step (each write is a full-file rewrite). A failed save is reported to the adapter as a system error message — the user must know the session may be lost.
- **Manual save** — `:save [file]` or `Ctrl+S` at any time (TUI mode).
- **Load** — On startup, AlayaCore starts a new empty session unless you specify `--session` to load an existing one. A missing session file is the normal first-run case (silent). If the file exists but is corrupt or unreadable, an error message is emitted (system error message, shown by the active adapter) and a fresh session is started; an incompatible `message_version` is rejected outright (see below).
- **Auto-summarize** — When `--auto-summarize` is set to a positive threshold percentage (e.g. `--auto-summarize=65`), AlayaCore automatically triggers `:summarize` when context reaches that percentage of the limit. It is checked at the start of every prompt **and before each step of a running task**, so a single long task is compacted mid-flight rather than only between prompts: the summarize prompt is sent after the step's tool result (so the model reads it before summarizing), the compacted history replaces the conversation, and the task continues from it. A failed summarization ends the task and is reported to the adapter as a system error message — the uncompressed history is preserved rather than the request that would follow being sent (see `docs/context-tracking.md`).

Session files use a key-value frontmatter + one-frame-per-line body format. The frontmatter uses `---` delimiters with simple `key: value` lines (parsed by `config.ParseKeyValue`). The body contains the conversation (messages, tool calls, tool results) written directly as frames after the frontmatter — conversation content only, so a stream's control frames (`UE`, `CE`) do not belong in it: the loader groups frames into messages by role, and a delimiter would be a boundary it cannot honor.

**History IDs are not in the file.** A body record carries a tag and its JSON body only — `HistoryID`/`Role`/`Time` are `json:"-"` on `ContentPartMeta`, and the writer serializes the frame with an empty `"id"` (`contentPartJSON(part, "")`). The id is a *stream* concern: added per frame for the adapter by `replayContentsToAdapter`, never by the writer. Loading re-issues IDs sequentially in file order (`persistence.go`: `seqID++` then `SetHistoryID(seqID)`), so a loaded session's IDs are exactly `1..N`, ascending with position, by construction. That is why `histCounter` can resume at `len(Contents)` without risking a collision, and why a numbering anomaly seen in a live session — a block numbered against the record, or IDs ordered against the record — is a property of that run's arrival order: it is never written down, and reopening the file clears it.

The frontmatter includes a `message_version` field carrying the **adapter protocol version** — the frame format adapters speak (tags, SM types, the states and the commands they carry). It is broadcast to adapters as the first `TagSystemMsg` frame on startup (`{"type":"version","data":{"message_version":17,"core_version":"<build-time version>"}}`), so an out-of-process adapter (a `--rawio` client) can validate format compatibility before processing subsequent messages. The built-in adapters are compiled from this same source, so they share the constant directly and do not act on the frame. The number moves for additive changes too (a new tag, a new state, a renamed command): an adapter that knows only the older format has no other way to learn that the newer one exists.

Loading demands the number **exactly** (`parseSessionMeta`): a file one generation behind does not open, by design — the number says which vocabulary and framing wrote it, and a reader that guessed would be reading a file it was never written for. The loader never migrates; `internal/sessionmigrate` carries one step per generation that moved body bytes, each with its own script (`misc/session_upgrade_v14.sh`,
`misc/session_upgrade_v15.sh`), because the core reads none of the older generations and a tool that carries a copy of the old framing is what can convert one. A generation that moved no body bytes gets no step and no script: v16 renamed a command and an SM type, neither of which a file carries, so its whole upgrade is the `message_version` line — an edit, not a conversion, in a file that is plain text. The next generation that only renames wire vocabulary inherits that rule — v17 is one: the command plane's field names, its error codes and what it refuses are wire-only, so a v16 file is upgraded by editing that line. Both scripts recognize a file newer than themselves and say so, which is what a user arriving at such a generation should see instead of an error.

**Message grouping on load:** The session format stores a flat sequence of frames with no explicit message boundaries. On load, frames are grouped into messages by role: consecutive frames with the same role are merged into a single message's `Content` array. This correctly handles multi-part user messages (e.g., when a user adds context after a failed prompt) and assistant messages containing reasoning + text + tool calls.

### Agent Layer (`internal/llm/`)

The agent layer handles LLM interaction and tool-calling orchestration.

| Component | Description |
|-----------|-------------|
| `Agent` | Tool-calling loop orchestration with configurable max steps |
| `Provider` interface | Streaming LLM abstraction — `StreamMessages` returns an event iterator the agent consumes |
| `Factory` | Creates the correct provider based on `protocol_type` |
| `Providers` | Anthropic and OpenAI implementations |
| `TypedExecute` | Type-safe tool execution via Go generics |
| `GenerateSchema` | Auto-generates JSON schemas from struct tags |

**Key pattern — Callback Streaming:**

```go
Agent.Stream(ctx, contents, llm.StreamCallbacks{
	OnTextDelta:         func(delta string, historyID uint64) error { ... },
	OnReasoningDelta:    func(delta string, historyID uint64) error { ... },
	OnToolInputStart:    func(toolCallID, name string, historyID uint64) error { ... },
	OnToolInputComplete: func(toolCallID string, input json.RawMessage, historyID uint64) error { ... },
	OnToolOutput:        func(toolCallID string, contents []ContentPart, err error, historyID uint64) error { ... },
	OnToolConfirm:       func(req llm.ToolConfirmRequest) <-chan bool { ... },
	ToolNeedsConfirm:    func(name string) bool { ... },
	OnStepStart:         func(step int) error { ... },
	OnStepFinish:        func(contents []ContentPart, usage Usage) error { ... },
	IDGen:               func() uint64 { ... },
})
```

Messages are appended incrementally in `OnStepFinish` so they're preserved even if the user cancels. On a canceled or failed step, the results of tools that already executed are also folded into history via `OnStepFinish` (the salvage path), so side effects stay visible to a retry or `:continue` — see [tool-execution.md](tool-execution.md).

### Tools Layer (`internal/tools/`)

| Tool | Description | Safety | Dependency |
|------|-------------|--------|------------|
| `read_file` | Read file contents with optional line ranges. 64KB max for full reads (truncates at line boundary with metadata; the cap also holds when the first line alone is larger). Individual lines are capped at 1MB and marked truncated, so files made of very long lines stay readable. An empty range says why (past EOF vs. empty file). | Safe | — |
| `edit_file` | Search/replace edits on existing files. Atomic (temp file + rename), writing through a symlink to the real file and preserving its mode. Needs write permission on the containing directory (the rename requires the sibling temp file); unlike `write_file` it has no in-place fallback, because truncating the target would destroy the source it is still reading. | Medium | — |
| `write_file` | Create or replace files. Atomic (temp file + rename), so an interrupted write cannot leave a half-written target; writing empty content truncates. Preserves an existing file's mode. | Dangerous | — |
| `execute_command` | Execute commands in the detected shell (cross-platform). Runs in the process's working directory unless the optional `workdir` argument names another — absolute or relative to that directory, and it must exist and be a directory, or the call fails before the command starts. It applies to that call only. Large output (>64KB) saved to a temp file under `os.TempDir()/alayacore-<suffix>/cmd-*.txt`; only file path and metadata returned. Streams are spilled to disk past the 64KB in-memory budget, so unbounded output cannot grow the process. Streams live output previews (`Uf`) while running. | Most Dangerous | — |
| `search_content` | Search file contents using ripgrep (`rg`). Results exceeding `max_lines` (0 = no limit) or 64KB saved to a temp file under `os.TempDir()/alayacore-<suffix>/search-*.txt`; only match count and file path returned. Streams live match previews (`Uf`) while searching; runs under the same global timeout as `execute_command`. | Safe | Requires `rg` binary on system (a missing binary is reported with an actionable message) |

Each tool is implemented with type-safe input structs and auto-generated JSON schemas. All tools accept a `context.Context` parameter and respect cancellation — `:cancel` will interrupt long-running tool execution. See [schema-improvements.md](internal/schema-improvements.md) for the pattern.

Built-in tools are controlled via the `--builtin-tools` flag:
- **Not specified** (default): all five built-in tools are available.
- **Empty** (`--builtin-tools=`): no built-in tools are available (the agent relies solely on MCP tools).
- **List** (`--builtin-tools=read_file,write_file`): only the specified tools are available.

The system prompt always includes guidance to use search tools before reading files, as this applies regardless of whether the search is done via the built-in `search_content` or an MCP-provided search tool.

#### Shell Detection (`internal/tools/shell/`)

The `execute_command` tool uses a cross-platform shell detection system. On startup, it probes the OS environment for an available shell and selects the best candidate.

**Detection order:**

1. `ALAYACORE_SHELL` environment variable (matched against known shells; unknown values are ignored)
2. OS-specific `knownShells` list tried in preference order (guaranteed to succeed — `sh` on Unix, `cmd` on Windows)

**Supported shells:**

| Shell | Binary | OS | Invocation | Notes |
|-------|--------|----|------------|-------|
| Bash | `bash` | Unix | `bash -c <cmd>` | Preferred on Unix; LLMs naturally write bash syntax |
| Zsh | `zsh` | Unix | `zsh -c <cmd>` | Second choice on Unix |
| POSIX sh | `sh` | Unix | `sh -c <cmd>` | Guaranteed on all POSIX systems |
| PowerShell Core | `pwsh` | Windows | `pwsh -NoLogo -NonInteractive -Command <cmd>` | Preferred on Windows |
| Windows PowerShell | `powershell` | Windows | `powershell -NoLogo -NonInteractive -Command <cmd>` | Ships with Windows |
| cmd | `cmd` | Windows | `cmd /c <cmd>` | Guaranteed on all Windows machines |

The tool description (shown to the LLM) is dynamically generated based on the detected shell so the LLM uses the correct syntax. Platform-specific process isolation is handled per-OS:

- **Unix**: `setsid` creates a new session; `SIGINT` → `SIGKILL` for cancellation
- **Windows**: `CREATE_NO_WINDOW` isolates the child; `process.Kill()` for cancellation

The package uses Go build tags (`//go:build !windows` / `//go:build windows`) for all OS-specific code.

## Cross-Platform Architecture

AlayaCore uses Go build tags for all OS-specific code. The only platform-dependent subsystem is shell execution, isolated in the `internal/tools/shell/` package:

| File | Build tag | Provides |
|------|-----------|----------|
| `shell.go` | *(all)* | `Shell` type, `Detect()`, `detect()` |
| `shell_unix.go` | `!windows` | Unix shell defs (`bash`, `zsh`, `sh`), `knownShells` |
| `shell_windows.go` | `windows` | Windows shell defs (`pwsh`, `powershell`, `cmd`), `knownShells` |
| `exec_unix.go` | `!windows` | `SetDetachFlags` (setsid), `OpenDevNull` (/dev/null) |
| `exec_windows.go` | `windows` | `SetDetachFlags` (CREATE_NO_WINDOW + CREATE_NEW_PROCESS_GROUP), `OpenDevNull` (NUL) |
| `terminate_unix.go` | `!windows` | `Job` (no-op), `AssignJob` (no-op), `ClearJob` (no-op), `SignalProcessGroup` (SIGINT; SIGKILL follow-up via `exec.Cmd.WaitDelay`) |
| `terminate_windows.go` | `windows` | `Job` type, `AssignJob`, `ClearJob`, `SignalProcessGroup` (Job Object → `taskkill /F /T` → `Kill`) |

All other packages (LLM providers, session management, frame protocol, skills, schema generation) are pure Go with no OS-specific code.

## System Prompt Architecture

The system prompt is sent as **separate messages** (not a single concatenated string):

```
System Message 1: Default Prompt (identity + rules + search preferences
                  + tool-call ordering contract)
                  + Skills section (only when skills configured)
                  + Current working directory

System Message 2: Extra System Prompt (from --system flag, repeatable)
```

The default prompt states one rule about tool calls: calls made together may run
concurrently and in any order, so a call that needs another call's result belongs
in a later step. It is stated without naming a mode, because there is only one
prompt string for every `model.conf` and it is not rebuilt when the user switches
models — a promise written for `serial_tool_calls: true` would be false the
moment the session moved to a model that does not set it. See
[tool-execution.md](tool-execution.md#writes-exclude-all-access-and-order-nothing).

The search-before-read instruction is unconditional: it sits in
`systemPromptBase`, so it is sent whether or not `rg` is installed and whether or
not `search_content` survives `--builtin-tools` — deliberately, since the
guidance is written for any search tool, an MCP-provided one included (see the
note under [Tools Layer](#tools-layer-internaltools) for the tool-registration
side). Nothing at startup probes for `rg`: `search_content` is registered like
every other built-in (`BuiltinTools` in `internal/tools/tools.go`), and a
missing binary surfaces as that tool's own error, which names it and points at
`execute_command` with `grep`/`find` instead.

When skill container paths are provided via `--skill` and skills are discovered, the prompt includes instructions for reading skill `SKILL.md` files from their `<location>` — and for running a skill's own relative paths with `execute_command`'s `workdir` set to the folder holding that file — followed by an `<available_skills>` XML fragment listing each skill's name, description, and location. Both are omitted entirely when no skills are configured. The three values come from files that may have come from anywhere, so each is folded to a single line and XML-escaped as it is written: the fragment stays well-formed no matter what a manifest contains.

Both providers (`openai`, `anthropic`) send these as independent system
messages — the extra one only when `--system` gave something to send. They are
kept separate rather than concatenated so the LLM API can cache them
independently.

### Current Working Directory

The current working directory is appended to the end of System Message 1. This
placement has two benefits:

1. **Absolute path anchoring** — LLMs use the CWD to construct correct absolute
   paths from the first tool call onward, rather than guessing or assembling
   paths incorrectly. Empirical testing shows that without the CWD, LLMs still
   use absolute paths but occasionally construct them with the wrong base
   directory, wasting steps.

2. **Cache reuse** — The stable portion of the system prompt (identity, rules,
   skills) remains identical across sessions and can be served from the API
   cache. Only the CWD suffix changes between projects, minimizing cache misses.

The CWD is **not** persisted in the session file. On session load, it is
rebuilt from the current runtime environment, ensuring the LLM always sees the
correct base directory for the current session.

## Design Decisions

1. **Frame Protocol** — A simple line protocol enforces strict separation between adapters and session. The TUI, plain-IO, and raw-IO modes all share the same session/agent logic. No adapter may call agent functions directly — all communication goes through frames.
2. **Virtual Scrolling** — Only visible windows are rendered. See [virtual-rendering-performance.md](internal/virtual-rendering-performance.md).
3. **Typed Tools** — `TypedExecute[T]` wrapper for type-safe tool implementations with auto-generated schemas. See [schema-improvements.md](internal/schema-improvements.md).
4. **Lazy Agent Init** — Agent and provider are created on first use, not at startup.
5. **Tool Execution** — Tools execute concurrently during streaming. Tools needing confirmation block on per-tool channels until the user responds (MCP-style). See [tool-execution.md](tool-execution.md).
6. **Context Efficiency** — Large outputs (>64KB) saved to `os.TempDir()/alayacore-<suffix>/` instead of inline. See [truncation.md](truncation.md).
7. **Reasoning Mode** — Provider-specific thinking fields added to API requests. Three levels: 0=off, 1=normal, 2=max. Toggled via `:reasoning_level_set <0|1|2>`; the startup level is set via `--reasoning-level <0|1|2>`, which wins over the session file's saved `reasoning_level` (an absent flag restores the saved value).
8. **Concurrent Task Execution** — Each task runs in its own goroutine so the main loop stays responsive during LLM streaming. Communication via typed channels and atomic fields.
9. **Filter-What-You-See** — Searchable list components (ModelSelector, HelpOverlay, AttachmentOverlay) build a pre-computed, lowercased search key for each item at load time. Filtering is a single `FuzzyMatch(term, key)` against this pre-computed string, ensuring zero per-filter allocations and consistent matching with what the user sees on screen (e.g. typing "quitexit" matches `:quit` + `Exit application`).

