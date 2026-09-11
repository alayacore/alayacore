# TUI Architecture: Elm and AlayaCore's Design

## The Elm Architecture (Reference Model)

The Elm architecture is built on three core concepts:

```
Model  →  application state (single value)
Update →  pure function: (Model, Msg) → (Model, Cmd)
View   →  pure function: Model → Html (rendering)
```

Properties:
- **Immutable state** — `Update` returns a new `Model`, never mutates the old one
- **Side effects as data** — `Cmd` describes what to do, not how to do it (inspectable record)
- **Same-frame Cmd processing** — Runtime can inspect Cmd data and recursively call `update` within the same frame before rendering

## The Runtime: Key Differences from Elm

AlayaCore runs its own minimal TUI runtime (`program.go`) that keeps the
Elm/Bubble Tea model — `Model`/`Update`/`View`/`Cmd`/`Msg` —
without the framework:

```go
type Cmd func() Msg  // not data — an opaque function
```

| Aspect | Elm | Our runtime | Consequence |
|--------|-----|------------|-------------|
| Cmd | Data (inspectable record) | `func() Msg` (opaque) | Runtime cannot inspect Cmd; renders before executing it |
| Msg dispatch | Sum types, exhaustive | `interface{}` + type switch | No compiler guarantee |
| Same-frame effect | Yes — runtime recurses before render | Cmd runs after render | UI transitions are reported as `Result` values and folded in the same Update; `Cmd` carries only I/O |

## Architecture Overview

```
Terminal (value type, root model)
├── Update(msg Msg) → (Model, Cmd)     ← single entry point
│
├── KeyMsg → walks the input layer stack (inputLayers), top first:
│   ├── universal   → Ctrl+Z (suspend), above every overlay and modal
│   ├── modal       → confirm / MCP dialog (consumes all keys)
│   ├── overlay     → theme / model / attachment / help (consumes all keys)
│   ├── global      → Tab, Ctrl+S/L/P/R/H, F1
│   └── pane        → DisplayModel.Update / PromptInput.Update
│       The first layer that consumes the key wins. keyboardTarget walks the
│       same stack to answer "which box does text go to", so dispatch and
│       routing cannot disagree.
│
├── A component's Update returns (Self, []Result): facts as values.
│   The dispatcher folds them synchronously through applyResult (a selection,
│   an editor request, a confirm outcome), so the state change is same-frame and
│   no opaque Cmd has to be unwrapped. Cmd is for I/O only.
│
├── Key identity is a Chord (Code + Mod), never a string: KeyMsg.Chord() →
│   Chord, and every handler switches on a Chord (keys.go).
│
├── Asynchronous messages still handled by Update: session load results,
│   WindowSizeMsg, tickMsg, themePreviewMsg, editor events,
│   displayError/NotifyMsg, FocusMsg/BlurMsg, PasteMsg.
│
├── Components (each has Update returning []Result):
│   ├── DisplayModel      Update(msg Msg) → (DisplayModel,     []Result)
│   ├── PromptInput       Update(msg Msg) → (PromptInput,      []Result)
│   ├── ConfirmDialog     Update(msg Msg) → (ConfirmDialog,    []Result)
│   ├── ThemeSelector     Update(msg Msg) → (ThemeSelector,    []Result)
│   ├── ModelSelector     Update(msg Msg) → (ModelSelector,    []Result)
│   ├── HelpWindow        Update(msg Msg) → (HelpWindow,       []Result)
│   ├── AttachmentWindow  Update(msg Msg) → (AttachmentWindow, []Result)
│   └── InputField        Update(msg Msg) → (InputField,       []Result)
│
├── Code reuse units (pure functions, no I/O):
│   └── FilteredListCore  HandleKey(msg KeyMsg) → (Self, FilteredListResult)
│
└── External systems (via interfaces/pointers):
    ├── out         OutputWriter    (session output, shared mutable)
    ├── streamInput io.WriteCloser  (TLV pipe to session)
    └── themeManager *ThemeManager  (theme load errors at startup)

```

## Component vs Code Reuse Unit

### Components
- Have their own lifecycle (open/close)
- Report facts to Terminal as `Result` **values** (ModelSelectedMsg,
  AttachmentSelectedMsg, …), which Terminal folds synchronously
- All have `Update(msg Msg) → (Self, []Result)`

### Code Reuse Units (FilteredListCore)
- Cannot exist independently — embedded into components
- Have `HandleKey(msg KeyMsg) → (Self, FilteredListResult)` — no I/O
- Shared pure logic (filtering, list navigation) with no parent-visible
  results, so it needs neither a Cmd nor a Result
- Its `FilteredListResult` still carries `[]Result` for the inner `InputField`,
  which reports nothing today but keeps the core from assuming that

## Message-Based Communication

Components report facts to Terminal as `Result` values, not as Cmds that
Terminal has to execute and inspect:

```
DisplayModel.Update     → []Result{openEditorForDisplayMsg} → Terminal.applyResult
DisplayModel.Update     → []Result{focusInputWithValueMsg}  → Terminal.applyResult
PromptInput.Update      → []Result{openEditorForPromptMsg}  → Terminal.applyResult
ThemeSelector.Update    → []Result{ThemeSelectedMsg}        → Terminal.applyResult
ModelSelector.Update    → []Result{ModelSelectedMsg}        → Terminal.applyResult
HelpWindow.Update       → []Result{HelpCmdMsg}              → Terminal.applyResult
AttachmentWindow.Update → []Result{AttachmentSelectedMsg}   → Terminal.applyResult
ConfirmDialog.Update    → []Result{ConfirmResultMsg}        → Terminal.applyResult
```

Terminal folds those results in the same Update (`foldResults` → `applyResult`)
and never reads component internals. `applyResult` is the single place a
result's meaning lives; `Terminal.Update` handles only input events and
asynchronous facts from the runtime and the session.

## I/O Strategy

| I/O Operation | Path | Reason |
|--------------|------|--------|
| `emitCommand` (TLV write) | `Cmd` | Always in Update context |
| `submitCmd` (batch TLV writes) | `Cmd` | Multiple writes, one unit |
| `startMCPAuthFlow` (OAuth) | `Sequence` | Multi-phase: notify → open browser → wait for callback |
| `displayErrorMsg` / `displayNotifyMsg` | `Cmd` → `Terminal.Update` handler | Routes all `WriteError`/`WriteNotify` through the event loop |
| `WriteError` (in Init) | `Batch` of `displayErrorMsg` Cmds | Now goes through Update like all other display writes |
| `StartCallbackServer` | Direct write in Update | Unavoidable — Cmd needs resultCh |

Principle: All I/O in Update goes through `Cmd`. Exceptions are operations
that must happen synchronously because their result is needed before the Cmd
can be created (e.g., `StartCallbackServer` creates the channel that the Cmd
waits on).

## Concurrency Model

```
Cmd        → go dispatch(cmd())             ← goroutine
Batch(a,b) → go a(); go b()                 ← goroutine per Cmd
Sequence(a,b) → a(); b()                    ← event loop, no goroutine
```

- `Batch` is for independent operations (no ordering needed)
- `Sequence` is for dependent operations (e.g., Close before Quit)
- `ExecProcess` (editor handoff) and Ctrl-Z suspend run synchronously in
  the event loop: the terminal is released, the child runs in the
  foreground, then the terminal is re-acquired and repainted (`exec.go`)

## Remaining Differences from Elm

| Aspect | Pure Elm | Our Code | Acceptable? |
|--------|----------|----------|-------------|
| Cmd | Data (inspectable) | `func() Msg` (opaque) | Yes — runtime constraint |
| Same-frame updates | `Cmd` is same-frame | UI facts are `Result` values folded same-frame; `Cmd` is I/O only | Yes — same frame kept, I/O kept async |
| Messages | Sum types, exhaustive | `interface{}` + type switch | Yes — Go limitation |
| Sub-components | `Cmd.map` for type-safe routing | Ordered input layer stack + `Result` fold in Terminal | Yes — Go has no generics for this |
| Immutable syntax | Record update `{ x \| f = v }` | Field assignment on local copy | Yes — equivalent semantics |
