# Vocabulary

One concept, one name. One name, one concept. Both failures are invisible to
the compiler — a synonym costs a reader a guess, and an overload costs a
reader a wrong guess — which is why they are written down here and checked in
`make check` (`misc/check-terms.sh`).

A name is used **where it is defined**; a name that crosses a file boundary is
qualified. That is the rule the table encodes.

## The session

| Term | In code | Means | Never |
|------|---------|-------|-------|
| **task** | `task`, `beginTask`, `in_progress` | One run of the session: `in_progress` true → false. Contains N steps. Its *kind* — `promptKind` — is the only distinction there is: `userPrompt` (the user's words started it) or `summarizeCall` (`:summarize`, auto-summarize). | `job`, `run` (except Go's own `run()` loop), **`turn`** — a run is a task; say which kind when the difference matters |
| **step** | `step`, `current_step` | One LLM request and the tool calls it asks for, inside a task. | `round`, `iteration` |
| **prompt** | `prompt` | The user's words. In the TUI, the text in the prompt box. | `query`, `message` |
| **command** | `command`, `:name` | A session command: `:save`, `:continue`. Never prompt text. | — |
| **content part** | `llm.ContentPart`, `Contents` | One item of conversation history. | — |

## The wire

| Term | In code | Means | Never |
|------|---------|-------|-------|
| **frame** | `wire.WriteFrame`, `Tag*` | The transport unit: a tag byte, a body, a newline. | `message` for the unit (a `system message` names one frame's payload) |
| **tag** | `wire.TagUserX` | The two letters that open a frame. | — |

## The TUI

| Term | In code | Means | Never |
|------|---------|-------|-------|
| **window** | `Window`, `WindowBuffer` | One entry in the transcript: a prompt, an answer, a tool call, a notice. Its own line is the only chrome. | `block`, `entry`, `item` |
| **overlay** | `overlay`, `*Overlay` | A floating surface that owns the keyboard: help, attachments, model/theme selectors, the confirm dialog. | `window`, `panel`, `popup` |
| **folded** | `Window.Folded` | The one-line state of a window. The other state is **unfolded**. | `collapsed`, `expanded` |
| **window cursor** | `windowCursor`, `WithWindowCursor`, `EnsureWindowCursorVisible` | Which window the keyboard is on, and its highlight. | bare `cursor` |
| **caret** | `caret`, `CaretCell`, `WithCaretPos` | The insertion point inside a text field (the prompt box, a filter box). | bare `cursor` |
| **screen** | `Screen`, `screenWidth`, `ScreenSizeMsg` | The terminal surface and its size. | `window` for the size |
| **screen cursor** | `ScreenCursor`, `NewScreenCursor` | The host terminal's own cursor, drawn where the caret is. | bare `cursor` at a file boundary |
| **prompt box** | `PromptInput`, `Terminal.promptBox` | The box the user types in. | `input box`, `prompt input`, `input field` (`InputField` is the generic text field it embeds) |
| **keys** | `KeyParser`, `keySource`, `readKeys` | Bytes read from the host, and their parsing. | `input` for the host side |
| **session pipe** | `Terminal.sessionPipe` | The writer a prompt or command leaves the TUI through. | `streamInput`, `stdin` |
| **display** | `DisplayModel`, `Terminal.display` | The pane that shows the transcript. | `screen` (that is the terminal) |
| **live edge** | `live_edge.go` | The transcript's closing row: the newest line plus what the viewport hides. | — |

Two layer-local shortcuts are allowed because the file owns the word:

- inside `screen.go` / `program.go`, `cursor` is the **screen cursor** (the
  terminal's own term — `DECSCUSR` is "cursor style");
- inside `display.go` / `window.go` / `window_buffer.go`, `cursor` is the
  **window cursor**;
- inside `input_field.go` / `prompt_input.go`, `caret` is the **caret**.

Nothing else may use an unqualified `cursor`, `input` or `window`.

## Frozen vocabulary

Frame tags, command names, session-file keys and CLI flags are contracts:
`UX`, `:continue`, `in_progress`, `--terseio`. Renaming one of them is a
protocol or format change, not a clarity change, so it travels with a version
bump and a migration (`internal/sessionmigrate`) — never as a rename alone.
Their meaning is already single, which is why they are not on the list of
defects this page exists to prevent.

## Terms that look like duplicates and are not

| Pair | Why both stand |
|------|----------------|
| `userPrompt` / `summarizeCall` | Two kinds of task, not two units: one publishes its progress, keeps the conversation and takes steering; the other does none of that. Both are tasks, and neither is called anything else. |
| `notify` / `notice` | A `SYSTEM NOTIFY` frame versus the verb ("a host that never notices"). |
| `frame` (wire) / `frame` (screen) | The transport unit versus one painted screenful. The file owns the word: `wire`/`protocol` versus `screen.go`. |
| `Msg` (Elm) / `system message` | A UI event versus one frame's payload. In the `terminal` package, Elm's `Msg` owns the bare word; the wire side says `Frame`. |
