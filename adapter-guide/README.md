# Adapter Guide

This document is a **dual-purpose reference**: it describes the TLV protocol
used by `alayacore --rawio` for **human adapter developers**, and it is also
designed to be read by **AI agents** that need to understand the protocol
in order to generate or update adapter implementations.

The `tlv-samples/` subdirectory contains sample `.bin` frames for reference.
When updating this guide, maintain the structured, unambiguous style so that
both human and AI readers can rely on it as the single source of truth.

## Wire Format

```
[2-byte tag][4-byte length (big-endian)][N bytes of value]
```

## Tags

```
UT  → stdin   User text
UI  → stdin   User image (data:image/...;base64,... or URL)
UV  → stdin   User video (data:video/...;base64,... or URL)
UA  → stdin   User audio (data:audio/...;base64,... or URL)
UD  → stdin   User document (data:application/...;base64,... or URL)
UE  → stdin   User message end — flushes staged content as a single message
CI  → stdin   Command input (JSON CmdMsg: {"id":"...","name":"...","input":"..."})
At  ← stdout  Assistant text (streaming delta: \x00<id>\x00<content>)
Ar  ← stdout  Assistant reasoning (streaming delta: \x00<id>\x00<content>)
Af  ← stdout  Function/tool argument (streaming delta: \x00<id>\x00<JSON>)
AT  ← stdout  Assistant text (complete/authoritative: \x00<id>\x00[content]; content empty if deltas preceded it)
AR  ← stdout  Assistant reasoning (complete/authoritative: \x00<id>\x00[content]; content empty if deltas preceded it)
AF  ← stdout  Function/tool lifecycle (\x00<id>\x00<JSON>)
UF  ← stdout  Function/tool result (\x00<id>\x00<JSON>)
Uf  ← stdout  Function/tool result preview (ephemeral, display-only: \x00<id>\x00<JSON>; never authoritative — UF overwrites)
CO  ← stdout  Command output (JSON CmdResultMsg: {"id":"...","output":...,"is_error":...})
SM  ← stdout  System message, no history ID (JSON: {"type":"...","data":{...}})
UT  ← stdout  User text echo (\x00<id>\x00<content>)
UI  ← stdout  User image echo (\x00<id>\x00<data URI or URL>)
UV  ← stdout  User video echo (\x00<id>\x00<data URI or URL>)
UA  ← stdout  User audio echo (\x00<id>\x00<data URI or URL>)
UD  ← stdout  User document echo (\x00<id>\x00<data URI or URL>)
```

## Tag Naming Convention

Each tag is two characters: **role** + **type**.

| First letter | Role | Examples |
|---|---|---|
| `U` | **U**ser | UT (user text), UI (user image), UF (function result), Uf (result preview) |
| `A` | **A**ssistant | AT (assistant text), AR (assistant reasoning), AF (function call) |
| `C` | **C**ommand (control plane) | CI (command input), CO (command output) |
| `S` | **S**ystem | SM (system message) |

| Second letter | Type | Examples |
|---|---|---|
| `T` | **T**ext | UT, AT, At |
| `I` | **I**mage | UI |
| `V` | **V**ideo | UV |
| `A` | **A**udio | UA |
| `D` | **D**ocument | UD |
| `R` | **R**easoning | AR, Ar |
| `F` | **F**unction/tool | AF, UF, Af, Uf |
| `E` | **E**nd (flush) | UE |
| `I` | **I**nput (command request) | CI |
| `O` | **O**utput (command result) | CO |
| `M` | **M**essage | SM |

**Case convention:** Uppercase tags carry complete/authoritative content.
Lowercase tags carry streaming delta (incremental) content:
- `At`/`Ar` — text/reasoning deltas, appended to the window per chunk
- `AT`/`AR` — complete text/reasoning, sent after all deltas for that block (content is empty when deltas preceded it; carries full text during replay or `--no-delta` mode)
- `Af` — tool argument delta, partial JSON chunk during streaming
- `Uf` — tool result preview snapshot (ephemeral, display-only; the
  authoritative result always arrives via `UF`, which overwrites any
  preview content)
- `AF` — complete tool call (start + input frames)

The role indicates who the content belongs to in the conversation, **not** the
direction of the message. For example, UF is a function result (user-role
content) but appears on stdout. Similarly, UT/UI/UV/UA/UD on stdout are user
message echoes — they carry user-role content sent from the agent back to the
adapter.

**Control-plane exception:** The `C` (Command) tags are the exception — they
are control frames, not conversation content. `I`/`O` encode **direction**:
`CI` is sent by the adapter on stdin, `CO` by the agent on stdout. Unlike
user tags, command tags never appear on both sides.

**Important:** User tags (UT, UI, UV, UA, UD) appear on **both** stdin and stdout:

| Direction | Meaning |
|-----------|---------|
| **stdin** (adapter → agent) | New user input — the adapter sends a user message to the agent |
| **stdout** (agent → adapter) | User message echo — the agent echoes the user's message back to the adapter for display, with a history ID assigned |

The adapter must be prepared to **receive** user tags on stdout in these scenarios:

1. **Prompt echo** — When the user sends a prompt (UT + UE on stdin), the agent echoes each content part back on stdout with an assigned history ID before sending them to the LLM.
2. **Session replay** — When a saved session file (key-value frontmatter + binary TLV body, specified via `--session`) is loaded, all historical content (including user messages) is replayed to the adapter on stdout with their original history IDs.
3. **Auto-summarize** — The auto-summarization prompt is echoed as UT on stdout.

> **For adapter implementors:** You cannot assume user tags only appear on stdin.
> The terminal adapter (`internal/adapters/terminal/output.go`), the plainio
> adapter (`internal/adapters/plainio/output.go`), and the terseio adapter
> (`internal/adapters/terseio/output.go`, which suppresses them — its stdout
> carries only the final answer) all receive user tags from stdout.

## Delta Messages (At, Ar)

Streaming content uses **lowercase** tags (`At`, `Ar`) with NUL-delimited history IDs:

```
\x00<history-id>\x00<content>
```

The same history ID can appear multiple times — each subsequent frame is a
continuation (delta) of that content block. The adapter concatenates them.

> **Uf is lowercase but not a continuation:** the tool result preview
> (`Uf`) is a **snapshot**, not a delta — each frame replaces the previous
> preview and frames are never concatenated. See [Function Lifecycle](#function-lifecycle-af-uf).

**Complete/authoritative frames** use **uppercase** tags (`AT`, `AR`). These are
sent after all deltas for a content block have been received. The adapter may
choose either approach:
- If it has accumulated deltas (`At`/`Ar`), it can **ignore** the uppercase frame
  (deltas already represent the complete content).
- Alternatively, it can **use** the uppercase frame as the authoritative source,
  replacing any delta-accumulated content.

During replay (session load) only the uppercase frames are sent; no deltas
precede them — the adapter must use the uppercase frame to display the content.

**`--no-delta` mode:** When the `--no-delta` flag is set, no lowercase delta
frames (At, Ar, Af, Uf) are sent. Instead, the uppercase frames (AT, AR, AF)
carry the complete content directly. This mode is useful for adapters that
prefer batch delivery over streaming, or for environments where the overhead
of many small frames is undesirable.

| Mode | At/Ar | Af | Uf | AT/AR content | AF content |
|------|-------|----|----|---------------|------------|
| Default (deltas on) | Sent | Sent | Sent (ephemeral preview) | Empty (terminator only) | Repaired JSON (always complete) |
| `--no-delta` | Not sent | Not sent | Not sent | Full text | Repaired JSON (always complete) |

**History ID format:** a flat monotonic history counter (e.g. `1`, `2`, `3`).
Each content block (text, reasoning, tool call, user content part) receives a
unique ID from this counter.

**History ID:**
- Same history ID → content is a continuation of that stream (At/Ar only)
- Different history ID → different content block
- No NUL prefix → plain text, no history ID

## User Echo on stdout (UT, UI, UV, UA, UD)

User echoes are **not** delta — each frame contains **complete content** (the full
text string, the full data URI or URL). There is no streaming/continuation for
user echo frames; each frame is a standalone content part.

When a user sends multiple parts in one prompt (e.g. image + text), the agent
echoes them as consecutive user-tag frames on stdout. The adapter groups them
into a single user message. Other tag types use different grouping mechanisms:

| Tags | Grouping mechanism |
|------|-------------------|
| UT, UI, UV, UA, UD (stdout) | **Position** — consecutive frames belong to one message; a non-user tag starts the next |
| At, Ar | **History ID** — same ID = same stream (delta concatenation) |
| Af | **JSON `"id"`** — partial JSON chunk for the named tool call |
| AF | **History ID** (start+input share same ID) + **JSON `"id"`** — start announces name, input carries arguments |
| UF | **JSON `"id"`** — matches the corresponding AF (history ID is present but not used for matching) |
| Uf | **JSON `"id"`** — snapshot preview for the named tool call (not concatenated; UF overwrites) |
| SM | **None** — each frame is standalone |

Each user content part gets its own **unique** history ID (monotonic counter).

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UI data:image/png;base64,...
                               UT "What's in these images?"
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UI \x00 2 \x00 data:image/png;base64,...    ← different ID
                               UT \x00 3 \x00 What's in these images?      ← different ID
                               AT \x00 4 \x00 These images show...         ← starts assistant message
```

## Function Lifecycle (AF, UF)

All stdout content frames carry a NUL-delimited history ID (`\x00<id>\x00`). For
AF/UF the history ID identifies the content block; tool matching uses the JSON
`"id"` field.

**AF** — function lifecycle (two or three frames live, one frame on replay):
- `\x00<id>\x00{"id":"t1","name":"read_file"}` — tool name announced (start frame, no input yet)
- `\x00<id>\x00{"id":"t1","delta":"{\\"path\\":"}"` — partial JSON argument (delta frame, zero or more during streaming)
- `\x00<id>\x00{"id":"t1","input":{...}}` — full tool arguments (input frame, name already known, same history ID as start)

During live streaming `OnToolInputStart` → `AF` start, `OnToolInputDelta` → `Af` (zero or more),
and `OnToolInputComplete` → `AF` input share the same history ID (they belong to the same tool call).
During session replay the tool call is a single AF frame with both `name` and `input`.

**UF** — function result:
- `\x00<id>\x00{"id":"t1","output":[...]}` — succeeded (`is_error` omitted when `false`)
- `\x00<id>\x00{"id":"t1","output":[...],"is_error":true}` — failed

**Uf** — ephemeral result preview (display only, optional):
- `\x00<id>\x00{"id":"t1","text":" 42%"}` — live snapshot of the tool result while it runs
- `text` is a single line: the current line of the most recently written
  stream (stdout or stderr), with the other stream as fallback
- Sent only when the tool supports streaming (e.g. `execute_command`); zero or more
  frames, coalesced at ~100ms. **Never authoritative**: adapters may drop or ignore
  it, and the final `UF` frame always overwrites any preview content.

A tool call (AF) without a matching UF is still in progress. Each `.bin` sample below shows one frame in this lifecycle.

## Example: Text Prompt Flow (with streaming deltas)

```
Adapter writes → stdin:        UT "Read the file main.go"
                               UE
Session writes → stdout:       UT \x00 1 \x00 Read the file main.go        ← echo with history ID
                               AF \x00 2 \x00 {"id":"t1","name":"read_file"} ← non-user tag flushes user msg
                               Af \x00 2 \x00 {"id":"t1","delta":"{\\"path\\":"} ← partial arg delta
                               Af \x00 2 \x00 {"id":"t1","delta":"\\"main.go\\"}"}  ← more args
                               AF \x00 2 \x00 {"id":"t1","input":{"path":"main.go"}}  ← complete args
                               Uf \x00 2 \x00 {"id":"t1","text":"reading main.go..."}  ← ephemeral result preview (optional)
                               UF \x00 3 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               At \x00 4 \x00 Here's what main.go does...    ← streaming text delta
                               AT \x00 4 \x00                                 ← authoritative complete (empty terminator)
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Text Prompt Flow (replay, no deltas)

```
Session writes → stdout:       UT \x00 1 \x00 Read the file main.go
                               AF \x00 2 \x00 {"id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF \x00 3 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               AT \x00 4 \x00 Here's what main.go does...
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Image Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UI data:image/png;base64,...
                               UT "What's in these images?"
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UI \x00 2 \x00 data:image/png;base64,...
                               UT \x00 3 \x00 What's in these images?
                               AT \x00 4 \x00 These images contain...         ← complete (live or replay)
```

The three user frames (IDs 1, 2, 3) are accumulated into one user message.
`AT \x00 4` triggers the flush and begins the assistant response.

## Example: Media Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...    (or URL)
                               UA data:audio/mpeg;base64,...    (or URL)
                               UV data:video/mp4;base64,...     (or URL)
                               UD data:application/pdf;base64,... (or URL)
                               UT "Analyze these files"        (optional — media-only is valid)
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UA \x00 2 \x00 data:audio/mpeg;base64,...
                               UV \x00 3 \x00 data:video/mp4;base64,...
                               UD \x00 4 \x00 data:application/pdf;base64,...
                               UT \x00 5 \x00 Analyze these files       (absent for media-only)
                               AT \x00 6 \x00 Here's my analysis...
```

All media types accept either `data:` URIs or plain URLs. Media frames and text
can be combined in any order. The adapter accumulates them all until the first
non-user tag.

## Example: Session Replay Flow

When a saved session is loaded, all previous content is replayed on stdout.
Each content part gets a sequential history ID (rebuilt from `seqID++`):

```
Session writes → stdout:       UT \x00 1 \x00 Hello                        ← user
                               AT \x00 2 \x00 Hi! How can I help?          ← assistant
                               UT \x00 3 \x00 Read main.go                 ← user
                               AF \x00 4 \x00 {"id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF \x00 5 \x00 {"id":"t1","output":[...]}
                               AT \x00 6 \x00 Here's the content...        ← assistant
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

Note: During replay, a tool call is a single AF frame with both `name` and
`input` together (one `ToolInputPart`). The two-frame split (start then input)
only happens during live streaming. User and assistant frames are interleaved
as they were in the original conversation.

## Adapter Implementation Notes

### Handling user tags on stdout

1. **Parse the history ID** from the NUL-delimited prefix (`\x00<id>\x00<content>`).
   If no NUL prefix, the content is plain text without a history ID.

2. **Group by position:** consecutive user-tag frames (UT, UI, UV, UA, UD)
   belong to one user message. When a non-user tag arrives (AT, AR, AF, UF, SM),
   it starts the next message. There is no `UE` on stdout.

3. **Display:** Since there is no UE on stdout, the adapter cannot know if
   more user frames are coming until a non-user tag arrives. Render each
   frame incrementally as it arrives (e.g. the TUI adapter calls
   `SetWindowVisible` + `dirty.Store(true)` on every frame, then finalizes
   the window metadata when a non-user tag arrives).

### Handling history IDs

All stdout content-part frames (AT, AR, AF, UF, UT/UI/UA/UV/UD echoes) carry
a NUL-delimited history ID prefix `\x00<id>\x00`. The history ID is a flat
monotonic counter that increases over the session lifetime. System messages
(SM) and stdin frames never carry history IDs.

The **semantics** of the history ID differ by tag type:

| Tag | Same history ID means | Grouping / matching |
|-----|-----------------------|---------------------|
| AT, AR | **Content continuation** (delta) — concatenate frames with the same ID into one block | History ID |
| AF (start + input) | **Same tool call** — start frame announces the name, input frame carries arguments (not concatenated) | JSON `"id"` field + history ID |
| UF | **Same tool result** (matched to AF by JSON `"id"`, not by history ID) | JSON `"id"` field |
| Uf | **Same tool result preview** (matched to AF by JSON `"id"`; snapshot, not concatenated) | JSON `"id"` field |
| UT/UI/UA/UV/UD (stdout) | **N/A** — each echo has a unique history ID | **Position** (consecutive user tags → one message) |

**Key rules:**
- Different history ID → different content block (all tags)
- No NUL prefix → plain text, no history ID (SM only; stdin: all frames)
- History IDs are ephemeral: rebuilt from `seqID++` on session load, not persisted
- On session replay, ALL content parts (AT, AR, AF, UF, UT/UI/UA/UV/UD) carry a history ID,
  matching the format they had during live streaming

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
   - **Pre-summarize backup / auto-summarization** arrive during the task;
     the prompt continues (possibly over the context threshold). Display
     them so the user knows the session is at risk.

5. **Output stream broken**: On the first write error to stdout, the agent
   cancels the session context and stops processing. No further frames are
   sent. The adapter will see EOF on stdout and should handle it gracefully
   (e.g. close the connection, show a notification).

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
user's prompt echo with its assigned history ID.

### read_file

```
stdin:  UT-read-file.bin               UT "Read the file main.go"
                                       UE
stdout: UT-echo-read-file.bin          UT \x00 5 \x00 Read the file main.go
        AF-read-file-start.bin         AF \x00 6 \x00 {"id":"t1","name":"read_file"}
        AF-read-file-input.bin         AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go"}}   ← or AF-read-file-input-range.bin
        UF-read-file-success.bin       UF \x00 7 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
        UF-read-file-failed.bin        UF \x00 7 \x00 {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
```

The `AF-read-file-input-range.bin` sample is an **alternative** to `AF-read-file-input.bin` — it demonstrates the `start_line` and `num_lines` optional parameters. Only one input frame is sent per tool invocation.

### write_file

```
stdin:  UT-write-file.bin              UT "Write a hello world Go program to hello.go"
                                       UE
stdout: (echo)                         UT \x00 9 \x00 Write a hello world Go program to hello.go
        AF-write-file-start.bin        AF \x00 10 \x00 {"id":"t3","name":"write_file"}
        AF-write-file-input.bin        AF \x00 10 \x00 {"id":"t3","input":{"content":"package main","path":"main.go"}}
        UF-write-file-success.bin      UF \x00 11 \x00 {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
        UF-write-file-failed.bin       UF \x00 11 \x00 {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
```

### edit_file

```
stdin:  UT-edit-file.bin               UT "Edit main.go to fix the greeting"
                                       UE
stdout: (echo)                         UT \x00 7 \x00 Edit main.go to fix the greeting
        AF-edit-file-start.bin         AF \x00 8 \x00 {"id":"t2","name":"edit_file"}
        AF-edit-file-input.bin         AF \x00 8 \x00 {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
        UF-edit-file-success.bin       UF \x00 9 \x00 {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
        UF-edit-file-failed.bin        UF \x00 9 \x00 {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
```

### search_content

```
stdin:  UT-search-content.bin          UT "Search for TODO in Go files"
                                       UE
stdout: (echo)                         UT \x00 11 \x00 Search for TODO in Go files
        AF-search-content-start.bin    AF \x00 12 \x00 {"id":"t4","name":"search_content"}
        AF-search-content-input.bin    AF \x00 12 \x00 {"id":"t4","input":{"pattern":"TODO"}}
        UF-search-content-success.bin  UF \x00 13 \x00 {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
        UF-search-content-failed.bin   UF \x00 13 \x00 {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
```

### execute_command

```
stdin:  UT-execute-command.bin         UT "Run: ls -la"
                                       UE
stdout: (echo)                         UT \x00 13 \x00 Run: ls -la
        AF-execute-command-start.bin   AF \x00 14 \x00 {"id":"t5","name":"execute_command"}
        AF-execute-command-input.bin   AF \x00 14 \x00 {"id":"t5","input":{"command":"ls -la"}}
        Uf-execute-command-preview.bin Uf \x00 14 \x00 {"id":"t5","text":"total 42..."}   ← ephemeral preview (optional)
        UF-execute-command-success.bin UF \x00 15 \x00 {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
        UF-execute-command-failed.bin  UF \x00 15 \x00 {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

### Text / Reasoning / System / Tool

All stdout frames except SM carry a NUL-delimited history ID prefix.
Samples are grouped by direction below.

**stdin (adapter → agent, no history ID):**

```
UT-hello.bin                   UT "hello"
UT-empty.bin                   UT "" (length 0)
UI-image.bin                   UI data:image/jpeg;base64,...
UI-image-url.bin               UI https://...
UA-audio.bin                   UA data:audio/mpeg;base64,...
UA-audio-url.bin               UA https://...
UV-video.bin                   UV data:video/mp4;base64,...
UV-video-url.bin               UV https://...
UD-document.bin                UD data:application/pdf;base64,...
UE.bin                         UE "" (length 0)
CI-cancel.bin                  CI {"id":"1","name":"cancel"}
CI-save.bin                    CI {"id":"2","name":"save","input":"/tmp/x.alaya"}
CI-model-sync.bin              CI {"id":"3","name":"model_sync","input":"[{id,name,protocol_type,base_url,api_key,model_name,context_limit,max_tokens},...]"}
```

**stdout — delta/echo (with history ID `\x00<id>\x00`):**

```
UT-echo-hello.bin              UT \x00 1 \x00 Hello
UT-echo-read-file.bin          UT \x00 5 \x00 Read the file main.go
UI-echo-image.bin              UI \x00 2 \x00 data:image/jpeg;...
UA-echo-audio-url.bin          UA \x00 3 \x00 https://...
UV-echo-video-url.bin          UV \x00 4 \x00 https://...
At-hello.bin                   At \x00 1 \x00 Hello
At-world.bin                   At \x00 1 \x00 world (same stream)
At-new-step.bin                At \x00 2 \x00 Next step (new stream)
Ar-thinking.bin                Ar \x00 3 \x00 thinking...
AT-complete.bin                AT \x00 4 \x00 Here's what main.go does...   (content varies by mode)
AR-complete.bin                AR \x00 3 \x00 I need to read the file first... (content varies by mode)
```

**stdout — tool frames (with history ID `\x00<id>\x00`, JSON `"id"` for matching):**

```
AF-read-file-start.bin         AF \x00 6 \x00 {"id":"t1","name":"read_file"}
AF-read-file-input.bin         AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go"}}
AF-read-file-input-range.bin   AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go","start_line":10,"num_lines":20}}
UF-read-file-success.bin       UF \x00 7 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
UF-read-file-failed.bin        UF \x00 7 \x00 {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
AF-edit-file-start.bin         AF \x00 8 \x00 {"id":"t2","name":"edit_file"}
AF-edit-file-input.bin         AF \x00 8 \x00 {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
UF-edit-file-success.bin       UF \x00 9 \x00 {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
UF-edit-file-failed.bin        UF \x00 9 \x00 {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
AF-write-file-start.bin        AF \x00 10 \x00 {"id":"t3","name":"write_file"}
AF-write-file-input.bin        AF \x00 10 \x00 {"id":"t3","input":{"content":"package main","path":"main.go"}}
UF-write-file-success.bin      UF \x00 11 \x00 {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
UF-write-file-failed.bin       UF \x00 11 \x00 {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
AF-search-content-start.bin    AF \x00 12 \x00 {"id":"t4","name":"search_content"}
AF-search-content-input.bin    AF \x00 12 \x00 {"id":"t4","input":{"pattern":"TODO"}}
UF-search-content-success.bin  UF \x00 13 \x00 {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
UF-search-content-failed.bin   UF \x00 13 \x00 {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
AF-execute-command-start.bin   AF \x00 14 \x00 {"id":"t5","name":"execute_command"}
AF-execute-command-input.bin   AF \x00 14 \x00 {"id":"t5","input":{"command":"ls -la"}}
Uf-execute-command-preview.bin Uf \x00 14 \x00 {"id":"t5","text":"total 42..."}   ← ephemeral preview (optional)
UF-execute-command-success.bin UF \x00 15 \x00 {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
UF-execute-command-failed.bin  UF \x00 15 \x00 {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

**stdout — command output (CO, no history ID, JSON CmdResultMsg):**

```
CO-save-success.bin            CO {"id":"2","output":{"path":"/tmp/x.alaya"}}
CO-command-error.bin           CO {"id":"2","is_error":true,"output":{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}}
CO-task-started.bin            CO {"id":"9","output":{"status":"started"}}
```

**stdout — system messages (no history ID, JSON `{"type":"...","data":{...}}`):**

| Type | JSON Schema (data fields) | Example `.bin` |
|------|--------------------------|----------------|
| `version` | `message_version` (int), `core_version` (string) | `SM-message-version.bin` |
| `model` | `active_id` (int), `active_name` (string), `context_limit` (int) | `SM-model.bin` |
| `model_list` | `models` (array of `{id:int, name:string, protocol_type:string, base_url:string, api_key:string, model_name:string, context_limit:int, max_tokens:int}`) | `SM-model-list.bin` |
| `theme` | TUI only (not sent in NoTheme modes: plainio/terseio/rawio). `name` (string), `theme` (object, optional — full palette sent on startup, omitted on theme switch) | `SM-theme.bin` |
| `theme_list` | TUI only (not sent in NoTheme modes: plainio/terseio/rawio). `themes` (array of `{name:string, theme:{primary, dim, muted, text, warning, error, success, selection, cursor, added, removed, tool, fold_indicator: string}}`) | `SM-theme-list.bin` |
| `reasoning` | `level` (int: 0=off, 1=normal, 2=max) | `SM-reasoning.bin` |
| `video_config` | `fps` (int), `res` (int) | `SM-video-config.bin` |
| `task` | `in_progress` (bool), `current_step` (int, opt), `max_steps` (int, opt), `context` (int), `task_error` (bool, opt), `command_id` (string, opt — set when the task was started by `continue`/`summarize`) | `SM-task-start.bin`, `SM-task-end.bin` |
| `error` | `text` (string) | `SM-error.bin` |
| `notify` | `text` (string) | `SM-notify.bin` |
| `tool_confirm` | `id` (string), `allowed` (bool, opt — present only in adapter→agent response) | `SM-tool-confirm.bin` |
| `mcp` | `status` (string: one of `connecting`, `auth_required`, `auth_running`, `connected`, `failed`, `done`), `server` (string, opt), `url` (string, opt — set for `auth_required`; may contain `{{redirect_uri}}` and `{{state}}` placeholders), `error` (string, opt — set for `failed`) | `SM-mcp-connecting.bin`, `SM-mcp-auth-required.bin`, `SM-mcp-auth-running.bin`, `SM-mcp-connected.bin`, `SM-mcp-failed.bin`, `SM-mcp-done.bin` |

Complete wire values:

```
SM-message-version.bin         {"type":"version","data":{"message_version":11,"core_version":"(set at build time)"}}
SM-model.bin                   {"type":"model","data":{"active_id":4,"active_name":"DeepSeek / DeepSeek-V4 Flash","context_limit":1000000}}
SM-model-list.bin              {"type":"model_list","data":{"models":[{"id":0,"name":"Anthropic / Claude Haiku 4","protocol_type":"anthropic","base_url":"https://api.anthropic.com","api_key":"sk-ant-...","model_name":"claude-haiku-4-20260515","context_limit":200000,"max_tokens":0},{"id":4,"name":"DeepSeek / DeepSeek-V4 Flash","protocol_type":"openai","base_url":"https://api.deepseek.com/v1","api_key":"sk-ds-...","model_name":"deepseek-v4-flash","context_limit":1000000,"max_tokens":0}]}}
SM-theme.bin                   {"type":"theme","data":{"name":"theme-dark"}}
SM-theme-list.bin              {"type":"theme_list","data":{"themes":[{"name":"theme-dark","theme":{"primary":"#89d4fa","dim":"#313244","muted":"#6c7086","text":"#cdd6f4","warning":"#f77923","error":"#f38ba8","success":"#a6e3a1","selection":"#fab387","cursor":"#cdd6f4","added":"#a6e3a1","removed":"#f38ba8","tool":"#f9e2af","fold_indicator":"\u205d"}},{"name":"theme-light","theme":{"primary":"#1e66f5","dim":"#ccd0da","muted":"#9ca0b0","text":"#4c4f69","warning":"#df8e1d","error":"#d20f39","success":"#40a02b","selection":"#fe640b","cursor":"#dc8a78","added":"#40a02b","removed":"#d20f39","tool":"#df8e1d","fold_indicator":"\u205d"}}]}}
SM-reasoning.bin               {"type":"reasoning","data":{"level":2}}
SM-video-config.bin            {"type":"video_config","data":{"fps":5,"res":1}}
SM-task-start.bin              {"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":10,"context":0}}
SM-task-end.bin                {"type":"task","data":{"in_progress":false,"context":1500}}
SM-error.bin                   {"type":"error","data":{"text":"something broke"}}
SM-notify.bin                  {"type":"notify","data":{"text":"all good"}}
SM-tool-confirm.bin            {"type":"tool_confirm","data":{"id":"t1"}}
SM-mcp-connecting.bin          {"type":"mcp","data":{"status":"connecting","server":"github"}}
SM-mcp-auth-required.bin        {"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://github.com/login/oauth/authorize?...redirect_uri={{redirect_uri}}&state={{state}}"}}
SM-mcp-auth-running.bin        {"type":"mcp","data":{"status":"auth_running","server":"github"}}
SM-mcp-connected.bin           {"type":"mcp","data":{"status":"connected","server":"github"}}
SM-mcp-failed.bin              {"type":"mcp","data":{"status":"failed","server":"github","error":"connection timeout"}}
SM-mcp-done.bin                {"type":"mcp","data":{"status":"done"}}
```

## Adapter → Agent Commands

Commands are sent as **CI** (Command Input) frames — a JSON envelope with an
adapter-generated call ID, the command name, and an argument string:

```
CI {"id":"<call-id>","name":"<command>","input":"<args-string>"}
```

The `input` is an opaque string whose syntax is defined by each command
(e.g. `"/tmp/x.alaya"` for `save`, `"3"` for `model_set`). The adapter does
not need to understand command semantics — it only splits the human-facing
text into name + input.

Every CI frame receives exactly one **CO** (Command Output) frame, echoing
the same `id`:

```
CO {"id":"<call-id>","output":<command result>}                    ← success
CO {"id":"<call-id>","is_error":true,"output":{"code":"...","message":"..."}}  ← failure
```

- `output` is structured JSON defined per command (may be `null` for
  fire-and-forget commands like `cancel`).
- On failure, `output` is a **uniform error object**: `{"code":"<machine-readable>","message":"<human-readable>"}`.
  Machine clients key off `is_error`/`code`; the `message` is for display.

**Rendering is the adapter's job:** CO carries only structured data, never
display text. To render a result (e.g. show the file name after `save`), the
adapter correlates the CO's `id` with the CI it sent (the request carries the
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
CI {"id":"9","name":"continue"}
CO {"id":"9","output":{"status":"started"}}                          ← accepted
SM {"type":"task","data":{"in_progress":true,"command_id":"9"}}      ← running
SM {"type":"task","data":{"in_progress":false,"command_id":"9"}}     ← done
```

### Example: save command

```
stdin:  CI {"id":"1","name":"save","input":"/tmp/x.alaya"}
stdout: CO {"id":"1","output":{"path":"/tmp/x.alaya"}}
```

### Example: failed command

```
stdin:  CI {"id":"2","name":"model_set","input":"99"}
stdout: CO {"id":"2","is_error":true,"output":{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}}
```

### Tool Confirmation

When the agent sends `SM-tool-confirm.bin`, the adapter **must** respond
with a `tool_confirm` or `tool_decline` CI frame:

| SM Received | Adapter Action | CI Response |
|-------------|----------------|-------------|
| `{"type":"tool_confirm","data":{"id":"t1"}}` | Show prompt to user | `{"id":"...","name":"tool_confirm","input":"t1"}` or `{"id":"...","name":"tool_decline","input":"t1"}` |

### MCP Initialization & OAuth

When the agent sends `SM-mcp-auth-required.bin`, the adapter must open the
authorization URL in a browser, start a local callback server to capture
the OAuth code, then respond with the code:

| SM Received | Adapter Action | CI Response |
|-------------|----------------|-------------|
| `{"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://..."}}` | Open browser to `url` (with `{{redirect_uri}}`/`{{state}}` replaced), start callback server | `{"id":"...","name":"mcp_confirm","input":"github <code> <redirect_uri>"}` |
| User declines or flow fails | Send decline command | `{"id":"...","name":"mcp_decline","input":"github"}` |
| User cancels entire init | Send cancel command | `{"id":"...","name":"mcp_cancel"}` |

The `url` field may contain `{{redirect_uri}}` and `{{state}}` placeholders
that the adapter must replace with real values before opening the browser:

- `{{redirect_uri}}` → the adapter's local callback URL (e.g. `http://127.0.0.1:PORT/callback`)
- `{{state}}` → a random CSRF token generated by the adapter

The terminal adapter implements this via `startMCPAuthFlow`
(`internal/adapters/terminal/keybinds.go`): it starts
`platform.StartCallbackServer`, opens the browser with
`platform.OpenURL`, and writes the `mcp_confirm` CI frame on callback.
The plainio adapter mirrors the same flow in
`internal/adapters/plainio/mcp.go` (asynchronous, URL always printed as
a manual fallback). terseio and rawio do not implement it: terseio is
scripting-only, and rawio delegates the whole flow to the controlling
process.

### Model Sync

When the user finishes editing model config in an external editor, the adapter
sends the full model list JSON back to the agent via `model_sync`:

| Trigger | Adapter Action | CI Response |
|---------|----------------|-------------|
| `:model` command → editor session ends | Read edited file, send content as JSON array | `{"id":"...","name":"model_sync","input":"[{...}, {...}]"}` |

The `input` is a serialized array of model config objects (same format as the
`models` field in `SM-model-list.bin`). The agent replaces all models with
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
| `video_config` | `<fps> <0\|1>` | `{"fps":...,"res":...}` |
| `mcp_confirm` | `<server> <code> <redirect_uri>` | `{"server":...}` |
| `mcp_decline` | `<server>` | `{"server":...}` |
| `mcp_cancel` | — | `null` |
| `continue` / `summarize` | — | `{"status":"started"}` (async; completion via SM `task` with `command_id`) |

## Use

```sh
# Pipe a user prompt to the agent
cat tlv-samples/UT-read-file.bin | alayacore --rawio

# Pipe a user prompt and decode the response frames
cat tlv-samples/UT-read-file.bin | alayacore --rawio | go run ./misc/tlvcat.go

# Decode a single frame (stdin or stdout direction)
cat tlv-samples/AF-read-file-start.bin | go run ./misc/tlvcat.go

# Decode a user echo frame (agent → adapter, with history ID)
cat tlv-samples/UT-echo-hello.bin | go run ./misc/tlvcat.go
```

## Generate Media Requests (Go)

```sh
go run misc/gen_tlv_request.go "question" image.jpg audio.wav video.mp4
```
