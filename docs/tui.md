# Terminal UI

AlayaCore's terminal UI is built on a self-hosted minimal TUI stack (own
event loop, key parser, terminal management, and style layer) and uses
vim-like keybindings throughout. See `docs/tui-architecture.md` for the
architecture.

## Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down |
| `k` | Move window cursor up |
| `↓` / `J` | Scroll down one line |
| `↑` / `K` | Scroll up one line |
| `Ctrl+D` / `PgDn` | Scroll down half screen |
| `Ctrl+U` / `PgUp` | Scroll up half screen |
| `g` / `Home` | Go to first window, scroll to top |
| `G` / `End` | Follow the last window |
| `H` | Move cursor to top window in visible area |
| `M` | Move cursor to middle window in visible area |
| `L` | Move cursor to bottom window in visible area |
| `f` | Jump to next user prompt and put it at the top; leaves the view alone when there is none |
| `b` | Jump to previous user prompt and put it at the top; leaves the view alone when there is none |
| `e` | Open window content in external editor |

Two families, and they do not overlap: **the home row moves the pointer**
(`j`/`k`, and the vim names `H`/`M`/`L`, `g`/`G`, `f`/`b`), **the arrows and the
coarse keys move the viewport** (`↑`/`↓` one line; `Ctrl+U`/`Ctrl+D` and
`PgUp`/`PgDn` half a screen — all four share one handler, so a Page key moves by
half a screen here, which is what its table row says). `J`/`K` are the viewport
motion spelled on the home row, so shift carries exactly one meaning in this
table: leave the pointer, move the screen.

Four keys serve both families: `g` and `Home` put the pointer on the first window
and take the view with it, `G` and `End` do the last.

The arrows are bound to the viewport rather than copied onto `j`/`k` because of
the mouse wheel. While this program owns the alternate screen it asks for no
mouse reports (`screen.go` → `mouseReportingOff`), which is also what leaves
click-and-drag text selection with the terminal — the trade this UI refuses to
make. A host that lets the wheel reach the application at all in that state does
it by writing the arrow keys' own bytes, `ESC [ A` and `ESC [ B`, into the input
stream: gesture and keystroke are then literally the same message, there is no
second thing to bind, and whatever the arrows do *is* the wheel's behavior — one
line per arrow a host produces for a notch, releasing auto-follow on the way up
and staying inert at the live edge on the way down. Bound to the cursor instead,
that same stream is a motion auto-follow refuses at the live edge (rolling down
appears to do nothing) and one that steps whole windows at a time (rolling up
jumps): the feel this replaced.

Where a host synthesizes nothing for the wheel, nothing here changed for it: it
still sends no wheel input, and `Ctrl+U`/`Ctrl+D` and `PgUp`/`PgDn` remain the
coarse (half-screen) motions. Taking the wheel as a report instead (`key_parser.go` decodes
`CSI < Cb;Cx;Cy M` today in order to drop it) would reach more hosts and would
cost the terminal's own selection, so it is not on the table.

One consequence for the table above: `shift+up`/`shift+down` are now decoded and
bound nowhere, so no binding in this UI asks a terminal to report a *modified*
arrow — which is not what a wheel synthesizes anyway.

A list overlay takes no arrows at all, and that is the same rule, not an
exception: a picker's only degree of freedom is its selection — `ScrollIdx` is
derived from it (`filtered_list.go` → `EnsureVisible`), so there is no viewport
an arrow could move, and binding the arrows there would bind one state twice.
`j`/`k` navigate, `Enter` selects, `Esc` closes, `Tab` swaps between the filter
input and the list. Two consequences are wanted rather than tolerated: the wheel
is inert while an overlay is open (it arrives as the arrows, and the arrows
answer to nothing here), and `q` no longer closes a picker — it was a convention
stacked on top of `Esc`, and one key per job is why `Esc` is the only way out.

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt |
| `Ctrl+J` | Insert a line break in the prompt |
| `Ctrl+S` | Save session |
| `Ctrl+O` | Open in editor (`$EDITOR`) for multi-line input |
| `Ctrl+L` | Open model selector |
| `Ctrl+R` | Force redraw screen |
| `Ctrl+P` | Open theme selector |
| `Ctrl+H` | Open help window |
| `Ctrl+G` | Cancel current task (with confirmation) |
| `Ctrl+Z` | Suspend process |
| `Ctrl+C` | Clear text |
| `Ctrl+F` | Fork session from cursor position |
| `Ctrl+A` | Open attachment picker for multi-modal input |
| `:` | Switch to input with `:` prefix (command mode) |
| `Space` | Toggle window fold (expand/collapse) |
| `r` | Toggle markdown rendering (unfolded assistant text/reasoning) |

### Why `Shift+Enter` is not the line break

`Enter` is CR. On most terminals `Shift+Enter` is *also* CR: the modifier never
reaches the program, so there is no second thing to bind — binding it would name a
key that never arrives.

A terminal can report the difference, but only in an encoding the program has to
understand and, usually, ask for:

| Where the state exists | What it looks like |
|---|---|
| xterm with `modifyOtherKeys` | `ESC [ 27 ; 2 ; 13 ~` |
| kitty / WezTerm / Ghostty (kitty keyboard protocol) | `ESC [ 13 ; 2 u` |
| A Windows console read as *events* (`console_events.go`) | `VK_RETURN` with `SHIFT_PRESSED` — the legacy host reports it; the pseudo console hands over bytes, so under Windows Terminal the state arrives only if that host's richer input mode is on |

`key_parser.go` discards both wire forms today — the `27` parameter is explicitly
`not supported`, and `u` is not a final byte it knows — so a `shift+enter` binding
added on top of the current parser would do nothing even on a terminal that reports
the key. Supporting it means parsing those two forms and (for the protocol ones)
requesting the capability in `Screen.Start` with the matching reset, the way
bracketed paste is handled: a capability that degrades silently where the host
lacks it.

`Ctrl+J` asks for nothing of the kind. LF is a byte, the Enter key never produces
it, and it is already what a pasted line break is — which is why it is the line
break, and `Ctrl+O` (a real editor) the answer for anything long enough to want
one.

### Input Cursor & IME

The prompt input (and overlay filter boxes) render the **real terminal cursor** (the emulator's default steady block — themes do not configure cursor color, since the `cursor` field was dropped from `Theme` when body content rendering stopped carrying an explicit foreground color). This keeps input behavior identical to a shell prompt: Chinese/Japanese IME composition draws its inline preedit directly in the input field and the candidate window anchors to the input line, so it does not jump around while streaming output is being rendered.

### Paste and terminal capability

Bracketed paste (DEC private mode 2004) is a **terminal capability**, not a
program feature: the app opts in when it starts the screen
(`screen.go` → `SetModeBracketedPaste`), and a terminal that implements it
wraps clipboard content in `ESC [ 200~` … `ESC [ 201~`. The parser reads
whatever sits between those markers as data instead of as keystrokes
(`key_parser.go` → `PasteMsg`), and the input field normalizes `\r\n`/`\r`/`\n`
to `\n`, drops other control characters, and trims trailing newlines
(`input_field.go` → `blockText`). Windows Terminal, and xterm / VTE /
alacritty / kitty and friends on Unix, all take this path.

`blockText` is the rule for any text that arrives as a *block* rather than as
keystrokes, so the finished buffer of an external editor goes through it too
(`input_field.go` → `WithBlockValue`, reached from `tui.go` →
`handleEditorFinished`). That matters most on Windows, where both
sources use CRLF: notepad always, and vim by default for a file it creates.
Before the rule was shared, a prompt composed in `notepad` came back carrying
CRs, which the terminal reads as "column 0" — the frame painted over itself.

A field is one line or many, and the rule in `blockText` is only half of it: a
field that cannot hold a line break strips the ones a block carries
(`input_field.go` → `stripLineBreaks`), so a multi-line paste into an overlay
filter cannot leave a newline in a value that matches no item, and the
attachment box — which uses its value as a path — cannot store one either. The
prompt is the one multi-line field (`NewMultilineInputField`); every other text
box is single line by construction, which is why the prompt is the only box whose
border turns to the warning color when a draft spans more than one line.

A paste is bounded by its markers and by nothing else, so those two sequences have
to survive the shape the bytes arrive in. A read stops where the platform stops it
(`inputReadSize` bytes on Unix, `consoleEventsPerRead` events on Windows), so a
block longer than that is cut at an offset nobody chose, and the cut can land
inside the closing marker. Paste content is read without looking for structure in
it, so the head of a split marker is indistinguishable from the text in front of
it — and text is where it used to go, which is a paste that never closes: the
parser stays in paste mode and takes every keystroke that follows as pasted text.
The head is now held for the next read (`key_parser.go` → `takePaste`) and
completed by it, exactly as any other sequence a boundary splits, and the input
loop's silence timeout resolves the marker that never does arrive — the paste
delivered, the head dropped as the unknown sequence it is, paste mode left. That
timeout arms on *any* unfinished input (`InputParser.MidSequence`), not only on a
held marker head: a paste whose body never resembles the closing marker is
unfinished just the same, and only the state test covers it. The sweep over every
cut offset and every read size is `key_parser_paste_cut_test.go`; the loop's half
is a row in `program_input_cut_test.go`.

Where a paste lands is decided by the pane the user focused, not by the terminal
window's OS-level focus. That focus is reported too (DEC mode 1004, enabled by
`Screen.Start`, arriving as `FocusMsg`/`BlurMsg`), and it is a fact about drawing:
the real caret goes away (IME anchors on it) and the box takes its blurred colors.
It is not a fact about who arriving text is for, and the program cannot tell the
two apart. Where they disagree is the terminal's own **context menu**: a host that
reports that menu as a focus change has already said "unfocused" by the time the
menu hands the clipboard back, and the paste lands inside the blur. Which hosts
report it is a property of the terminal, not of this program, and is recorded as
unverified in [windows-console.md](internal/windows-console.md) rather than
asserted here — the gate below is removed on the general rule that input arriving
on this program's own pty is input addressed to it, whatever the window's
painting state says. Gating the input path on the blur made "Paste" from that menu
delete the block silently, while a middle-click paste — no menu, no blur — worked,
and the same menu paste worked in the attachment window's URL box, whose filter
gated on the pane only.

So the routing answer lives in exactly one place, derived from state the user
changed and nothing else: `Terminal.keyboardTarget` (`tui_focus.go`) resolves
prompt / display / overlay filter / overlay list / modal / loading, and every text
message is dispatched once against it. No box carries a flag that both routes its
input and paints its frame — that pair of jobs on one bool is what let a
cosmetic event (the commit that added focus handling said "to dim UI") delete a
paste for two years. The window's focus is now worth painting only: the blurred
border and text register, and the real caret (`View` derives
`PromptInput.WithActive` from target and focus each frame, so the two can never
drift). Which means `input_routing_test.go` can hold the whole model as a table:
ten states × paste and typed text × window focused and unfocused, and the states
that take nothing are discarded because that is a decision the table records, not
because a box happened to be looking inactive.

A terminal that does not implement mode 2004 gives the program no markers, and
there is no way to ask for them: `GetConsoleMode` succeeds on every Windows
console host whether or not it implements paste, and the standard query
(DECRQM mode reporting) is unanswered by the ones that do not — inferring the
answer from a missing reply means guessing on a timer. So pasted bytes arrive
as ordinary keystrokes, and what happens depends on which byte that terminal
uses for line endings:

- **LF line endings** (every non-Windows host): LF *is* Ctrl+J, which the
  prompt binds to "insert a line break". A pasted block therefore lands in the
  input with its line structure intact and **nothing submits** — press `Enter`
  to send the whole block as one prompt.
- **CRLF line endings** (legacy Windows console host — `cmd.exe`, Windows
  PowerShell, and `pwsh` in a plain console window): Windows clipboard text
  ends its lines with `\r\n`, and CR *is* the Enter key. Each line in a pasted
  block submits in turn: the first line goes out as the prompt, the rest hit the
  running-task rejection (`keybinds.go` → `handleSubmit`, and the session's own
  `BUSY` answer in `agent/session_io.go`), leaving their text in the input box.
  Nothing is sent twice and no tool runs without the model asking, but the
  prompt that arrives is not the block the user pasted.

That last case is not fixable from inside the program without inferring intent
from the timing of incoming bytes — there, the same CR is genuinely both
content and command, and the app does not guess which one the user meant. Three
options that work regardless of host, in order of preference:

| Option | Why it is terminal-independent |
|---|---|
| Run in **Windows Terminal** | implements bracketed paste, truecolor, and the rest of the VT set; it is the default host on Windows 11 |
| `Ctrl+O` — compose in `$EDITOR` | the editor owns the text; only the finished buffer is read back |
| `Ctrl+A` — attach as a file | the content travels as an attachment frame, never as keystrokes |

On a host without the markers, pasted text is processed one keystroke at a
time, so a large block arrives as many key messages: correct, but slower than
the bracketed path.

## Windows Consoles

The TUI writes ANSI escape sequences, and on Windows that is a negotiated
capability, not a given: a console screen buffer ignores them until the
application asks, so AlayaCore asks on entry
(`console_windows.go` → `enterVT`, from `TTY.MakeRaw`):

| What it does | Why |
|---|---|
| Sets `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on the output handle | without it the alternate-screen, cursor-positioning, and color sequences are written to the screen as visible text |
| Sets `DISABLE_NEWLINE_AUTO_RETURN` | the renderer emits `\r\n` and expects a bare LF to move down without returning to column 0, as on every other terminal |
| Clears `ENABLE_QUICK_EDIT_MODE` (with `ENABLE_EXTENDED_FLAGS`) | QuickEdit is on by default in `cmd` and PowerShell, and a single click inside the window enters select mode, which suspends the program's writes: the UI freezes mid-frame |
| Turns every mouse-tracking mode off (`screen.go` → `Start`/`Stop`) | the UI has no mouse handling, so the terminal must not *report* the mouse. The mode belongs to the session rather than to this process: any program that ran earlier in the same terminal can leave one set, so the reset makes the program independent of what it inherited. It is also the only answer for a report whose introducer is stripped *before* this program sees it — `Cb;Cx;CyM` on its own cannot be told from typing — and it gives the terminal's click-and-drag selection back to the user. The reported symptom's other half was the reader, not the terminal: a read boundary cut the report and the old flush dropped its head, which [the input loop's note](internal/windows-console.md) records; the parser's share (consuming a reassembled report, and the OSC/DCS/SOS/PM/APC string controls, whose introducers are *held* rather than resolved to an Alt chord so that a cut cannot leak a body) is pinned case by case by `key_parser_reports_test.go` and as a property by `key_parser_read_invariance_test.go` — every stream parsed in reads of every length must deliver what the same bytes deliver parsed at once |
| Puts all of it back on exit | the shell keeps using that screen buffer; leaving `DISABLE_NEWLINE_AUTO_RETURN` set would corrupt *its* output afterwards |
| Reads the console as *events* and turns them into the byte sequences a terminal sends (`console_events.go`) | a byte read on a console handle waits for a whole line in cooked mode and cannot reliably be cancelled, so it is the one thing that must not be pending while a child owns the keyboard — see [the editor handoff](internal/windows-console.md#what-the-two-reports-were) |
| Saves the cursor before entering the alternate screen, and restores it after leaving (`screen.go` → `Start`/`Stop`) | not Windows-specific, but it is why quitting cannot leave the caret in the middle of the shell's screen. The teardown moves to the last row, erases the screen it is leaving, switches back, then restores — so the shell is never left repainting from "caret mid-screen in an un-erased alternate buffer" |

Because the console reports keys as structured events, the key names this
application binds are produced by a table in `console_events.go` rather than by the
console's own VT translation. That table is xterm's vocabulary — the same one
`key_parser.go` reads from every Unix terminal — so `Ctrl+O`, `shift+up`, `home`,
`backspace`, and a CJK or emoji commit mean the same thing on both platforms, and
the agreement is asserted end to end (encoder → parser → key string) in
`console_events_test.go`, which runs on every platform rather than only on Windows.
Bracketed paste is unaffected by the change: where a terminal frames clipboard
content with the two markers, they reach this program as characters, and the
encoder passes characters through untouched (pinned by the same test file).

Because it is the same `MakeRaw`/`Restore` pair that enters and leaves raw
mode, re-acquisition (returning from `$EDITOR`, `Ctrl+O`) re-negotiates the
mode: a child that owned the console restores it to its own default, which has
no ANSI bit in it.

What that means per host:

| Host | Behavior |
|---|---|
| **Windows Terminal** (`wt`, default on Windows 11) | Nothing to enable — sequences are always processed. Bracketed paste, truecolor, and cursor-shape control all work |
| **Legacy console host** (`cmd.exe`, Windows PowerShell, `pwsh` in a plain console window) | Alternate screen, cursor addressing, erase, colors, and resize work. Two things that host does not implement at all: cursor *shape* control (`DECSCUSR`) is ignored, and pasted multi-line text submits per line — see [Paste and terminal capability](#paste-and-terminal-capability) |
| **No console at all** (service, detached process, redirected output with no `CONOUT$`) | Startup fails with an error naming `--plainio`, rather than painting escape codes onto a stream that will not read them |

On color: the style layer emits 24-bit truecolor (`38;2;r;g;b`), byte-pinned by
`style_test.go`, and there is deliberately no color-profile ladder — no
downgrade to 256 or 16 colors, no terminal-capability negotiation. What a
legacy console host does with a `38;2` sequence it does on its own: rendering
it, or substituting from its own palette. Which of those happens on which
Windows build is on the unverified list in
[windows-console.md](internal/windows-console.md), not a promise this document
makes.

`TERM` is not consulted anywhere: it is absent on Windows and uninformative on
the rest, and the checks above are made against the handle, not the
environment.

## Multi-Modal Attachments

AlayaCore supports multi-modal input — attaching images, audio, video, or documents alongside text. Attachments are sent as TLV frames **before** the text frame, all within a single `TagUserEnd`-delimited message:

```
[TagUserI/V/A/D frames...] + [TagUserT text] + [TagUserEnd]
```

### Attachment Picker

Press `Ctrl+A` to open and toggle the attachment picker overlay. Two modes are available:

**Local Mode** (default):
Browse and select local files via a file browser with fuzzy search.
The input field shows the directory being listed, ending in the separator that
opens the segment being typed — `/` on Unix, `\` on Windows, where either
separator is accepted when read.
Type a path fragment to filter files, or type a new absolute path to navigate.

| Key | Action |
|-----|--------|
| `Tab` | Toggle focus between path input and file list |
| `j` | Move selection down |
| `k` | Move selection up |
| `Backspace` | Delete one character, as in every other box |
| `Ctrl+W` | Delete the last path segment (`/abc/def/` → `/abc/`, `C:\a\b\` → `C:\a\`); the root survives |
| `Enter` on dir | Append directory name to path input |
| `Enter` on file | Add file as attachment and close |
| `Ctrl+A` | Switch to URL mode |
| `Esc` | Close picker without adding |

**URL Mode**:
Enter a remote URL to attach as an attachment.

| Key | Action |
|-----|--------|
| `Enter` | Add the URL as attachment and close |
| `Ctrl+A` | Switch to local mode |
| `Esc` | Close picker without adding |

`Ctrl+W` — not a modified Backspace — carries the segment delete, because a
control byte is the only kind of chord that reliably reaches a program:
`Shift+Backspace` *is* a plain Backspace on most terminals, `Ctrl+Backspace`
arrives as `Ctrl+H` (which opens the help window here), and the encodings that
could tell them apart — the kitty keyboard protocol's `ESC [ 127 ; 2 u`, xterm's
`modifyOtherKeys` — are the ones [documented
below](#why-shiftenter-is-not-the-line-break) as not read by `key_parser.go`.
An Alt-prefixed key does survive the trip — as `ESC` plus the character — for every character that cannot begin a terminal reply, and for nothing else (`key_parser.go` holds `ESC ]`, `ESC P`, `ESC X`, `ESC ^`, `ESC _` as replies in progress, which is affordable precisely because no Alt chord is bound). `Ctrl+W`
is what readline uses to kill the word before the cursor, and in a path box the
word is the segment. It is bound in this picker only; the prompt's own `Ctrl+W`
does nothing.

The path input is a bare input like every other overlay filter — no prompt prefix (the old `F`/`U` markers were removed). The current mode is discoverable from the help bar (`ctrl+a: switch to URL` / `switch to local`). File list rows render flush left, no `> ` marker; the selected row is **bold** in the default foreground while the others are muted.

### Attachment Types

The attachment type is determined by file extension (or URL path extension):

| Type | Icon | TLV Tag | Extensions |
|------|------|---------|------------|
| Image | 📷 | `UI` | `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`, `.bmp`, `.svg` |
| Video | 🎬 | `UV` | `.mp4`, `.mpeg`, `.mpg`, `.avi`, `.mov`, `.webm`, `.mkv` |
| Audio | 🎵 | `UA` | `.mp3`, `.wav`, `.ogg`, `.flac`, `.aac`, `.m4a`, `.wma` |
| Document | 📄 | `UD` | `.pdf`, `.txt`, `.md`, others / unknown |

### Display

Attachments render above the text — the media block first, the text directly
under it — beneath the window's own line (marker + label; see
[Window Container](#window-container)):

```
- USER PROMPT             2026/09/14 16:32:07 +08:00
📷 Image  🎵 Audio
what are these?
```

Nothing divides those two rows. The media block is header material — the
terminal's default color, bold, one of four fixed labels — and the text under
it is plain body: same color, told apart by weight. So the boundary is already
drawn; the `───` rule is reserved
for the joins that would otherwise read as one continuous run, which is two
text parts of one message and a tool window's arguments versus its result. The
prompt box works the same
way, and grows upward rather than downward: its top rule is anchored from the
bottom of the screen, so attaching a file costs the transcript a row and never
moves the caret.

Collapsed (`Space`), the same window keeps the attachments as a compact badge
summary and shows the text tail after it — two images and one audio here. The
badges keep the register they hold unfolded (bold, in the terminal's default
color — deliberately not the window line's muted color, which would make them
read as part of the tag): folding a window restyles nothing. When the row is
too narrow and truncation
cuts into the summary itself, the whole thing stays in the plain content color
rather than painting half a bold badge:

```
+ USER PROMPT     📷2 🎵1 analyze this
```

The label column is padded to `CollapsedLabelWidth` by `padLabel`, the fold
marker is prefixed by the window layer, and the tail is cut by
`tailCells`/`tailParts` (see
[Collapsed Summary Truncation](#collapsed-summary-truncation)). Expand the
window (`Space`) to see the full attachment labels and content — the marker
becomes `-`, the timestamp appears on the right, and the content follows.

### Sending

When you press `Enter`:
- Local files are read, base64-encoded into `data:` URIs, and sent as TLV frames
- URLs are sent as-is (no fetching)
- Text is sent as a `TagUserT` frame
- A `TagUserEnd` frame finalizes the message

Attachments are cleared after sending. Use `Ctrl+C` to discard both text and pending attachments without sending.

## Session Commands

See [commands.md](commands.md) for the full list of session commands (`:save`, `:cancel`, `:fork`, etc.).

Note: `:quit` / `:q`, `:cancel`, `:help`, and `:suspend` are intercepted by each adapter where supported — see [commands.md](commands.md#adapter-specific-commands) for the per-adapter table. In the terminal, `:quit`/`:q` and `:cancel` open a confirmation dialog: confirming quit exits the process, confirming cancel forwards `:cancel` to the session; `:help` (opens the help window) and `:suspend` (suspends the process) are handled locally and never reach the session. Elsewhere: plainio intercepts `:quit`/`:q` locally — it waits for any running task, then exits with code 0 — while `:help` and `:suspend` are not adapter-handled, so `:help` is sent to the session as a regular command; terseio treats stdin as prompt text unless it starts with `:` — then the whole input is sent as a single command (and `:quit`/`:q` are intercepted locally for a clean exit); rawio passes all commands through as raw CI frames since it doesn't interpret frame payloads.

## Window Container

The display area organizes content into separate windows — one per message or tool call. Windows have synchronized widths and can be navigated independently.

Every window spends exactly ONE row of chrome — its own line — and it never draws a border. That line opens with the fold marker (`+` folded, `-` expanded), then the label in the fixed `CollapsedLabelWidth` column, so the label sits at the same cell in both states and folding never moves it. What follows the label is what tells the two states apart: the content summary when folded, the arrival timestamp right-aligned to the window edge when expanded — with the message's hidden-line count beside it while that row is pinned to the screen top ([Sticky Window Line](#sticky-window-line)). Nothing delimits a window's end: the next window's own line does, and the last one is closed by the live-edge row and the prompt box under it. A *floating* surface instead brackets its content with top and bottom rules — the prompt box around the draft (and, when files are attached, the badge rows above it), a selector overlay around its filter box. In a selector, that box's closing rule is the only divider between the search and the bare list under it, and the help bar closes the overlay below.

### Window Order

A window's position in the list is fixed at creation time: `WindowBuffer` only ever appends, and `HistoryID` is stored for navigation, never used to re-sort. So the display order is the order in which windows were *first created*.

Streaming text deltas (`At`/`Ar`) do not touch the buffer as they arrive — they accumulate in `pendingTextDeltas` and are written in one batch by `flushPendingDeltas`, which runs when an authoritative frame arrives or on a UI tick. A short reasoning and the answer that follows it are therefore often pending together, and because the flush used to iterate a Go map (randomized order per run), the `REASONING` window could be created below the `ASSISTANT` window it preceded — for the rest of the session. `flushPendingDeltas` now sorts the pending entries by `historyID` first, tool deltas included, so a step's windows are created in one deterministic order instead of one coin flip per pending entry. `delta_flush_order_test.go` pins it for both frame orders.

The sort reproduces *arrival* order, not a ranking of block kinds: in delta mode `historyID` is handed out on first delta touch, so a provider that genuinely streams its answer before its reasoning numbers the answer lower and renders it first. It is not "reasoning always above text". Elsewhere in the tree `historyID` is never compared across records — the numeric uses are per-window max-tracking and a `> 0` validity guard, and `handleFork` resolves an ID to an index and slices positionally — so this flush is the only place treating ID magnitude as an ordering signal, and it depends on providers numbering blocks in the order they are persisted. That obligation, and the one arrival-order case no provider-side ordering can satisfy, are in [providers.md](providers.md) → "Complete-event order".

### Tool Status Indicator

Every tool window's line carries a status indicator right after the `TOOL CALL` label (`TOOL CALL ⠋`, `TOOL CALL ✓`, `TOOL CALL ✗`), separated by one space, and then the tool name — folded (`+ TOOL CALL ⠋     execute_command uname -a`) and expanded (`- TOOL CALL ⠋     execute_command …`) alike, so the same window reads the same way in both states. The label and the indicator share the line's single style; the name takes the tool-content color (bold) — the same one in both fold states, so folding a window does not repaint it — and only the arguments after the name are plain content. While arguments are still streaming in and while the tool is executing, the indicator is the same braille dot-segment spinner used by the session-loading screen and drawn by the status bar's indicator column (whose idle cell is the still braille cell `⠿`, the union of those frames — see [Glyphs and Terminal Width](#glyphs-and-terminal-width)). The glyph is a pure wall-clock function (10 frames × 150ms), so it advances with every header re-render; re-renders come from two sources — incoming deltas (`Af` argument chunks, `Uf` execution previews) and, while a tool executes with no output, a tick-driven invalidation that keeps the spinner rotating even during silent commands (see `WindowBuffer.InvalidateRunningToolSpinners` and [tool-spinner-refresh.md](internal/tool-spinner-refresh.md)). There is no dedicated animation timer. When the tool finishes, the spinner is replaced by a check mark (`✓` on success) or cross (`✗` on error); as a safety net, any tool window still running when the task ends is settled to `✗` (see `WindowBuffer.SettleUnfinishedTools`). The indicator shares the `TOOL CALL` label color (muted + bold) so `TOOL CALL` + indicator + tool name read as a single colored unit — the indicator carries no semantic color of its own. The one place the three part company is the cursor register: the highlight recolors the label and the indicator and leaves the name muted, exactly as it does when the window is expanded ([Fold Mode](#fold-mode)).

### Tool Result Separator

Tool windows separate the tool call's arguments from its result with a dimmed `───` rule. The arguments are shown without the status indicator or the `name: ` prefix (both live on the window's own line), so a window reads: its own line (`- TOOL CALL ⠋ execute_command`), argument line (`lscpu | grep …`, or `./scripts/fetch.sh [dir=/home/me/skills/weather]` when the call carried a `workdir`), `───`, result. `write_file` and `edit_file` follow the same layout. Only `edit_file`'s argument block is a real diff: the removed rows (`- `) render in the theme's removed color and the added rows (`+ `) in the added color (each wrapped continuation row stays self-contained); context rows and the bare argument line stay plain. `write_file` shows the raw file content being written — plain, never diff-colored (`- `/`+ ` lines there are literal content). While an overlay (model selector, help window, confirm dialog, …) is open, all this body text dims to the theme's `dim` color — see [configuration.md](configuration.md).

### Auto-Follow

Auto-follow is enabled by default at startup. When enabled, the viewport
automatically scrolls to keep the newest content visible as it arrives.

Auto-follow is disabled by any navigation that actually moves the cursor or
scrolls the viewport. While auto-follow is active:
| Key | Behavior | Disables auto-follow? |
|-----|----------|-----------------------|
| `G` | Follow the last window | ✅ Re-enables |
| `j` | Move cursor down | ❌ No-op (race protection) |
| `L` | Move cursor to bottom | ❌ No-op (race protection) |
| `↓` / `J` | Scroll down one line | ❌ No-op when at bottom |
| `Ctrl+D` / `PgDn` | Scroll down half screen | ❌ No-op when at bottom |
| `k` | Move cursor up | ✅ If cursor actually moves |
| `H` | Move to top of visible area | ✅ If cursor actually moves |
| `M` | Move to center of visible area | ✅ If cursor actually moves |
| `f` | Jump to next user prompt | ✅ If cursor actually moves |
| `b` | Jump to previous user prompt | ✅ If cursor actually moves |
| `g` / `Home` | Go to first window | ✅ If cursor actually moves |
| `↑` / `K` | Scroll up one line | ✅ Always |
| `Ctrl+U` / `PgUp` | Scroll up half screen | ✅ Always |
| `e` | Open in editor | ✅ Always |
| `Space` | Toggle window fold | ❌ Never |
| `r` | Toggle markdown rendering | ❌ Never |
| `Tab` | Toggle focus | ❌ Never |

### Sticky Window Line

While a window taller than the viewport is scrolled through, its own line —
marker, label, timestamp, and a count of the lines of that message that are
hidden above the screen — stays pinned to the **top row of the screen** for as
long as any of its body is still visible below. Reading the middle of a long
answer, you keep seeing whose answer it is, and how far into it you are:

```
- ASSISTANT                  14 lines above | 2026/09/14 16:32:07 +08:00   ← pinned
…the part of the answer you have scrolled to…
```

The count is the **window's own**, never the session's: it says "there are 14
more lines of this message above you", which is the question the pin raises. It
counts the body rows the pin did not draw — those between the message's own line
and the fragment's first row, including the row the pin displaced — so it is 1
at the first row that scrolls off and grows as you descend. Nothing about how
much transcript is above the message can change it. `lineCountText` spells it
(and the live edge's "N lines below") out, so the two phrasings cannot drift;
they count different things and the words say so: the live edge counts document
lines under the viewport (any window), this counts one window's lines above it.

Its own field is separated from the timestamp by ` | `, which is how this UI
separates fields on a chrome row: the status bar (`renderStatusSegments`) and
the help bars are joined the same way, and the glyph policy lists that ASCII
`|` with them (status bar, help bars — one cell, no waiver). The separator
takes the row's own style, like the count and the timestamp around it: the
fields read apart by the two spaces that bracket the bar, not by a second
color — a chrome row never mixes two colors.

Like the timestamp, the count is metadata: it is drawn only when the label
leaves room for it *and* for the timestamp, and it yields first — the timestamp
is the anchor it needs, and a row too narrow for both keeps the timestamp, while
a row too narrow for the timestamp keeps the label. See
[the timestamp](#the-timestamp) for the widths.

The pin appears the moment the line is cut off (`windowStart < yOffset`) and
disappears when the window's last row would be the only thing left above the
body: an orphan header with nothing under it is never drawn. At that point the
scroll position hands over to the next window, whose own line opens it.

**The pinned row displaces the body row that would have been at screen row 0**
— it is the top row's content that gives way, not a row of the frame, so the
frame still spends exactly `viewportHeight` rows and the newest content stays
visible at the bottom. The row that scrolls off the top and the row that
arrives at the bottom are still exactly one line apart.

**It is a screen-space composite and nothing else.** `lineHeights`,
`totalLines`, the caches keyed on them and `ScrollView` are untouched: the
document geometry must not depend on the scroll position, or clamping and
cursor visibility would depend on themselves.

The pinned row is the one row on screen whose text depends on where the viewport
is, so it is not the cached `lines[0]`: it is memoized on the count it carries,
in the window's render cache, and cleared with it. A frame that does not move the
viewport therefore re-reads it — 46 allocations with the pin and 48 without,
`TestStickyPinAddsNoAllocations` — while a frame that moves the viewport by a
row rebuilds that one row and nothing else: `BenchmarkStickyLineViewportRender`
measures 2012ns pinned-still and 3057ns pinned-scrolling against 2024ns
unpinned, and `TestStickyPinCostIsIndependentOfTheWindowSize` holds a 400-row
message to the same allocations as a 40-row one. `sticky_line_test.go` pins the
behaviour, the boundaries and the cost.

### Live Edge

The state reads on the **live edge** — the one row between the last message
and the input box's top rule, which is where the newest line arrives. It says
`- following -` while auto-follow is on, counts what the viewport hides
otherwise (`- 12 lines below -`), and stays blank when it has nothing to say
(scrolled back to the last line: the whole transcript is on screen).

It counts **document** lines under the viewport, whatever window they belong to
— a total, not a per-message figure; the pinned row's `N lines above` is the
other kind, counting one message's own hidden lines (see
[Sticky Window Line](#sticky-window-line)). Both are spelled by
`lineCountText`, which is what keeps "1 line below" and "1 line above" from
being written differently in two places.

The row belongs to the layout whether or not it carries text.
`updateDisplayHeight` reserves it (`liveEdgeRows`), because a row that appeared
and disappeared with the state would shift the viewport by a line on every flip
and take the frame's height with it — the content must soft-wrap to exactly the
screen height. The label is lowercase and dim: uppercase is this UI's block
heading (`USER PROMPT`, `TOOL CALL`, padded to `CollapsedLabelWidth`) and a
heading here would be read as one more window title; dim is what the rules and
borders are drawn with, so on the row directly above the input box's rule the
marker recedes into the frame rather than competing with it — deliberately the
quietest thing on screen, since it is on most of the time. The frame is an ASCII
hyphen, so the row is one cell in every terminal. No bold either: the running
task is marked by the spinner's motion, not by a color. `live_edge.go` renders it, `live_edge_test.go`
pins it.

It used to be a `F↓` segment at the start of the status bar — and that row is
*below* the input box, so the arrow pointed down at the prompt while the fact it
reported was above it, and the `F` asked the reader to already know it meant
follow. Its glyph, U+2193, was also the only entry on the width-waiver list
carrying its own "candidate for an ASCII replacement" note (see
[Fold Mode](#fold-mode)); the frame is now ASCII too, so the whole row draws no
waived glyph. Reading the state while rendering, instead of baking it into the
cached status string, retired a staleness patch along with it: navigation moves
no session status version, so the segment kept showing the previous state until
the next session event (`TestLiveEdgeReadsStateAtRenderTime`).

### Fold Mode

Windows arrive expanded when they are the conversation (assistant answers and the user's own prompts) and collapsed when they are the machinery around it (reasoning steps, tool calls, system messages), so a step's scaffolding does not push the conversation off the screen.

Press `Space` on any window to collapse it — the window becomes one line: the fold marker `+`, the label (`TOOL CALL` + status indicator, `REASONING`, `ASSISTANT`, `USER PROMPT`, `SYSTEM NOTIFY` for system notifications, or `SYSTEM ERROR`) and a content summary. Labels are left-justified to a fixed column so summaries align across window types (tool windows show `TOOL CALL` + indicator followed by the tool name + arguments). The marker marks the state — `+` folded, `-` expanded — and the label lands in the same column either way, so folding never shifts the text the eye is scanning down.

**The line is one style**, with one exception. The marker, the label and the timestamp — everything that names the window — are painted with a single `Style`: `lineStyleForTag`'s, bold, in the label color (muted) for **every** window type, because a user's turn, a reasoning step, an answer, a tool call and a system notification are all the same kind of thing — conversation — and none of them earns its line an accent. The single color exception is `SYSTEM ERROR`, which keeps the error color because an error has to be recognizable at a glance and from the far end of a scrollback. (The accent in this UI belongs to the prompt box at the bottom of the screen — the one live surface — and, transiently, to the cursor's own line: the highlight is the only thing that borrows it, and no window *type* carries it. Every line above the prompt is a record.)

The exception is a tool window's **name**, which is the row's payload rather than its chrome: it takes `toolNameStyle` (the tool-content color, bold) in the collapsed row and in the expanded one, so folding a window never repaints the token that says which tool ran. In the normal register this is the same color the line is drawn in; the two part company only under the cursor, where the highlight covers what names the window and leaves the name and the arguments in the content color. The other thing on the line that is *not* chrome is a folded window's content summary: it stays muted, and stays muted under the cursor too. The cursor register is the same object with those colors swapped for the accent color (`Styles.Selected()`), which is why the highlight needs no per-glyph logic.

```
+ REASONING       The user says "my os" — unclear. Perhaps they w… running. Let me check with a command.
- REASONING                                           2026/09/14 16:32:07 +08:00
The user says "my os" — unclear. Perhaps they want to know what OS they are on.
```

**The markers are ASCII**, and that is the argument for them: a glyph that sits at column 0 of every window row must measure one cell in every terminal and must exist in every font, and only ASCII guarantees both. They replaced the triangles `▸`/`▾` (U+25B8/U+25BE), which were chosen for width alone (East-Asian Neutral, outside Extended_Pictographic — see the glyph policy in `constants.go`): a good reason, and a weaker guarantee than the character set itself. Nothing in the frame depends on a symbol block any more; every glyph the window layer draws is either ASCII or a box-drawing rule, and box drawing is a class-wide waiver the frame has always paid for.

An expanded window's line is `- LABEL`, the arrival timestamp right-aligned to the window edge — and, while that line is pinned to the screen top, the count of the window's own lines hidden above it — and then the content. There is **no rule above and none below**: a window is opened by its own line, and closed by the next window's own line (the prompt box closes the last one). A *floating* surface brackets its content with rules instead — the prompt input around the draft, with the attachment badge rows above it when files are attached, and a selector overlay around its filter box — and in a selector the bare list hangs under that box's closing rule, with the help bar closing the overlay below.

#### The timestamp

The expanded line carries `2026/09/14 16:32:07 +08:00` at its right end: **when the adapter received this window**, which is the only time the display has. The session record carries no per-message time (its `created_at`/`updated_at` are session-level), so a replayed session stamps every message with the moment the replay reached the adapter — the honest reading of "arrival time", and the reason the header calls it nothing more.

It is the adapter's receipt clock, read once when the window is created (`Window.CreatedAt`) and never read again while rendering: rendering stays a pure function of the window's state (cacheable, testable), and the clock is touched at exactly one boundary. The column is a fixed 26 cells (`timeStampLayout`, pinned by a test) and right-aligned, so the label yields to it and never the other way round: a tool header too long for the row keeps its name and loses the timestamp rather than the reverse. On a terminal too narrow for both, the timestamp is simply not drawn — no truncation, no shortened format. The pinned row's `N lines above` sits in the gap to the left of it and yields first, because the timestamp is the anchor that places it: a label too wide leaves room for the timestamp alone, and the count then drops out (a count without its timestamp would be unanchored chrome). Label, then timestamp, then count: the count costs 15 cells at its narrowest (`1 line above` plus its ` | ` separator) and grows with the number, so it first fits at width 53 on an `ASSISTANT` row and at 75 on a tool row carrying `execute_command` — comfortable at 80 columns, out of reach on a 40-column terminal, which keeps the label and the timestamp.

Twenty-six cells, where the column was 16 before, buys two things. **Seconds**: a minute is shorter than the gaps this transcript is read for — a command that ran 40 seconds and the answer after it land in the same minute — while everything one delta flush delivers still shares one second (400 windows are created, stamped and rendered in about a millisecond), so the finer resolution separates what a reader is looking at without fragmenting what arrived together. **The offset**: six cells that name the zone the clock is in, and they are the same six cells on every row of a session, because the zone is the process's — constant chrome, spent on making a column that appears nowhere else in the frame self-describing. A zone *name* would be three cells, `CST`, and mean three different zones. The format is the wall-clock shape the UI already used, so this is not RFC 3339 and a strict parser will reject it; a machine-readable column, if one is ever wanted, is a separate decision.

#### Cursor highlight

The cursor highlight covers the window's own line, and never its content: on a folded window that is the marker and the label column, on an expanded one the same chrome plus the timestamp at the far end (and, when that row is the pinned one, the `N lines above` and its ` | `, which takes the row's own style with the rest). Two things on that row stay out of it, because they are content — a tool window's name (it takes `toolNameStyle` in both fold states, so folding a window does not repaint it) and a folded row's summary, which keeps the muted color. That is why the label and the summary are separate styles (`Styles.Label` vs `Styles.System`), and why the name is a third one. And because the chrome is one style, "the highlight" is a single styles swap — the row is not assembled from pieces that would each need their own color. Mechanically this is a derived styles set — `Styles.Selected()` swaps the two colours that can name a window (the label colour, which is what every window line is drawn in, and the error colour, which `SYSTEM ERROR` uses) for the accent color (the theme's primary), and the renderers paint the row from whatever styles they are handed, so "who is the cursor" needs no extra parameter. The highlighted row is built on first request rather than by `Window.Render`, because the collapsed variant costs a second `BuildCollapsed`: `Render` runs for every window on every content change, while exactly one window at a time is under the cursor. Under an overlay (`Styles.Dimmed()`) the accent color *is* the dim color, so the highlight disappears while a modal owns the screen.

The marker glyph is fixed by the terminal layout (`foldArrow`/`unfoldArrow` in `internal/adapters/terminal/constants.go`), not by the theme: the line reserves exactly one cell for it, so the glyph is a geometry decision and switching color schemes must not change it. The spinner frames `⠋…⠏` and the tool markers `✓ ✗` are Neutral and were never part of the problem. See [performance analysis](internal/virtual-rendering-performance.md) for the rendering rationale (collapsed windows are O(1) to render and track).

### Collapsed Summary Truncation

Collapsed window summaries use **head + "…" + tail** (40/60 split of the available width) for all one-shot content, so the user sees both the topic phrase and the latest content — e.g. a long ASSISTANT message becomes `beginning of answer begi…er  and then some more content ending` rather than just the tail.

**The only exception is streaming delta content** — tool argument deltas (`Af`) and the UF execution preview (`Uf` while `ToolStatusPending`). For these, the head is "what already happened" and the tail is "what just arrived", so a **leading `…`** is more useful: `…"content":"hello"}`. The user just wants to see the latest chunk, not the start of the stream.

This gives a clean rule: **only delta → leading `…`; everything else → head+tail.**

The truncation marker (`…`) is rendered with the **dim** color (`t.Dim`) in both forms, while the surrounding content uses the muted color (`t.Muted`). This creates a clear visual hierarchy: actual content vs. truncation marker. The dim color is lighter than muted on a light background, so the `…` recedes — appropriate because it's metadata, not content. The truncation is grapheme-cluster-aware: ZWJ emoji (👨‍👩‍👧‍👦), combining marks (é), and wide CJK characters are never split mid-cluster. On a `USER PROMPT` row the content may open with the attachment badge summary; that run keeps the register (default foreground, **bold**) it holds unfolded — but only while the cut leaves it whole, since half a badge is not worth two registers (`collapsedRow.style`).

### Markdown Rendering

Rendering is **on by default** for assistant text (`ASSISTANT`) and reasoning
(`REASONING`) windows; `--no-markdown` starts new windows raw instead. Press `r`
on an **unfolded** window to toggle raw ↔ rendered per window; folded windows
always show their raw one-line summary.

When enabled, GFM-style tables are re-laid out as an aligned unicode grid. A
table wider than the window is re-flowed rather than truncated — cells hard-wrap
and a record may span several rows — and where a frame can no longer be drawn
the layout falls back to a record form. No content character is ever cut. Tables
inside fenced code blocks are never transformed.

The layouts, the exact widths where each applies (with rendered examples), what
was deliberately not done, and the guarantees tests hold:
[markdown-rendering.md](markdown-rendering.md).

### Virtual Scrolling

The display uses virtual scrolling to handle large outputs efficiently. The
viewport clips the window buffer to the visible **visual lines** and renders
only the windows that overlap them — typically 1-3 windows per frame, down
from the buffered window range of the old model. Cached display widths make
fragment output cheap: `GetAll` (viewport render) measured **~68% faster**
after the soft-wrap refactor (`BenchmarkWindowBufferGetAll`). See
[performance analysis](internal/virtual-rendering-performance.md) for details.

### Sentinel values

`WindowBuffer.dirtyIndex` uses a sentinel (`dirtyFullRebuild = -2`) to signal that all windows need recalculation. State transitions must check whether the sentinel is already set before overwriting — an `else` branch that blindly assigns a new index can downgrade a full-rebuild to a single-window update, silently dropping windows from the display. See `window.go` → `markDirty`.

### ANSI escape sequences are not recursive

When styling text with the style layer, each segment must be rendered
individually before concatenation. You cannot render a string that already
contains ANSI codes with a new style and expect it to work.


## Glyphs and Terminal Width

Cell arithmetic lives in one file. `internal/adapters/terminal/width.go`
owns both "how many cells does this occupy" and "cut this string at N
cells", from one width table (displaywidth's, with the options constructed
there). That is a fix, not a style preference: rows used to be sized with
`ansi.StringWidth` and cut with `rivo/uniseg`'s cluster widths, and the two
tables disagree for some single clusters — a keycap (`1` + U+FE0F + U+20E3)
is 1 cell to one and 2 to the other — so a 4-cell budget could be filled
with what measures 5 cells, shifting the row below it and wrapping the last
segment of a full-width row. `width_test.go` cuts every string in a corpus
of those clusters at every budget and re-measures the result; the
markdown-summary and tool-preview truncations that take this path are
covered by the same invariant.

Everything the TUI draws is then chosen against one constraint: the layout
reserves exactly one cell for it, and a character whose `East_Asian_Width`
is **Ambiguous** is drawn one cell wide by default and two cells wide by a
terminal configured for CJK (`xterm -cjkwidth`, mlterm's setting, some font
configurations) — a configuration nobody reaches by accident, and one no
runtime query reveals (the adapter issues no capability probe; see
[Paste and terminal capability](#paste-and-terminal-capability)). So the
guard is the choice of codepoint: the status indicator is the still braille
cell `⠿` when idle and the shared spinner (`⠋…⠏`) when running — both from the
same Neutral braille block, so the column cannot move between states; the
fold markers are ASCII (`+`/`-`) and not the triangles pair it replaced; and
the help bars separate key hints with an ASCII `|` rather than `│`. Lines the
app draws are one family: the frame rules, the
markdown table grid and the in-content divider (`───`, `Separator`) are all
box drawing, so a divider can never be confused with a markdown rule, a
unified-diff file header, or the `---` frontmatter and config-block
delimiters of this product's own file formats.

Two classes are accepted as limitations instead of being worked around,
because no Neutral alternative exists: **box drawing** (`─ │` and the
markdown table grid — the whole range `U+2500-U+257F` is Ambiguous, so
every rule and table frame in the app shares the same exposure), and a few
**typographic marks** (`…`, `—`, `∞`). Program-owned symbols stay
single codepoints throughout, now for a host-side reason: a glyph followed
by U+FE0F asks the terminal for emoji presentation, and a terminal that
ignores the request draws one cell where the table reserves two, and a ZWJ
family is one cluster here and several there.

One environment variable is worth naming, because it looks like support and
is not: `RUNEWIDTH_EASTASIAN` is read by `x/ansi` at init and makes it charge
Ambiguous glyphs two cells. `width.go` ignores it (it holds its own options),
but the escape-aware breakers (`ansi.Hardwrap`, `Wrap`, `Truncate`, `Cut`)
still read it, so with the variable set a frame rule wraps onto a second
row. **Double-width-ambiguous is not a supported mode**; the assumption is
written down here rather than left to a library default. Making it a real
mode means either a capability probe for what the terminal actually does, or
an ASCII glyph set for rules and grids — both product decisions, not bug
fixes.

The rule, the waiver list with its reasons, and the enforcement live in
`internal/adapters/terminal/constants.go` (glyph policy) and
`glyphs_test.go`, which scans the package's own string literals and fails
on an unclassified glyph, a stale entry, or a measured width that
contradicts its class.

## Tool Confirm Dialog

When a tool requires confirmation (configured via `--tool-confirm`), a dialog overlay appears:

| Key | Action |
|-----|--------|
| `y` | Allow the tool to run |
| `n`, `Esc` | Reject the tool |
| `e` | Open full tool input in external editor (view-only) |

The dialog shows the tool name in the title and a 2-row preview of the tool's
input arguments. The preview wraps like a message window: rows of the same
long command line are emitted as one continuous soft-wrap run (no hard
newlines inside the command, so a selection copies it without fake line
breaks), and when the input does not fit, the last visible row ends with `…`
so a cut never looks like the real end of the arguments. The Screen row diff
tracks the run's wrapped span, so live updates and the dialog close repaint
every row it covered. When the dialog carries tool input, a hint row under
the `y / n` line says `Press e to view the full input.` — the title itself
stays clean, so a long tool name never pushes the hint off. Press `e` to
inspect the complete input in `$EDITOR` without closing the dialog.

## Line Wrapping

Content in each window is wrapped to the available width using the
**terminal's own soft-wrap** (see
`internal/virtual-rendering-performance.md`). The viewport renders each
window as a **continuous fragment** — the visual rows are joined without
hard newlines, and every row except the last is padded with trailing
spaces to the full window width, so the terminal soft-wraps exactly at
the simulated breakpoints. Newlines appear only **between** windows
(preventing windows from merging into one soft-wrap run).

Consequences:

- **Copy fidelity**: selecting a window's content and pasting restores the
  original text — the layout newlines are soft wraps, not `\n` characters.
  (Layout padding — trailing spaces per row — is the visible trade-off of
  terminal soft-wrap; terminal selection includes them.)
- **Terminal continuation works**: soft-wrapped rows are one logical line,
  so features like cross-row URL recognition behave normally.
- **ANSI styles survive clipping**: every visual row is styled
  self-contained (styles re-applied per row), so scrolling into the middle
  of a colored diff keeps `-`/`+` colors correct.

This requires a **raw passthrough renderer**: cell-buffer renderers
truncate lines wider than the screen and re-materialize wrapped rows as
hard rows, which breaks both the display and copy fidelity. The self-built
TUI stack renders directly — `screen.go` writes the view content verbatim
to the terminal (`ED2` + home + content + absolute CUP) and leaves it to
the terminal to soft-wrap. Overlays are drawn with absolute cursor-position
sequences instead of line compositing (see `docs/tui-architecture.md`).

The program runs the terminal in **raw mode** (`x/term` MakeRaw clears
OPOST/ONLCR), so the renderer emits `\r\n` for every `\n` in the view
content: a bare LF would only move the cursor down without returning it
to column 0, spiraling every line after the first. The conversion is
output-only — the view content itself keeps plain `\n`, so terminal
selection still copies the original text.

The wrapping breakpoints are **character-boundary** — a word wider than
the line width is broken mid-word, matching how a typical terminal
behaves.

Width calculation is **Unicode-aware**:

Measuring and cutting both go through `width.go`, against one table
(`displaywidth`'s, options pinned there — see
[Glyphs and Terminal Width](#glyphs-and-terminal-width)):

- ASCII / Latin characters occupy **1 cell**
- East-Asian **Neutral** single-codepoint marks occupy **1 cell**, in every
  configuration: the fold markers `+`/`-`, the tool markers `✓ ✗`, the spinner
  frames `⠋…⠏` (which the status bar's indicator draws while a task runs; its
  idle cell is the still braille cell `⠿`, the union of those six dots). These
  are the glyphs the layout reserves
  exactly one column for, which is why the help bars use an ASCII `|` where they used to draw
  `│`. The heavier `▶ ▼` are Ambiguous too (`▶` Extended_Pictographic as
  well), and the reasoning marker `✦` — Neutral, and drawn even at level 0 —
  was deleted as decoration rather than replaced. All of it is the glyph
  policy in `constants.go`.
- East-Asian **Ambiguous** glyphs (`─ │ ├ ┼ →`, and the marks `… — ∞`) also
  occupy **1 cell** — the app-wide assumption behind every width calculation:
  the prompt box's rules are literally `strings.Repeat("─", width)`, and table
  rules and the truncation marker charge one cell per glyph. Box drawing has no
  Neutral alternative, so this is the accepted exposure (waiver 2), not an
  oversight.
- CJK characters (中文、日本語、한국어) occupy **2 cells**
- Emoji occupy **2 cells** (grapheme clusters per Unicode UAX #29), and are
  drawn from a single codepoint on purpose: a trailing U+FE0F asks the
  terminal for emoji presentation, and a terminal that ignores the request
  draws one cell where this table reserves two
- ANSI escape codes (colors, bold, etc.) occupy **0 cells**
- Tabs are expanded to **8 cells** (`TabWidth`) via `expandTabs` **before** any
  width-sensitive operation (truncation, wrapping), because both the table in
  `width.go` and the `x/ansi` breakers count a tab as 0 cells while a terminal
  renders it at the tab stop — expanding first keeps truncation budgets and
  the final render consistent.

`RUNEWIDTH_EASTASIAN=1` is worth naming precisely, because it is not a
terminal setting: it changes what the Go width libraries report, not what the
terminal draws. `width.go` ignores it by construction, but the escape-aware
breakers (`ansi.Hardwrap`, `Wrap`, `Cut`) read it through `x/ansi`, so with it
set a rule that measures one way breaks another way and the frame shifts —
15 tests in this package fail that way. Double-width Ambiguous is therefore
**not a supported mode**, and setting the variable on an ordinary terminal
breaks the UI that would otherwise have been correct.

Window rendering produces **visual line arrays** (`Window.cache.lines`) — one
element per terminal row. Display widths are measured once per render
(`Window.cache.widths`) and reused by the viewport for padding, so fragment
output never re-measures lines. `lineHeights` are the visual line counts,
so cursor navigation (j/k/H/M/L) and `EnsureCursorVisible` operate on
terminal rows exactly as before.

Incremental updates avoid re-wrapping the entire content on every token. Only the last line is combined with the new delta and re-wrapped, keeping per-token cost proportional to the delta size rather than total content.

## Help Window

Press `Ctrl+H` or type `:help` to open a help window listing all keybindings and commands. The filter input at the top lets you fuzzy-search for specific keys or commands (e.g. typing `gt` matches `:theme_set`):

| Key | Action |
|-----|--------|
| `Tab` | Toggle focus between filter input and list |
| `Esc` | Close help window |
| `j` | Move selection down |
| `k` | Move selection up |
| `Enter` | Copy selected command to input (commands only) |

The help window is organized into three sections:

- **Commands** — colon commands available in the input field
- **Global Shortcuts** — keybindings that work from any context
- **Display Mode** — navigation and editing keys for the display area

The help window uses the same size, position, and overlay pattern as the model selector and theme selector.
