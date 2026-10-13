# Adapter Guide

This document is a **dual-purpose reference**: it describes the frame protocol
used by `alayacore --rawio` for **human adapter developers**, and it is also
designed to be read by **AI agents** that need to understand the protocol
in order to generate or update adapter implementations.

The `wire-samples/` subdirectory contains one sample file per frame it
documents, each holding that frame as it appears on the wire. When updating this
guide, maintain the structured, unambiguous style so that both human and AI
readers can rely on it as the single source of truth.

## Wire Format

One frame per line:

```
[2-byte tag][value]\n
```

The tag is two ASCII letters. The value is a JSON object, except for the control
frames `UE` and `CE`, whose line is the tag alone. Nothing else follows the value
on the line, and there are no separator lines: every line is a frame.

Two consequences shape everything below.

**A value is never more than one line.** A raw LF or CR inside a value would end
the frame early, so it is not allowed, and a writer that marshals JSON with any
standard library gets that for free (`\n` is escaped). Skip the library and the
reader reports the fragments as malformed rather than reading them as frames —
no line of a multi-line JSON object begins with two ASCII letters.

**A malformed frame ends the stream.** The reader does not skip it and does not
resynchronise: a frame that is not a frame means the writer lost its place, and
guessing where it meant to be is how a client misreads everything after it. The
reader reports the line — a blank one, a tag that is not two letters, a value
that is not a JSON object — and the session stops. For the same reason a stream
that ends mid-frame is an error rather than a frame: a truncated message must not
be read as a complete one.

## Protocol Version

The first `SM` frame of a session is the version frame:

```
SM {"type":"version","data":{"message_version":15,"core_version":"<build-time version>"}}
```

`message_version` identifies the wire format described in this document — the
tags, the SM types, and the states those carry. It is the number to check before
parsing anything else, because a core speaking a different version may use tags
this document does not define, and an older client has no other way to learn that
a newer core is there.

It moves for additive changes too. A new tag, a new state, a new command: each is
something an adapter may start relying on, and the number is where it finds out
that it can.

`core_version` is the alayacore build, for reporting.

## Tags

Every frame's value is either a zero-length control payload (`UE`, `CE`) or a
JSON object. The two-character tag names the frame's **plane**, not the concrete
content kind — the kind travels inside the JSON as a content item's `"type"`.
The vocabulary is a 2x2 over {into the model (`U`), out of the model (`A`)} x
{content (`X`), function (`F`)}:

```
UX  ↔   content    User content: JSON content item {"type":"..."} (both directions)
AX  ←   content    Assistant content: JSON content item, complete/authoritative
Ax  ←   content    Assistant content delta: JSON content item, fragment
AF  ←   function   Function/tool call (JSON: {"call_id":...,"name":...,"input":...})
Af  ←   function   Function/tool argument delta (JSON: {"call_id":...,"delta":...})
UF  ←   function   Function/tool result (JSON: {"call_id":...,"output":[...],"is_error":...})
Uf  ←   function   Function/tool result preview (ephemeral, display-only)
UE  →   control    User message end — flushes staged content as a single message
CI  →   command    Command input (JSON CmdMsg: {"call_id":...,"name":...,"input":...})
CE  →   control    Input end — no more prompts. Not a closed stream: commands may still follow
CO  ←   command    Command output (JSON CmdResultMsg: {"call_id":...,"output":...,"is_error":...})
SM  ←   system     System message (JSON: {"type":"...","data":{...}})
```

A content item is `{"type":...}` plus that kind's payload:

```
{"type":"text","text":"..."}                       {"type":"image","uri":"..."}
{"type":"reasoning","text":"..."}                  {"type":"audio","uri":"..."}
{"type":"video","uri":"...","fps":5,"res":1}       {"type":"document","uri":"..."}
```

The same shape is used at the top level and inside a tool result's `output`
array. `fps`/`res` are a video's own settings: they are fixed when the video
enters the conversation, so a later `:video_config` never rewrites it.

**History id and time.** Output content and function frames carry the history id
as the JSON top-level `"id"`, and authoritative frames also carry `"time"`
(RFC3339, the message time). Deltas carry `"id"` but not `"time"`. Input frames
carry neither — the core assigns the id and stamps the time on ingest. There is
no NUL-delimited prefix any more: `"id"` is an ordinary field.

## Tag Naming Convention

Each tag is two characters: **side** + **kind**.

| First letter | Side | Tags |
|---|---|---|
| `U` | into the model (user content + tool results) | UX, UF, Uf |
| `A` | out of the model (assistant content + tool calls) | AX, Ax, AF, Af |
| `C` | **C**ommand (control plane) | CI, CO, CE |
| `S` | **S**ystem | SM |

| Second letter | Kind | Tags |
|---|---|---|
| `X` | content (kind in the JSON `"type"`) | UX, AX, Ax |
| `F` | **F**unction/tool | AF, UF, Af, Uf |
| `E` | **E**nd (flush / input end) | UE, CE |
| `I` / `O` | command **I**nput / **O**utput | CI, CO |
| `M` | **M**essage | SM |

**Case convention:** Uppercase tags carry complete/authoritative content;
lowercase tags carry streaming delta content:
- `Ax` — assistant content delta (text or reasoning, per the item's `"type"`), appended to the window per chunk
- `AX` — complete assistant content, sent after all deltas for that block (content is empty in delta mode; it carries the full text during replay or `--no-delta` mode)
- `Af` — tool argument delta, a partial JSON chunk during streaming
- `Uf` — tool result preview snapshot (ephemeral, display-only; the
  authoritative result always arrives via `UF`, which overwrites it)
- `AF` — complete tool call (start + input frames)
- `UX` has no delta: user content never streams.

The side indicates **which side of the model** the content belongs to, not the
direction of the message. A tool result (`UF`) goes *into* the model but appears
on stdout; `UX` on stdout is the echo of user content sent back to the adapter.

**Control-plane exception:** The `C` (Command) tags are not conversation
content. `I`/`O` encode **direction**: `CI` is sent by the adapter on stdin,
`CO` by the agent on stdout. They never appear on both sides.

**Important:** `UX` appears on **both** stdin and stdout:

| Direction | Meaning |
|-----------|---------|
| **stdin** (adapter → agent) | New user content — the adapter sends a user message to the agent |
| **stdout** (agent → adapter) | User content echo — the agent echoes the user's message back to the adapter for display, with a history id assigned |

The adapter must be prepared to **receive** `UX` on stdout in these scenarios:

1. **Prompt echo** — When the user sends a prompt (`UX` + `UE` on stdin), the agent echoes each content item back on stdout with an assigned history id before sending them to the LLM.
2. **Session replay** — When a saved session file (key-value frontmatter + frame body, specified via `--session`) is loaded, all historical content (including user messages) is replayed to the adapter on stdout with their original history ids.
3. **The agent's own user turns** — Not every `UX` frame is something the user typed: the `Continue` that resumes a cancelled turn (`:continue`) and the one a mid-task compaction appends are written by the agent. They are parts of the conversation like any other, so they are echoed like any other.
4. **Steering** — A prompt sent while a task is running is spliced into that turn at its next step boundary (or becomes the next prompt if the turn ends first), so its echo (with its history id) arrives when the model receives it — one step later, not immediately. The session reports this lifecycle as an SM `steering` message carrying a `phase` of `queued`, `delivered`, `dropped`, or `refused`. The echo is the authoritative record — the words enter `Contents` only at delivery.

> **For adapter implementors:** You cannot assume user content only appears on stdin.
> The terminal adapter (`internal/adapters/terminal/output.go`), the plainio
> adapter (`internal/adapters/plainio/output.go`), and the terseio adapter
> (`internal/adapters/terseio/output.go`, which suppresses them — its stdout
> carries only the final answer) all receive user content from stdout.

## Delta Messages (Ax, Af)

Streaming content uses **lowercase** tags (`Ax`, `Af`). Each delta body carries
the history id and the fragment:

```
Ax ← stdout   {"id":"4","type":"text","text":"Here's what "}
Af ← stdout   {"id":"2","call_id":"t1","delta":"{\"path\":"}
```

The same history id can appear multiple times — each subsequent frame is a
continuation (delta) of that content block. The adapter concatenates them.

> **Uf is lowercase but not a continuation:** the tool result preview
> (`Uf`) is a **snapshot**, not a delta — each frame replaces the previous
> preview and frames are never concatenated. See [Function Lifecycle](#function-lifecycle-af-uf).

**Complete/authoritative frames** use **uppercase** tags (`AX`). These are
sent after all deltas for a content block have been received. The adapter may
choose either approach:
- If it has accumulated deltas (`Ax`), it can **ignore** the uppercase frame
  (deltas already represent the complete content).
- Alternatively, it can **use** the uppercase frame as the authoritative source,
  replacing any delta-accumulated content.

During replay (session load) only the uppercase frames are sent; no deltas
precede them — the adapter must use the uppercase frame to display the content.

**`--no-delta` mode:** When the `--no-delta` flag is set, no lowercase delta
frames (`Ax`, `Af`, `Uf`) are sent. Instead, the uppercase frames (`AX`, `AF`)
carry the complete content directly. This mode is useful for adapters that
prefer batch delivery over streaming, or for environments where the overhead
of many small frames is undesirable.

| Mode | Ax | Af | Uf | AX content | AF content |
|------|-------|----|----|---------------|------------|
| Default (deltas on) | Sent | Sent | Sent (ephemeral preview) | Empty (terminator only) | Repaired JSON (always complete) |
| `--no-delta` | Not sent | Not sent | Not sent | Full text | Repaired JSON (always complete) |

**History id format:** a flat monotonic counter, rendered as a JSON string (e.g.
`"1"`, `"2"`, `"3"`). Each content block (text, reasoning, tool call, user
content item) receives a unique id from this counter, and it is present on every
output content/function frame.

## User Echo on stdout (UX)

User echoes are **not** delta — each frame contains **complete content** (the full
text string, the full data URI or URL). There is no streaming/continuation for
user echo frames; each frame is a standalone content item.

When a user sends multiple items in one prompt (e.g. image + text), the agent
echoes them as consecutive `UX` frames on stdout. The adapter groups them into a
single user message. Other tag types use different grouping mechanisms:

| Tags | Grouping mechanism |
|------|-------------------|
| UX (stdout) | **Position** — consecutive frames belong to one message; a non-`UX` tag starts the next |
| Ax | **History id** — same id = same stream (delta concatenation) |
| Af | **JSON `"call_id"`** — partial JSON chunk for the named tool call |
| AF | **History id** (start+input share the same id) + **JSON `"call_id"`** — start announces name, input carries arguments |
| UF | **JSON `"call_id"`** — matches the corresponding AF |
| Uf | **JSON `"call_id"`** — snapshot preview for the named tool call (not concatenated; UF overwrites) |
| SM | **None** — each frame is standalone |

Each user content item gets its own **unique** history id (monotonic counter).

```
Adapter writes → stdin:       UX {"type":"image","uri":"data:image/jpeg;base64,..."}
                               UX {"type":"image","uri":"data:image/png;base64,..."}
                               UX {"type":"text","text":"What's in these images?"}
                               UE
Session writes → stdout:      UX {"id":"1","time":"...","type":"image","uri":"data:image/jpeg;base64,..."}
                               UX {"id":"2","time":"...","type":"image","uri":"data:image/png;base64,..."}  ← different id
                               UX {"id":"3","time":"...","type":"text","text":"What's in these images?"}   ← different id
                               AX {"id":"4","time":"...","type":"text","text":"These images show..."}     ← starts assistant message
```

## Function Lifecycle (AF, UF)

A tool call and its result are a **pair**, correlated by `call_id` — not by the
history id, which is present only to place the frame in the transcript. They can
run in parallel, interleave, stream half-way, and be reordered by id.

**AF** — function lifecycle (two or three frames live, one frame on replay):
- `{"id":"2","time":"...","call_id":"t1","name":"read_file"}` — tool name announced (start frame, no input yet)
- `{"id":"2","call_id":"t1","delta":"{\"path\":"}` — partial JSON argument (an `Af` frame, zero or more during streaming)
- `{"id":"2","time":"...","call_id":"t1","input":{...}}` — full tool arguments (input frame, name already known, same history id as start)

During live streaming `OnToolInputStart` → `AF` start, `OnToolInputDelta` → `Af` (zero or more),
and `OnToolInputComplete` → `AF` input share the same history id (they belong to the same tool call).
During session replay the tool call is a single AF frame with both `name` and `input`.

**UF** — function result:
- `{"id":"3","time":"...","call_id":"t1","output":[...]}` — succeeded (`is_error` omitted when `false`)
- `{"id":"3","time":"...","call_id":"t1","output":[...],"is_error":true}` — failed

**Uf** — ephemeral result preview (display only, optional):
- `{"id":"2","call_id":"t1","text":" 42%"}` — live snapshot of the tool result while it runs
- `text` is a single line: the current line of the most recently written
  stream (stdout or stderr), with the other stream as fallback
- Sent only when the tool supports streaming (e.g. `execute_command`,
  `search_content`); zero or more frames, coalesced at ~100ms. **Never
  authoritative**: adapters may drop or ignore it, and the final `UF` frame
  always overwrites any preview content.

A tool call (AF) without a matching UF is still in progress. Each `.txt` sample below shows one frame in this lifecycle.

## Example: Text Prompt Flow (with streaming deltas)

```
Adapter writes → stdin:       UX {"type":"text","text":"Read the file main.go"}
                               UE
Session writes → stdout:      UX {"id":"1","time":"...","type":"text","text":"Read the file main.go"}  ← echo
                               AF {"id":"2","time":"...","call_id":"t1","name":"read_file"}          ← non-UX tag flushes user msg
                               Af {"id":"2","call_id":"t1","delta":"{\"path\":"}                    ← partial arg delta
                               Af {"id":"2","call_id":"t1","delta":"\"main.go\"}"}                  ← more args
                               AF {"id":"2","time":"...","call_id":"t1","input":{"path":"main.go"}} ← complete args
                               Uf {"id":"2","call_id":"t1","text":"reading main.go..."}             ← ephemeral result preview (optional)
                               UF {"id":"3","time":"...","call_id":"t1","output":[{"type":"text","text":"package main..."}]}
                               Ax {"id":"4","type":"text","text":"Here's what main.go does..."}     ← streaming text delta
                               AX {"id":"4","time":"...","type":"text"}                             ← authoritative complete (empty terminator)
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Text Prompt Flow (replay, no deltas)

```
Session writes → stdout:      UX {"id":"1","time":"...","type":"text","text":"Read the file main.go"}
                               AF {"id":"2","time":"...","call_id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF {"id":"3","time":"...","call_id":"t1","output":[{"type":"text","text":"package main..."}]}
                               AX {"id":"4","time":"...","type":"text","text":"Here's what main.go does..."}
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Image Prompt Flow

```
Adapter writes → stdin:       UX {"type":"image","uri":"data:image/jpeg;base64,..."}
                               UX {"type":"image","uri":"data:image/png;base64,..."}
                               UX {"type":"text","text":"What's in these images?"}
                               UE
Session writes → stdout:      UX {"id":"1","time":"...","type":"image","uri":"data:image/jpeg;base64,..."}
                               UX {"id":"2","time":"...","type":"image","uri":"data:image/png;base64,..."}
                               UX {"id":"3","time":"...","type":"text","text":"What's in these images?"}
                               AX {"id":"4","time":"...","type":"text","text":"These images contain..."}
```

The three user frames (ids 1, 2, 3) are accumulated into one user message.
The `AX` frame triggers the flush and begins the assistant response.

## Example: Media Prompt Flow

```
Adapter writes → stdin:       UX {"type":"image","uri":"data:image/jpeg;base64,..."}   (or URL)
                               UX {"type":"audio","uri":"data:audio/mpeg;base64,..."}   (or URL)
                               UX {"type":"video","uri":"data:video/mp4;base64,...","fps":5,"res":1}  (or URL)
                               UX {"type":"document","uri":"data:application/pdf;base64,..."}  (or URL)
                               UX {"type":"text","text":"Analyze these files"}          (optional — media-only is valid)
                               UE
Session writes → stdout:      UX {"id":"1","time":"...","type":"image","uri":"..."}
                               UX {"id":"2","time":"...","type":"audio","uri":"..."}
                               UX {"id":"3","time":"...","type":"video","uri":"...","fps":5,"res":1}
                               UX {"id":"4","time":"...","type":"document","uri":"..."}
                               UX {"id":"5","time":"...","type":"text","text":"Analyze these files"}  (absent for media-only)
                               AX {"id":"6","time":"...","type":"text","text":"Here's my analysis..."}
```

All media types accept either `data:` URIs or plain URLs. Media frames and text
can be combined in any order. The adapter accumulates them all until the first
non-`UX` tag. A video may carry its own `fps`/`res`; if it does not, the core
stamps the session default (from `:video_config`) when the video is ingested.

## Example: Session Replay Flow

When a saved session is loaded, all previous content is replayed on stdout.
Each content part gets a sequential history id (rebuilt from `seqID++`).
Startup system-info frames (version, task, model, theme, reasoning,
video_config) are broadcast first, then the replayed content, and finally —
once initialization completes — the session-ready frame:

```
Session writes → stdout:      SM {"type":"task","data":{"in_progress":false,"context":1500}}  ← startup broadcast (before replay)
                               UX {"id":"1","time":"...","type":"text","text":"Hello"}       ← user
                               AX {"id":"2","time":"...","type":"text","text":"Hi! How can I help?"}  ← assistant
                               UX {"id":"3","time":"...","type":"text","text":"Read main.go"} ← user
                               AF {"id":"4","time":"...","call_id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF {"id":"5","time":"...","call_id":"t1","output":[...]}
```

Note: During replay, a tool call is a single AF frame with both `name` and
`input` together (one `ToolInputPart`). The two-frame split (start then input)
only happens during live streaming. User and assistant frames are interleaved
as they were in the original conversation.

### Session lifecycle signal

The session reports its lifecycle in `session` system messages — exactly one
per transition, so a client that waits for a frame is never left guessing:

```
SM {"type":"session","data":{"state":"ready"}}   ← ready to accept prompts
SM {"type":"session","data":{"state":"closed"}}  ← the session is over
```

**`ready`** — sent **once per session**, when the session becomes ready to
accept prompts — i.e. after session load + replay (always synchronous, before
startup) and after MCP init has settled (done / canceled / aborted, or never
configured).

- Ordering: with MCP configured it arrives after the final `mcp` frame
  (`done` or `canceled`); without MCP it arrives immediately after the
  replayed content / startup broadcast.
- This is the **authoritative** readiness signal. Clients that wait for
  initialization (spinner, blocked input box, "waiting for MCP" states)
  should wait for this frame instead of inferring readiness from `mcp`
  frames or the startup `task` frame.
- A prompt that arrives **before** this frame is accepted and held: the session
  runs it the moment it becomes ready. A client does not have to wait for the
  frame before prompting — which matters for one that has already reached the
  end of its input, because it has no way to send the prompt again.

**`closed`** — sent **once**, as the session's last act: after every other
frame, including the output of the last task. It is the terminal frame, and
nothing follows it on stdout. A client that needs to know the session has
ended — rather than inferring it from EOF on stdout — waits for this frame;
see [Delivery guarantee](#delivery-guarantee) for how it relates to `ready`.

Adapters that don't recognize the `session` type ignore it (unknown SM types
are skipped), so older adapters are unaffected.

#### Delivery guarantee

`ready` and `closed` are both **always** delivered exactly once each, and a
session that ends before it was ever ready still sends `closed`:

| MCP outcome | Path to the ready frame |
|-------------|------------------------|
| Not configured | Broadcast at the start of the session loop |
| Initialized (`done`) | After the final `mcp` `done` frame |
| User canceled (`:mcp_cancel`) | After the `mcp` `done` frame that follows cancellation |
| Server failure (`failed`) | After the final `mcp` `done` frame — init always ends with a `done` event, even when some or all servers failed |
| Event stream closed abnormally | After the "MCP initialization canceled" error frame |

This is guaranteed by three layers in the core:

1. The MCP initializer **always closes** its event stream on exit (success,
   failure, cancellation, or context cancellation — via `defer`).
2. A closed stream still delivers already-queued events (buffered channel
   semantics), so a final `done`/`canceled` event is never lost.
3. If the stream closes without a clean event, the session falls back to
   `aborted` — which still transitions to `ready`, so the adapter is never
   left waiting.

The ready frame is not sent when the session itself terminates (input stream
closed or session canceled) **before** MCP init settles — but that case gets
the `closed` frame instead, so a client waiting for either signal still hears
back. A client that only knows about `ready` should treat `closed` (or EOF)
as the end of the protocol; the session is gone either way, and no prompt it
sent can be answered.

## Adapter Implementation Notes

### Handling user tags on stdout

1. **Parse the history id** from the JSON top-level `"id"` field of a content or
   function frame. There is no NUL-delimited prefix any more.

2. **Group by position:** consecutive `UX` frames belong to one user message.
   When a non-`UX` tag arrives (AX, Ax, AF, Af, UF, Uf, SM), it starts the next
   message. There is no `UE` on stdout.

3. **Display:** Since there is no UE on stdout, the adapter cannot know if
   more user frames are coming until a non-user tag arrives. Render each
   frame incrementally as it arrives (e.g. the TUI adapter calls
   `SetWindowVisible` + `dirty.Store(true)` on every frame, then finalizes
   the window metadata when a non-user tag arrives).

### Handling history IDs

All stdout content and function frames (AX, Ax, AF, Af, UF, Uf, and `UX` echoes)
carry the history id as the JSON top-level `"id"`. It is a flat monotonic
counter that increases over the session lifetime. System messages (SM),
command frames (CI/CO), and stdin frames never carry it.

The **semantics** of the history ID differ by tag type:

| Tag | Same history id means | Grouping / matching |
|-----|-----------------------|---------------------|
| Ax | **Content continuation** (delta) — concatenate frames with the same id into one block | History id |
| AF (start + input) | **Same tool call** — start frame announces the name, input frame carries arguments (not concatenated) | JSON `"call_id"` + history id |
| UF | **Same tool result** (matched to AF by `call_id`, not by history id) | JSON `"call_id"` |
| Uf | **Same tool result preview** (matched to AF by `call_id`; snapshot, not concatenated) | JSON `"call_id"` |
| UX (stdout) | **N/A** — each echo has a unique history id | **Position** (consecutive `UX` frames → one message) |

**Key rules:**
- Different history id → different content block (all tags)
- No `"id"` field → the frame carries no history id (SM, CI/CO, and all stdin frames)
- History ids are ephemeral: rebuilt from `seqID++` on session load, not persisted
- On session replay, every content and function frame (AX, AF, UF, and `UX` echoes)
  carries a history id, matching the format it had during live streaming

### Summarize (`--auto-summarize` and `:summarize`)

Compaction replaces the conversation, and no frame announces the replacement, so
after one the transcript an adapter has accumulated is not the session's
`Contents`. Three things to know:

- **The summary arrives as ordinary assistant text** (Ax deltas, then AX), like
  any other model output — it is produced by an ordinary request.
- **Its frame's history ID is not the stored part's ID.** The ID is assigned
  while streaming, and the compacted history then carries the summary as a fresh
  part with a fresh ID. A window keyed by the frame's ID names a part no
  `Contents` holds, and `:fork` on it answers `NOT_FOUND`.
- **The user turn that opens the compacted history is not sent as a frame.** The
  history opens on a user turn (the summary request); it is never echoed, so live
  the summary appears with no user turn in front of it, and a reload shows it.

### Error handling

1. **Corrupt session file**: If a content part in a saved session cannot be
   deserialized (unknown tag, malformed JSON), the session load fails and the
   agent sends an error message like:
   ```
   SM {"type":"error","data":{"text":"corrupt session file: failed to serialize content part (HistoryID=5): unexpected end of JSON input"}}
   ```
   The adapter should display the error and stop sending new prompts — no
   further frames will be processed.

2. **Model config errors**: At startup, model configuration errors (invalid
   fields, duplicate names, etc.) are sent to the adapter as system error
   messages:
   ```
   SM {"type":"error","data":{"text":"model \"Bad Model\": unknown protocol_type \"foobar\" — skipped"}}
   SM {"type":"error","data":{"text":"model.conf block 3: duplicate name \"Model A\" — skipped"}}
   ```
   These are informational — the session continues with whatever valid models
   are available. The adapter should display them so the user can fix their
   `model.conf`. After `:model_load` / `:model_sync`, the same validation
   errors arrive differently: the command **fails** with a CO frame
   (`code:"MODEL_VALIDATION"`, errors joined in `message`) — the user must
   see them to fix the config.

3. **MCP config errors**: At startup, MCP configuration errors (empty server
   name, duplicate server names, etc.) are sent to the adapter as system error
   messages:
   ```
   SM {"type":"error","data":{"text":"mcp.conf block 1: skipping block with empty server name"}}
   SM {"type":"error","data":{"text":"mcp.conf block 2: duplicate server name \"my-db\" — skipped"}}
   ```
   These are informational — the session continues with whatever valid MCP
   servers are available. The adapter should display them so the user can fix
   their `mcp.conf`.

4. **Session-level errors (persistence, summarization)**: Failures that put
   the conversation at risk are sent as system error messages during or
   after task execution:
   ```
   SM {"type":"error","data":{"text":"Auto-save failed: <error>"}}
   SM {"type":"error","data":{"text":"Failed to create pre-summarize backup: <error>"}}
   SM {"type":"error","data":{"text":"Auto-summarization failed: <error>"}}
   ```
   - **Auto-save** arrives after the task completes (before the final `task`
     message with `in_progress:false`) — the conversation is unaffected, but
     the session file is stale and may be lost.
   - **Pre-summarize backup / auto-summarization** arrive during the task. A
     failed auto-summarization ends the turn rather than sending the oversized
     request that would follow, and the task's completion arrives behind it. A
     failed backup does not stop the summarize. Display both: the first says
     why the turn stopped, the second that a summarize which proceeds without
     a backup leaves the original conversation unrecoverable.

5. **Output stream broken**: On the first write error to stdout, the agent
   cancels the session context and stops processing. No further frames are
   sent — not even the terminal `closed` frame, since writing it is what failed
   — so in this one case the adapter must read the end of stdout as the end of
   the session instead of waiting for that frame. Handle it gracefully (e.g.
   close the connection, show a notification).

6. **Missing UF**: A tool call (AF) without a matching UF is still in progress.
   If the session ends before all tool calls complete, pending tool calls are
   abandoned — no UF will arrive for them.

7. **History ID collision**: History IDs are assigned monotonically. During
   live streaming they come from a single counter; during replay they are
   rebuilt from `seqID++`. Collisions cannot occur under normal operation.
   If a corrupt session causes duplicate IDs, the adapter should treat each
   frame as an independent content block (AT/AR delta concatenation on same
   ID still applies).

## Samples by Tool

Each tool's samples list the stdin frames (adapter → agent) followed by
the stdout frames (agent → adapter). The first stdout frame carries the
user's prompt echo with its assigned history id. A sample's filename names the
tag it carries and the frame is the JSON body in the file.

### read_file

```
stdin:  UX-read-file.txt               UX {"type":"text","text":"Read the file main.go"}
                                       UE
stdout: UX-echo-read-file.txt          UX {"id":"5","time":"...","type":"text","text":"Read the file main.go"}
        AF-read-file-start.txt         AF {"id":"6","time":"...","call_id":"t1","name":"read_file"}
        AF-read-file-input.txt         AF {"id":"6","time":"...","call_id":"t1","input":{"path":"main.go"}}   ← or AF-read-file-input-range.txt
        UF-read-file-success.txt       UF {"id":"7","time":"...","call_id":"t1","output":[{"type":"text","text":"package main..."}]}
        UF-read-file-failed.txt        UF {"id":"7","time":"...","call_id":"t1","output":[{"type":"text","text":"file not found"}],"is_error":true}
```

The `AF-read-file-input-range.txt` sample is an **alternative** to `AF-read-file-input.txt` — it demonstrates the `start_line` and `num_lines` optional parameters. Only one input frame is sent per tool invocation.

### write_file

```
stdin:  UX-write-file.txt              UX {"type":"text","text":"Write a hello world Go program to hello.go"}
                                       UE
stdout: (echo)                         UX {"id":"9","time":"...","type":"text","text":"Write a hello world Go program to hello.go"}
        AF-write-file-start.txt        AF {"id":"10","time":"...","call_id":"t3","name":"write_file"}
        AF-write-file-input.txt        AF {"id":"10","time":"...","call_id":"t3","input":{"content":"package main","path":"main.go"}}
        UF-write-file-success.txt      UF {"id":"11","time":"...","call_id":"t3","output":[{"type":"text","text":"File written successfully"}]}
        UF-write-file-failed.txt       UF {"id":"11","time":"...","call_id":"t3","output":[{"type":"text","text":"permission denied"}],"is_error":true}
```

### edit_file

```
stdin:  UX-edit-file.txt               UX {"type":"text","text":"Edit main.go to fix the greeting"}
                                       UE
stdout: (echo)                         UX {"id":"7","time":"...","type":"text","text":"Edit main.go to fix the greeting"}
        AF-edit-file-start.txt         AF {"id":"8","time":"...","call_id":"t2","name":"edit_file"}
        AF-edit-file-input.txt         AF {"id":"8","time":"...","call_id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
        UF-edit-file-success.txt       UF {"id":"9","time":"...","call_id":"t2","output":[{"type":"text","text":"File edited successfully"}]}
        UF-edit-file-failed.txt        UF {"id":"9","time":"...","call_id":"t2","output":[{"type":"text","text":"old_string not found"}],"is_error":true}
```

### search_content

```
stdin:  UX-search-content.txt          UX {"type":"text","text":"Search for TODO in Go files"}
                                       UE
stdout: (echo)                         UX {"id":"11","time":"...","type":"text","text":"Search for TODO in Go files"}
        AF-search-content-start.txt    AF {"id":"12","time":"...","call_id":"t4","name":"search_content"}
        AF-search-content-input.txt    AF {"id":"12","time":"...","call_id":"t4","input":{"pattern":"TODO"}}
        Uf-search-content-preview.txt  Uf {"id":"12","call_id":"t4","text":"main.go:3:// TODO: fix"}   ← ephemeral preview (optional)
        UF-search-content-success.txt  UF {"id":"13","time":"...","call_id":"t4","output":[{"type":"text","text":"main.go:1:package main"}]}
        UF-search-content-failed.txt   UF {"id":"13","time":"...","call_id":"t4","output":[{"type":"text","text":"invalid regex"}],"is_error":true}
```

### execute_command

```
stdin:  UX-execute-command.txt         UX {"type":"text","text":"Run: ls -la"}
                                       UE
stdout: (echo)                         UX {"id":"13","time":"...","type":"text","text":"Run: ls -la"}
        AF-execute-command-start.txt   AF {"id":"14","time":"...","call_id":"t5","name":"execute_command"}
        AF-execute-command-input.txt   AF {"id":"14","time":"...","call_id":"t5","input":{"command":"ls -la"}}
        Uf-execute-command-preview.txt Uf {"id":"14","call_id":"t5","text":"total 42..."}   ← ephemeral preview (optional)
        UF-execute-command-success.txt UF {"id":"15","time":"...","call_id":"t5","output":[{"type":"text","text":"total 42..."}]}
        UF-execute-command-failed.txt  UF {"id":"15","time":"...","call_id":"t5","output":[{"type":"text","text":"command not found"}],"is_error":true}
```

### Text / Reasoning / System / Tool samples

Samples are grouped by direction below.

**stdin (adapter → agent, no history id, no time):**

```
UX-hello.txt                   UX {"type":"text","text":"hello"}
UX-empty.txt                   UX {"type":"text"}   (no text — an empty prompt)
UX-image.txt                   UX {"type":"image","uri":"data:image/jpeg;base64,..."}
UX-image-url.txt               UX {"type":"image","uri":"https://..."}
UX-audio.txt                   UX {"type":"audio","uri":"data:audio/mpeg;base64,..."}
UX-audio-url.txt               UX {"type":"audio","uri":"https://..."}
UX-video.txt                   UX {"type":"video","uri":"data:video/mp4;base64,...","fps":5,"res":1}
UX-video-url.txt               UX {"type":"video","uri":"https://...","fps":5,"res":1}
UX-document.txt                UX {"type":"document","uri":"data:application/pdf;base64,..."}
UE.txt                         UE "" (length 0)
CE.txt                         CE "" (length 0) — input end: no more prompts, commands may still follow
CI-cancel.txt                  CI {"call_id":"1","name":"cancel"}
CI-save.txt                    CI {"call_id":"2","name":"save","input":"/tmp/x.alaya"}
CI-model-sync.txt              CI {"call_id":"3","name":"model_sync","input":"[{id,name,protocol_type,base_url,api_key,model_name,context_limit,max_tokens},...]"}
```

**stdout — content (history id in `"id"`, time on authoritative frames):**

```
UX-echo-hello.txt              UX {"id":"1","time":"...","type":"text","text":"Hello"}
UX-echo-read-file.txt          UX {"id":"5","time":"...","type":"text","text":"Read the file main.go"}
UX-echo-image.txt              UX {"id":"2","time":"...","type":"image","uri":"data:image/jpeg;..."}
UX-echo-audio-url.txt          UX {"id":"3","time":"...","type":"audio","uri":"https://..."}
UX-echo-video-url.txt          UX {"id":"4","time":"...","type":"video","uri":"https://..."}
Ax-hello.txt                   Ax {"id":"1","type":"text","text":"Hello"}
Ax-world.txt                   Ax {"id":"1","type":"text","text":" world"} (same stream)
Ax-new-step.txt                Ax {"id":"2","type":"text","text":"Next step"} (new stream)
Ax-thinking.txt                Ax {"id":"3","type":"reasoning","text":"thinking..."}
AX-complete.txt                AX {"id":"4","time":"...","type":"text","text":"Here's what main.go does..."}   (content varies by mode)
AX-reasoning.txt               AX {"id":"3","time":"...","type":"reasoning","text":"I need to read the file first..."} (content varies by mode)
```

**stdout — tool frames (history id in `"id"`; matching by `"call_id"`):**

```
AF-read-file-start.txt         AF {"id":"6","time":"...","call_id":"t1","name":"read_file"}
AF-read-file-input.txt         AF {"id":"6","time":"...","call_id":"t1","input":{"path":"main.go"}}
AF-read-file-input-range.txt   AF {"id":"6","time":"...","call_id":"t1","input":{"path":"main.go","start_line":10,"num_lines":20}}
UF-read-file-success.txt       UF {"id":"7","time":"...","call_id":"t1","output":[{"type":"text","text":"package main..."}]}
UF-read-file-failed.txt        UF {"id":"7","time":"...","call_id":"t1","output":[{"type":"text","text":"file not found"}],"is_error":true}
AF-edit-file-start.txt         AF {"id":"8","time":"...","call_id":"t2","name":"edit_file"}
AF-edit-file-input.txt         AF {"id":"8","time":"...","call_id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
UF-edit-file-success.txt       UF {"id":"9","time":"...","call_id":"t2","output":[{"type":"text","text":"File edited successfully"}]}
UF-edit-file-failed.txt        UF {"id":"9","time":"...","call_id":"t2","output":[{"type":"text","text":"old_string not found"}],"is_error":true}
AF-write-file-start.txt        AF {"id":"10","time":"...","call_id":"t3","name":"write_file"}
AF-write-file-input.txt        AF {"id":"10","time":"...","call_id":"t3","input":{"content":"package main","path":"main.go"}}
UF-write-file-success.txt      UF {"id":"11","time":"...","call_id":"t3","output":[{"type":"text","text":"File written successfully"}]}
UF-write-file-failed.txt       UF {"id":"11","time":"...","call_id":"t3","output":[{"type":"text","text":"permission denied"}],"is_error":true}
AF-search-content-start.txt    AF {"id":"12","time":"...","call_id":"t4","name":"search_content"}
AF-search-content-input.txt    AF {"id":"12","time":"...","call_id":"t4","input":{"pattern":"TODO"}}
Uf-search-content-preview.txt  Uf {"id":"12","call_id":"t4","text":"main.go:3:// TODO: fix"}
UF-search-content-success.txt  UF {"id":"13","time":"...","call_id":"t4","output":[{"type":"text","text":"main.go:1:package main"}]}
UF-search-content-failed.txt   UF {"id":"13","time":"...","call_id":"t4","output":[{"type":"text","text":"invalid regex"}],"is_error":true}
Af-tool-delta.txt              Af {"id":"6","call_id":"t1","delta":"{\"path\":\""}
Uf-execute-command-preview.txt Uf {"id":"14","call_id":"t5","text":"total 42..."}
```

**stdout — commands and system messages (no history id):**

```
CO-save-success.txt            CO {"call_id":"2","output":{"path":"/tmp/x.alaya"}}
CO-command-error.txt           CO {"call_id":"2","is_error":true,"output":{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}}
CO-task-started.txt            CO {"call_id":"9","output":{"status":"started"}}
```

The system-message samples follow with their schema.

**stdout — system messages (no history ID, JSON `{"type":"...","data":{...}}`):**

| Type | JSON Schema (data fields) | Example `.txt` |
|------|--------------------------|----------------|
| `version` | `message_version` (int), `core_version` (string) | `SM-message-version.txt` |
| `model` | `active_id` (int), `active_name` (string), `context_limit` (int) | `SM-model.txt` |
| `model_list` | `models` (array of `{id:int, name:string, protocol_type:string, base_url:string, api_key:string, model_name:string, context_limit:int, max_tokens:int, serial_tool_calls:bool, reasoning_0/1/2:raw JSON (optional), reasoning_field:string (optional)`) | `SM-model-list.txt` |
| `theme` | TUI only (not sent in NoTheme modes: plainio/terseio/rawio). `name` (string), `theme` (object, optional — full palette sent on startup, omitted on theme switch) | `SM-theme.txt` |
| `theme_list` | TUI only (not sent in NoTheme modes: plainio/terseio/rawio). `themes` (array of `{name:string, theme:{primary, dim, muted, warning, error, added, removed: string}}`) | `SM-theme-list.txt` |
| `reasoning` | `level` (int: 0=off, 1=normal, 2=max) | `SM-reasoning.txt` |
| `video_config` | `fps` (int), `res` (int) | `SM-video-config.txt` |
| `task` | `in_progress` (bool), `current_step` (int, opt), `max_steps` (int, opt), `context` (int), `command_id` (string, opt — set when the task was started by `continue`/`summarize`), `step_tps` (float, opt — latest step's end-to-end throughput in tokens/sec: output tokens ÷ round-trip duration, latency included; absent until a step with output tokens completes; 0 for a step with no output tokens), `ttft_ms` (int, opt — latest step's time-to-first-token in ms) | `SM-task-start.txt`, `SM-task-step.txt`, `SM-task-end.txt` |
| `error` | `text` (string) | `SM-error.txt` |
| `notify` | `text` (string) | `SM-notify.txt` |
| `tool_confirm` | `call_id` (string) — one-way notification; the user's decision is returned via the `tool_confirm` / `tool_decline` commands (CI frames), not via an SM response | `SM-tool-confirm.txt` |
| `mcp` | `status` (string: one of `connecting`, `auth_required`, `auth_running`, `connected`, `failed`, `done`), `server` (string, opt), `url` (string, opt — set for `auth_required`; may contain `{{redirect_uri}}` and `{{state}}` placeholders), `error` (string, opt — set for `failed`) | `SM-mcp-connecting.txt`, `SM-mcp-auth-required.txt`, `SM-mcp-auth-running.txt`, `SM-mcp-connected.txt`, `SM-mcp-failed.txt`, `SM-mcp-done.txt` |
| `session` | `state` (string: `ready` — sent **exactly once**, when initialization completes; `closed` — sent **exactly once**, as the last frame before the session exits) | `SM-session-ready.txt`, `SM-session-closed.txt` |
| `steering` | `phase` (string: one of `queued`, `delivered`, `dropped`, `refused`), `reason` (string, opt — `canceled`, `failed` or `quit`; set only for `dropped`), `text` (string, opt — a human-readable line; `delivered` carries none) | `SM-steering-queued.txt`, `SM-steering-delivered.txt`, `SM-steering-dropped.txt`, `SM-steering-refused.txt` |

Complete wire values:

```
SM-message-version.txt         {"type":"version","data":{"message_version":15,"core_version":"(set at build time)"}}
SM-model.txt                   {"type":"model","data":{"active_id":4,"active_name":"DeepSeek / DeepSeek-V4 Flash","context_limit":1000000}}
SM-model-list.txt              {"type":"model_list","data":{"models":[{"id":0,"name":"Anthropic / Claude Haiku 4","protocol_type":"anthropic","base_url":"https://api.anthropic.com","api_key":"sk-ant-...","model_name":"claude-haiku-4-20260515","context_limit":200000,"max_tokens":0,"serial_tool_calls":false},{"id":4,"name":"DeepSeek / DeepSeek-V4 Flash","protocol_type":"openai","base_url":"https://api.deepseek.com/v1","api_key":"sk-ds-...","model_name":"deepseek-v4-flash","context_limit":1000000,"max_tokens":0,"serial_tool_calls":false}]}}
SM-theme.txt                   {"type":"theme","data":{"name":"theme-dark"}}
SM-theme-list.txt              {"type":"theme_list","data":{"themes":[{"name":"theme-dark","theme":{"primary":"#89d4fa","dim":"#313244","muted":"#6c7086","warning":"#f77923","error":"#f38ba8","added":"#a6e3a1","removed":"#f38ba8"}},{"name":"theme-light","theme":{"primary":"#1e66f5","dim":"#ccd0da","muted":"#9ca0b0","warning":"#df8e1d","error":"#d20f39","added":"#40a02b","removed":"#d20f39"}}]}}
SM-reasoning.txt               {"type":"reasoning","data":{"level":2}}
SM-video-config.txt            {"type":"video_config","data":{"fps":5,"res":1}}
SM-task-start.txt              {"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":10,"context":0}}
SM-task-step.txt               {"type":"task","data":{"in_progress":true,"current_step":2,"max_steps":10,"context":8600,"step_tps":12.5,"ttft_ms":1200}}
SM-task-end.txt                {"type":"task","data":{"in_progress":false,"context":1500,"step_tps":12.5,"ttft_ms":1200}}
SM-error.txt                   {"type":"error","data":{"text":"something broke"}}
SM-notify.txt                  {"type":"notify","data":{"text":"all good"}}
SM-tool-confirm.txt            {"type":"tool_confirm","data":{"call_id":"t1"}}
SM-mcp-connecting.txt          {"type":"mcp","data":{"status":"connecting","server":"github"}}
SM-mcp-auth-required.txt        {"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://github.com/login/oauth/authorize?...redirect_uri={{redirect_uri}}&state={{state}}"}}
SM-mcp-auth-running.txt        {"type":"mcp","data":{"status":"auth_running","server":"github"}}
SM-mcp-connected.txt           {"type":"mcp","data":{"status":"connected","server":"github"}}
SM-mcp-failed.txt              {"type":"mcp","data":{"status":"failed","server":"github","error":"connection timeout"}}
SM-mcp-done.txt                {"type":"mcp","data":{"status":"done"}}
SM-session-ready.txt           {"type":"session","data":{"state":"ready"}}
SM-session-closed.txt          {"type":"session","data":{"state":"closed"}}
SM-steering-queued.txt         {"type":"steering","data":{"phase":"queued","text":"steering queued — delivered into the running turn, or as your next prompt"}}
SM-steering-delivered.txt      {"type":"steering","data":{"phase":"delivered"}}
SM-steering-dropped.txt        {"type":"steering","data":{"phase":"dropped","reason":"canceled","text":"steering dropped — the task was canceled before it was delivered"}}
SM-steering-refused.txt        {"type":"steering","data":{"phase":"refused","text":"Too many prompts are already queued for the running task; this one was not queued. Wait for it to continue, or cancel it."}}
```

## Adapter → Agent Commands

Commands are sent as **CI** (Command Input) frames — a JSON envelope with an
adapter-generated call ID, the command name, and an argument string:

```
CI {"call_id":"<call-id>","name":"<command>","input":"<args-string>"}
```

The `input` is an opaque string whose syntax is defined by each command
(e.g. `"/tmp/x.alaya"` for `save`, `"3"` for `model_set`). The adapter does
not need to understand command semantics — it only splits the human-facing
text into name + input.

The command vocabulary — every name, its arguments, and its result — is listed
in [commands.md](../docs/commands.md); this chapter documents how a command
travels, not which ones exist. For a client with no built-in adapter, two matter
most: `cancel` stops the task in flight, and `quit` ends the session, letting a
task that is already running finish first (the [session lifecycle
signal](#session-lifecycle-signal) is what follows).

Every CI frame receives exactly one **CO** (Command Output) frame, echoing
the same `call_id`:

```
CO {"call_id":"<call-id>","output":<command result>}                    ← success
CO {"call_id":"<call-id>","is_error":true,"output":{"code":"...","message":"..."}}  ← failure
```

- `output` is structured JSON defined per command (may be `null` for
  fire-and-forget commands like `cancel`).
- On failure, `output` is a **uniform error object**: `{"code":"<machine-readable>","message":"<human-readable>"}`.
  Machine clients key off `is_error`/`code`; the `message` is for display.

**Rendering is the adapter's job:** CO carries only structured data, never
display text. To render a result (e.g. show the file name after `save`), the
adapter correlates the CO's `call_id` with the CI it sent (the request carries the
`name`) — exactly like tool results: AF carries the tool name, UF carries
only the `id`, and the adapter tracks the mapping. Commands whose effect is
self-evident in the UI (e.g. `cancel`, `theme_set`) render nothing — the
state change itself is the confirmation. (`save`/`fork` render their path,
`mcp_confirm`/`mcp_decline` render a status line.)

**Important:** Command results travel exclusively via CO. SM `error`/`notify`
messages are reserved for non-command events (task errors, MCP status, etc.).

### Async task commands (`continue`, `summarize`)

These commands start a long-running task. CO replies **immediately** —
`{"status":"started"}` on acceptance, or an error if the task cannot start
(e.g. another task is running → `code:"BUSY"`). Task completion is reported
via an SM `task` message carrying the same `command_id`:

```
CI {"call_id":"9","name":"continue"}
SM {"type":"task","data":{"in_progress":true,"command_id":"9"}}      ← running
CO {"call_id":"9","output":{"status":"started"}}                          ← accepted
SM {"type":"task","data":{"in_progress":false,"command_id":"9"}}     ← done
```

CI/CO (the control plane) and SM (the state plane) are independent channels
multiplexed on one stream: the CO ack and the `in_progress:true` frame are two
projections of the same acceptance, and **their relative order is not part of
the contract** — do not depend on seeing the ack before the running state. The
ack is still immediate; it is never deferred until the task finishes. A refused
command produces the CO error and no running frame.

### Example: save command

```
stdin:  CI {"call_id":"1","name":"save","input":"/tmp/x.alaya"}
stdout: CO {"call_id":"1","output":{"path":"/tmp/x.alaya"}}
```

### Example: failed command

```
stdin:  CI {"call_id":"2","name":"model_set","input":"99"}
stdout: CO {"call_id":"2","is_error":true,"output":{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}}
```

### Tool Confirmation

When the agent sends `SM-tool-confirm.txt`, the adapter **must** respond
with a `tool_confirm` or `tool_decline` CI frame:

| SM Received | Adapter Action | CI Response |
|-------------|----------------|-------------|
| `{"type":"tool_confirm","data":{"call_id":"t1"}}` | Show prompt to user | `{"call_id":"...","name":"tool_confirm","input":"t1"}` or `{"call_id":"...","name":"tool_decline","input":"t1"}` |

### MCP Initialization & OAuth

When the agent sends `SM-mcp-auth-required.txt`, the adapter must open the
authorization URL in a browser, start a local callback server to capture
the OAuth code, then respond with the code:

| SM Received | Adapter Action | CI Response |
|-------------|----------------|-------------|
| `{"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://..."}}` | Open browser to `url` (with `{{redirect_uri}}`/`{{state}}` replaced), start callback server | `{"call_id":"...","name":"mcp_confirm","input":"github <code> <redirect_uri>"}` |
| User declines or flow fails | Send decline command | `{"call_id":"...","name":"mcp_decline","input":"github"}` |
| User cancels entire init | Send cancel command | `{"call_id":"...","name":"mcp_cancel"}` |

The `url` field may contain `{{redirect_uri}}` and `{{state}}` placeholders
that the adapter must replace with real values before opening the browser:

- `{{redirect_uri}}` → the adapter's local callback URL (e.g. `http://127.0.0.1:PORT/callback`)
- `{{state}}` → a random CSRF token generated by the adapter

The terminal adapter implements this via `startMCPAuthFlow`
(`internal/adapters/terminal/keybinds.go`): it starts
`platform.StartCallbackServer`, opens the browser with
`platform.OpenURL`, and writes the `mcp_confirm` CI frame on callback.
The plainio and terseio adapters share the same automatic flow in
`internal/mcpauth/mcpauth.go` (asynchronous; the URL is always printed).
Where there is somewhere to type — plainio on a terminal — a callback that
never arrives falls back to printing the manual
`:mcp_confirm`/`:mcp_decline` commands; where there is not — plainio on
piped stdin, and terseio always — the server is declined instead, so MCP
init settles. rawio delegates the whole flow to the controlling process.

### Model Sync

When the user finishes editing model config in an external editor, the adapter
sends the full model list JSON back to the agent via `model_sync`:

| Trigger | Adapter Action | CI Response |
|---------|----------------|-------------|
| `:model` command → editor session ends | Read edited file, send content as JSON array | `{"call_id":"...","name":"model_sync","input":"[{...}, {...}]"}` |

The `input` is a serialized array of model config objects (same format as the
`models` field in `SM-model-list.txt`). The agent replaces all models with
the provided list, re-resolves the active model, and reinitializes the
provider. If any model block is rejected or persistence fails, the command
fails with `code:"MODEL_VALIDATION"` and the errors in `message`.

### Full command list

| Command | input | CO output (success) |
|---------|-------|---------------------|
| `cancel` | — | `null` |
| `save` | `[filename]` | `{"path":...}` |
| `fork` | `<id> <filename>` | `{"path":...,"count":...,"history_id":...}` |
| `theme_set` | `<name>` (TUI only — `UNAVAILABLE` in NoTheme modes: plainio/terseio/rawio) | `{"name":...}` |
| `tool_confirm` / `tool_decline` | `<tool-id>` | `{"tool_id":...}` |
| `model_set` | `<id>` | `{"active_id":...,"active_name":...}` |
| `model_load` | — | `{"models":[...]}` |
| `model_sync` | `<json>` | `{"models":[...]}` |
| `reason` | `<0\|1\|2>` | `{"level":...}` |
| `video_config` | `<fps> <0\|1>` | `{"fps":...,"res":...}` — sets the session **default** for videos ingested after it; each video keeps the settings it was ingested with |
| `mcp_confirm` | `<server> <code> <redirect_uri>` | `{"server":...}` |
| `mcp_decline` | `<server>` | `{"server":...}` |
| `mcp_cancel` | — | `null` |
| `continue` / `summarize` | — | `{"status":"started"}` (async; completion via SM `task` with `command_id`) |

## Use

A sample file *is* the bytes to send — one frame per line, so it can be piped
straight in. `wirecat` decodes what comes back with the same reader the core uses,
which means it also reports a frame the format does not allow.

```sh
# Pipe a user prompt to the agent
cat wire-samples/UX-read-file.txt | alayacore --rawio

# Pipe a user prompt and decode the response frames
cat wire-samples/UX-read-file.txt | alayacore --rawio | go run ./misc/wirecat.go

# Decode a single frame (stdin or stdout direction)
cat wire-samples/AF-read-file-start.txt | go run ./misc/wirecat.go

# Decode a user echo frame (agent → adapter, with history ID)
cat wire-samples/UX-echo-hello.txt | go run ./misc/wirecat.go
```

## Generate Media Requests (Go)

```sh
go run misc/gen_wire_request.go "question" image.jpg audio.wav video.mp4
```
