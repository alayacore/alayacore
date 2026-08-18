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

AlayaCore runs its own minimal TUI runtime (`program.go`, REFACTOR.md §8)
that keeps the Elm/Bubble Tea model — `Model`/`Update`/`View`/`Cmd`/`Msg` —
without the framework:

```go
type Cmd func() Msg  // not data — an opaque function
```

| Aspect | Elm | Our runtime | Consequence |
|--------|-----|------------|-------------|
| Cmd | Data (inspectable record) | `func() Msg` (opaque) | Runtime cannot inspect Cmd; renders before executing it |
| Msg dispatch | Sum types, exhaustive | `interface{}` + type switch | No compiler guarantee |
| Same-frame Cmd | Yes — runtime recurses before render | No — renders first, executes Cmd after | Continuous UI events must bypass Cmd to avoid 1-frame delay |

## Architecture Overview

```
Terminal (value type, root model)
├── Update(msg Msg) → (Model, Cmd)     ← single entry point
│
├── Dispatches messages to components:
│   ├── KeyMsg  → handleKeyMsg
│   │   ├── overlay active → overlay.Update(msg)
│   │   ├── Tab → toggleFocus
│   │   ├── global shortcut → handleGlobalKeys
│   │   └── focus-specific
│   │       ├── display → DisplayModel.Update(msg)  ← delegates all display keys
│   │       └── input   → PromptInput.Update(msg)   ← delegates all input keys
│   ├── ThemeSelectedMsg  → emit theme_set command
│   ├── ModelSelectedMsg  → emit model_set command
│   ├── ConfirmResultMsg  → handleConfirmResult
│   ├── HelpCmdMsg        → focus input with command
│   ├── AttachmentSelectedMsg → addAttachment
│   ├── openEditorForDisplayMsg → open editor (display content)
│   ├── openEditorForPromptMsg → open editor (prompt content)
│   ├── focusInputWithValueMsg → focus input and insert text
│   ├── OverlayClosedMsg  → restoreFocus
│   ├── PasteMsg   → handlePaste (attachment window or input)
│   ├── BlurMsg    → handleBlur
│   ├── FocusMsg   → handleFocus
│   ├── WindowSize → handleWindowSize
│   └── default (unknown msg) → stderr log
│
├── Components (each has Update returning Cmd):
│   ├── DisplayModel      Update(msg Msg) → (DisplayModel,     Cmd)
│   ├── PromptInput       Update(msg Msg) → (PromptInput,      Cmd)
│   ├── ConfirmDialog     Update(msg Msg) → (ConfirmDialog,    Cmd)
│   ├── ThemeSelector     Update(msg Msg) → (ThemeSelector,    Cmd)
│   ├── ModelSelector     Update(msg Msg) → (ModelSelector,    Cmd)
│   ├── HelpWindow        Update(msg Msg) → (HelpWindow,       Cmd)
│   ├── AttachmentWindow  Update(msg Msg) → (AttachmentWindow, Cmd)
│   └── InputField        Update(msg Msg) → (InputField,       Cmd)
│
├── Code reuse units (pure functions, no Cmd):
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
- Communicate with Terminal via messages (ThemeSelectedMsg, etc.)
- All have `Update(msg Msg) → (Self, Cmd)`

### Code Reuse Units (FilteredListCore)
- Cannot exist independently — embedded into components
- Have `HandleKey(msg KeyMsg) → (Self, Result)` — no Cmd
- Used for continuous UI operations (scrolling, filtering) where
  a 1-frame delay from Cmd routing would cause perceptible lag
- This is NOT a hack; Elm does the same thing with pure helper functions.
  The difference is that Elm's Cmd system is same-frame, so the optimization
  is unnecessary there. In our runtime, Cmd execution adds 1 frame delay.

## Message-Based Communication

Components communicate with Terminal through messages, not by returning
result structs that Terminal reads:

```
DisplayModel.Update     → Cmd(openEditorForDisplayMsg) → Terminal.Update handles it
DisplayModel.Update     → Cmd(focusInputWithValueMsg)  → Terminal.Update handles it
PromptInput.Update      → Cmd(openEditorForPromptMsg)  → Terminal.Update handles it
ThemeSelector.Update    → Cmd(ThemeSelectedMsg)          → Terminal.Update handles it
ModelSelector.Update    → Cmd(ModelSelectedMsg)          → Terminal.Update handles it
HelpWindow.Update       → Cmd(HelpCmdMsg)               → Terminal.Update handles it
AttachmentWindow.Update → Cmd(AttachmentSelectedMsg)    → Terminal.Update handles it
ConfirmDialog.Update    → Cmd(ConfirmResultMsg)         → Terminal.Update handles it
```

Terminal does NOT read component internals. It only handles messages
in its own Update switch.

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
| Same-frame Cmd | Yes (recursive before render) | No (render before exec) | Yes — runtime limitation |
| Continuous UI | Cmd is fine (same-frame) | Pure `HandleKey` (bypass Cmd) | Yes — necessary optimization |
| Messages | Sum types, exhaustive | `interface{}` + type switch | Yes — Go limitation |
| Sub-components | `Cmd.map` for type-safe routing | Flat switch in Terminal | Yes — Go has no generics for this |
| Immutable syntax | Record update `{ x \| f = v }` | Field assignment on local copy | Yes — equivalent semantics |
