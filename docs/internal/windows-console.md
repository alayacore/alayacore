# Windows Console Support

Design record for the terminal adapter on Windows. The user-facing statement is
in [tui.md → Windows Consoles](../tui.md#windows-consoles); this file is for
whoever is about to change it, and mostly records decisions so they are not
re-litigated from scratch.

## Where things live

| File | Owns |
|---|---|
| `term_io.go` | the lifecycle: `TTY.MakeRaw` negotiates raw mode **and** sequence processing, `TTY.Restore` undoes both, `TTY.Close` releases only the files `openTTY` opened. Nothing else enters or leaves that state |
| `console_windows.go` | the `GetConsoleMode`/`SetConsoleMode` calls on the output handle, plus two pure functions (`outputVTMode`, `inputNoSelectionMode`) holding the bit arithmetic |
| `console_unix.go` | the same two hooks as no-ops, so the lifecycle above is written once for every platform |
| `program_input.go` | the input loop and the parking protocol, shared by every platform |
| `program_input_unix.go` | the Unix input source: the TTY polled with a bounded wait, read as bytes |
| `program_input_windows.go` | the Windows input source: the console input buffer read as **events**, and the two console syscalls that fill them |
| `console_events.go` | the `INPUT_RECORD` layout and the translation of an event into the bytes a terminal would have sent. Built for every GOOS so it can be tested without a console |
| `exec.go` → `releaseTerminal`/`acquireTerminal` | the handoff around a foreground child: park, release, run, re-acquire (which re-negotiates the mode a child may have reset) |
| `program.go` → `refreshSize` | the size re-read that stands in for the resize signal Windows does not have |

`golang.org/x/term` does the input half of raw mode and, as a detail worth
knowing, also sets `ENABLE_VIRTUAL_TERMINAL_INPUT` in its `makeRaw`. On the byte
path that is what makes keystrokes arrive as the sequences `key_parser.go`
parses. Since the events rewrite the program no longer reads that byte stream:
the mode is still set, and it is still restored by `term.Restore`, but nothing in
this tree depends on it. It never touches the output handle; that is the half
`console_windows.go` added after the Bubble Tea fork that used to do it was
removed (4edb5a85).

## What the two reports were

Both Windows reports about the external editor had one root: **a console read that
was pending when it should not have been.**

*Opening an editor left it unresponsive to the keyboard.* `exec_windows.go` said
`pauseInput` was a no-op, so releasing the terminal did not stop the input loop.
Its read was still queued on the console input buffer when the child started, and
the console delivers input to whoever asked first — so the editor never saw a
keystroke. On top of that, `TTY.Restore` puts the buffer back into cooked mode
before the child runs, and a cooked-mode byte read waits for a whole line, so the
stolen input could sit in the program's hands for an arbitrarily long time.

*After `:q`, the shell prompt did not appear until another key was pressed.* Same
pending read, different victim: `os.File.Close` does not return until the read in
flight on that file does (`internal/poll` waits for its last reference), and the
teardown in `Run` closed the TTY's input file. The process therefore could not
exit, and cmd and PowerShell print their prompt after the process exits. The
keystroke that "fixed" it was the keystroke that finished the abandoned read.
`screen.go`'s teardown order was reworked while this was being chased
(`screen_teardown_test.go` records that) and did not cure it, because the
sequence we wrote last was never the part that was waiting.

The fix is therefore not in the teardown bytes at all. It is that a read must be
**bounded by construction**, so the loop is always within a poll interval of
noticing that it has been asked to stop, and two invariants hold: no read is in
flight when the terminal is released to a child, and no read is in flight when the
terminal is handed back to the shell.

## Decisions

**The console is read as events, not as bytes.** The byte path is convenient — it
is the same code as Unix — but a byte read on a console handle has no bounded form:
`ReadConsole` waits for input (for a whole line, in cooked mode), and there is no
reliable way to interrupt it. Reading events fixes the *wait* rather than working
around it: `GetNumberOfConsoleInputEvents` counts what is already queued,
`ReadConsoleInput` consumes from that same queue, so asking for no more events than
were just counted cannot block. The loop then always returns, which is what the
park protocol needs.

**Cancellation was rejected, not neglected.** Three known routes were considered
and none is sound enough to build a shutdown path on:
`CancelIoEx`/`CancelIo` on the console input handle — Bubble Tea uses one and
comments "these cancel methods do not reliably work on console input and should not
be counted on"; `CancelSynchronousIo` on a thread locked to the read — same
documented unreliability, plus thread affinity to get wrong; and
`muesli/cancelreader`, whose Windows implementation waits on an overlapped
`CONIN$` handle and whose `Cancel` returns `false` when it could not cancel,
because the pseudo console "sometimes returns from the wait without input being
available". A fix that sometimes works is not a fix for a bug whose symptom is a
keyboard that does not work.

**Readiness must be measured in the currency of the read.** A count-gated *byte*
read was the smaller change and is not sound: the count is of events, and a byte
read consumes only the events that have a character form, so a buffer holding a
key release, a lone modifier, or a mouse event reports "ready" to a read that will
then wait. There is no predicate over the events that avoids the question without
reimplementing the console's private translation table — so the read is taken in
the same currency as the count, and the translation is this tree's own, where it
can be tested.

**An event is re-encoded as the bytes an xterm-style terminal sends for it, and
`key_parser.go` stays the only definition of what a key means.** The alternative —
turning events into `KeyMsg` directly — would give the application two key
vocabularies to keep in step. The contract is pinned by feeding the encoder's
output through the real parser and asserting the key strings the application binds
(`TestEncodedKeysAreWhatTheParserReads`). Two consequences are worth naming:
`ENABLE_VIRTUAL_TERMINAL_INPUT`, whose output no test had ever observed, is no
longer load-bearing; and the mapping runs on every platform's test job, because
`console_events.go` contains no syscall.

**Two events are deliberately not encoded as the console reported them.**
Backspace carries BS (`0x08`) in its character field, which this parser reads as
`ctrl+h` — the help window; terminals report Backspace as DEL (`0x7f`) and the
input field binds `backspace`, so DEL is what is emitted. AltGr is Ctrl+Alt on the
layouts that need both to type a character; prefixing those characters with ESC
would turn `@` into `alt+@`, so Alt is reported as a chord only when Ctrl is not
held. A third case covers for the console rather than departing from it:
`Ctrl+letter` normally arrives as the control code in the character field, and when
it arrives bare the code is derived from the key, which is what a terminal sends and
what the `ctrl+<letter>` bindings name. All three are asserted in
`console_events_test.go` rather than left to a comment.

**The loop polls at a fixed short interval instead of waiting on the handle.** A
console input handle is only waitable when it is opened separately with
`FILE_FLAG_OVERLAPPED`, and the pseudo console is documented to signal that wait
without input available — which, with a non-blocking read behind it, is a busy
loop. A 10 ms poll costs one cheap query per interval, caps both the keystroke
delay and the delay before an editor starts, and owns no extra handle. (Bubble
Tea's `peekConsInput` settles on the same shape with a 16 ms sleep.)

**Parking is shared, and it is not advisory.** `pauseInput` waits for the loop's
acknowledgement before `releaseTerminal` touches the terminal; `Run`'s teardown
calls `stopInput` and waits for the loop to finish before it writes the
leave-alt-screen sequences, restores the mode, and closes anything. The wait is
bounded (`readLoopTimeout`) because a bounded wait that expires is better than no
wait at all — and, given the reads above, expiring means the loop is wedged in
something that is not a read.

The counterpart is that **no failure path may leave the loop parked.** A parked
loop is a program that renders and cannot be typed at: it can show an error and
cannot be told what to do about it. So `releaseTerminal` un-parks before
returning a mid-release error, and `acquireTerminal` un-parks on the way out
whether or not re-entering raw mode and the alt screen succeeded. The degraded
mode `acquireTerminal`'s error opens onto — frames on the main screen, reported
through `EditorFinishedMsg` rather than fatal — is only the lesser harm if the
user can still reach `:quit`.

**`Program.inputPaused` no longer has a Unix-only reader.** It used to be a lint
finding for `GOOS=windows` (the flag was set and cleared by code that Windows never
called), and the CI comment recorded it as the reason the Windows job did not lint.
The protocol being shared now, `GOOS=windows golangci-lint run
./internal/adapters/terminal/...` is clean, so the job runs it.

**The window-size event is read and dropped.** With events, a resize is finally
visible to this program (`WINDOW_BUFFER_SIZE_RECORD`), but the size is still owned
by `Program.refreshSize` on the model tick: one source of truth about what the
terminal's size is, and the resize already converges within one tick. An immediate
resize path would be an improvement, and the record is where it would start.

**Mouse events are read and dropped** because this UI has no mouse handling at all
— `key_parser.go` parses no mouse sequence, so there was no behavior to preserve.

**An external editor's buffer is block text, not keystrokes.** `blockText`
(`input_field.go`) is what turns text arriving as a chunk into something the input
field may hold: CRLF and lone CR become LF, control characters other than the
newlines go, trailing newlines go. It has always been the rule for a bracketed
paste; the editor handoff now runs the finished buffer through it as well. The
reason is a Windows default rather than a Windows bug: notepad writes CRLF, and so
does vim for a file it creates, so a prompt composed in `$EDITOR` came back with
CRs in it — and a CR reaching the frame is the terminal being told to go to column
0, with the rest of the line painted over another one.

### Kept from before

**Gate on the syscalls failing, not on reading the bit back.** A read-back looks
like the more rigorous check, and it is the one that lies: on a pseudo console the
mode word may not track `ENABLE_VIRTUAL_TERMINAL_PROCESSING` even though every
sequence is processed, so "set succeeded, bit not visible" would turn a working
Windows Terminal into a startup failure. `SetConsoleMode` reports unsupported flags
with `ERROR_INVALID_PARAMETER`, which is the signal we act on.

**The input console mode is recorded once, by x/term.** `TTY.state` holds what
`term.MakeRaw` saved before changing the input handle, and `term.Restore` puts that
back. A second copy of the same mode would create two owners disagreeing about
which value is the original. One consequence is worth knowing: the saved word
predates `enterVT`'s QuickEdit clear, so the mode handed to a child during a
handoff has click-to-select *enabled* again — see Known gaps.

**QuickEdit is cleared best-effort.** A refusal there leaves the click-to-select
behavior, which is a usability problem, not a broken frame; aborting startup over it
would be the wrong trade. ANSI processing is the opposite case — without it the
frame *is* garbage — so its failure is fatal.

**No host detection.** `GetConsoleMode` succeeds on every Windows console host,
which is precisely why the pre-existing `term.IsTerminal` check could not see this
bug. There is no probe that distinguishes conhost from a pseudo console: `DECRQM`
mode reporting is unanswered by the hosts that lack it, and inferring from a missing
answer is a timeout. The environment variables that happen to correlate
(`WT_SESSION`) identify one vendor's product, not the capability, and would be wrong
for every other terminal that does implement it. So the code asks the console for
the mode and believes the answer, instead of maintaining a list.

**No paste-by-timing heuristic.** Two were tried and rejected while this was being
designed, and both are recorded so nobody spends a third round on them:

1. *"A CR that arrives in the middle of a read chunk is pasted content."* The
   chunk boundary is set by `unix.Poll`/a blocking `Read` and the loop's own
   progress, not by the user, so a fast typist or a held key produces the same
   shape as a paste. It also does not hold for a large paste, which arrives in
   several chunks and would submit wherever a boundary happens to fall.
2. *"Drain the queue before deciding"* (`GetNumberOfConsoleInputEvents` /
   `poll(0)`) — closes the hole in (1) by adding a second mechanism whose
   correctness depends on how the host paces bytes into the input queue.

That rejection is about *inferring meaning* from the shape of the queue, and the
events reader does not do that: it uses the queue count only to decide whether a
read can block, never to decide what a keystroke was. A pasted character and a
typed one are the same event and are treated identically, as on every other
platform.

What shipped instead is a split in the keymap: `Ctrl+J` (LF) inserts a line break,
`Enter` (CR) submits. It decides nothing about the host and guesses about nothing;
the residue is documented in tui.md. Note that on the event path a paste is a
sequence of key events with no framing at all — which is what makes that split the
right answer rather than a workaround.

**No color-profile ladder.** The style layer emits 24-bit truecolor
(`38;2;r;g;b`, pinned by `style_test.go`). Negotiating down to 256 or 16 colors
would mean a capability model in front of every style decision, for a failure mode
that is "a color is substituted", not "the screen is wrong". See the unverified list
below for what hosts actually do.

**Fail fast rather than degrade to a plain renderer.** A console that will not
accept sequence processing cannot host a cursor-addressed UI, and a second render
path with no host to exercise it in CI is worse than the error message that says
`--plainio`. The input side now makes the same judgment for the same reason:
`newInput` asks the handle whether it is a console input buffer and refuses to
start if it is not (`program_input_windows.go`).

## What is proven where

CI (`test.yml`) runs on every push: `go vet ./...` for `GOOS=windows` (which
type-checks every package including test files), `golangci-lint` for the terminal
adapter with `GOOS=windows`, `go test ./internal/adapters/terminal/...` on a
Windows runner, builds for all twelve release targets, and the whole suite plus lint
on Linux.

Covered by tests that run there:

- `console_windows_test.go` — the bit arithmetic: what `outputVTMode` adds, that
  it preserves everything else, that it is idempotent, and the same for
  `inputNoSelectionMode` including the `ENABLE_EXTENDED_FLAGS` requirement.
- `console_events_test.go` — the `INPUT_RECORD` ABI (size and union offset), the
  event decoder, every sequence the encoder emits, the surrogate pairing, the two
  deliberate departures above, and the end-to-end claim that each encoded key
  arrives through the real parser as the string the application binds. It is
  build-tagged for nothing: it runs on Linux too, and on any machine this file is
  changed on.
- `program_input_test.go` — the parking protocol: `pauseInput` does not return
  while a read is in flight, a parked loop starts no reads and delivers what
  arrived only after it is resumed, and `stopInput` returns only once the loop has
  finished. Also build-tagged for nothing, so the Windows job runs these against
  the loop it used to be unable to park.
- `program_input_unix_test.go` — that the real Unix source keeps the promise the
  protocol is built on: it is between reads within one poll timeout, and the
  terminal is left alone while parked.
- `program_input_windows_test.go` — that `newInput` refuses a handle that is not a
  console input buffer, with an error that says so. A runner's stdin is a pipe,
  which is exactly the wrong kind of stream, so this is the one Windows startup
  decision a runner can observe.
- `term_io_test.go` — that `TTY.Close` releases only what `openTTY` opened. This is
  the half of the delayed-prompt fix that is not about reading: the process's own
  standard streams are not this program's to close.
- `program_resize_poll_test.go` — `refreshSize` queues exactly one
  `WindowSizeMsg` per real change (`TestConsecutiveTicksQueueOneResizeMessage`
  sends three ticks and counts one message), does not advance the tracked size
  when the send is dropped (so the next tick retries), and is driven by a
  `tickMsg` through the actual event loop.

### Cost and convergence

`program_resize_cost_bench_test.go` measures the quiet-tick path against a real
pty: **~240ns, 0 allocs**, ioctl included; the change path is the same plus a
non-blocking send. At the 250ms tick that is ~1µs of CPU per second of running the
UI — which is why `refreshSize` runs on every platform instead of behind a
"no resize signal" branch.

It cannot storm, and the reason is structural rather than a rate limit: every
`WindowSizeMsg` sender in the tree takes the size from `Screen.Size()`, and the
tracked pair is only ever assigned by the event loop. There are four places a
message can come from — `run`'s initial frame, `watchSignals` (where a resize
signal exists), `refreshSize` on the tick, and `acquireTerminal` after a child
returns — and the last two are the same function, which is the only one that both
compares and commits. So a message always agrees with what the next compare will
read, one change produces one message, and there is exactly one writer of
`p.width`/`p.height`. `acquireTerminal` goes through `refreshSize` rather than
sending on its own authority for a second reason: it runs *on* the loop that drains
the queue, so a blocking send there would be a self-deadlock whenever the queue
filled while an editor held the foreground — which a session streaming output into
a 64-message buffer does easily. `watchSignals` deliberately queries without
assigning: a second writer there would let one resize queue two messages, each
clearing the frame caches and forcing a full repaint.

Two senders can still agree-but-duplicate in principle (a SIGWINCH landing between
a tick's query and its assignment) — the cost is one redundant message and one
same-content frame, and `render` skips a byte-identical one. Fixing that would mean
a lock around two integers read 4 times a second, which is a worse trade than the
duplicate.

The input loop's own cost on Windows is one `GetNumberOfConsoleInputEvents` per
10 ms while idle, and one more per batch of at most 128 events while typing.

## Not proven anywhere but a real machine

A runner gives `go test` pipes, never a console window, so nothing about what the
console *does* can be observed there. The events rewrite moved the whole keyboard
onto a path no runner can exercise, so these are not academic:

- [ ] Typing in the prompt: letters, digits, punctuation, Space, Tab, Enter,
      Backspace (must delete a character, not open help), Delete, Escape.
- [ ] Arrows and the navigation block, bare and with Shift/Ctrl: `shift+up`,
      `ctrl+left`, Home/End, PageUp/PageDown, and `Ctrl+Home`/`Ctrl+End`. This is
      the assumption the whole design rests on: that
      `ENABLE_VIRTUAL_TERMINAL_INPUT` (which `term.MakeRaw` turns on and nothing
      here reads) translates keys when a *byte* read consumes the events, and
      leaves the events themselves canonical. If it instead injected the
      translated characters as their own key events, an arrow would arrive as this
      program's sequence *plus* a stray `ESC [ A` of characters, and it would be
      obvious in the first second of typing.
- [ ] The chords the application binds: Ctrl+A/C/D/G/H/J/L/O/P/R/S/U, F1, and
      Ctrl+J versus Enter (the line-break split, which Ctrl+Enter should also
      reach as a line break).
- [ ] Function keys — F1 is the one the help window is bound to, and the SS3/CSI
      split is where a hand-written table is most likely to be wrong.
- [ ] A CJK and an emoji input, typed and pasted (the surrogate pairing lives in
      `keyEncoder`, not in the console).
- [ ] A multi-line paste in `cmd`: nothing submits that the user did not ask for.
- [ ] `Ctrl+O` into an editor and back: the editor takes input from the first
      keystroke, and returning repaints at the current size.
- [ ] The same handoff into **notepad** (one of the default editors when `EDITOR`
      is unset): it is a GUI process, so nothing about the console is involved, and
      what it writes is a CRLF file — the prompt must come back with its lines
      intact rather than painted over itself.
- [ ] Quitting after an editor session, and quitting without one: the shell's
      prompt appears immediately, with no keypress required.
- [ ] Colors render as the theme specifies in `cmd.exe` and in Windows Terminal;
      record whether a legacy host substitutes from its palette.
- [ ] Alt screen entered and left cleanly; `DISABLE_NEWLINE_AUTO_RETURN`
      demonstrably did not leak into the shell's own output.
- [ ] Clicking inside the `cmd` window while output is streaming does not freeze
      the UI (the QuickEdit clear).
- [ ] Dragging the window resizes the layout, in both hosts, within one tick.
- [ ] Focus in and out of the window: the dim and restore that
      `FocusMsg`/`BlurMsg` drive, if `FOCUS_EVENT_RECORD` reaches a legacy console
      application at all.

## Known gaps

- **mintty / MSYS2 pty**: `openTTY` falls back to `CONIN$`/`CONOUT$` when stdout is
  not a console, and a pty-backed process may have no console to open — so it errors
  out although mintty would render ANSI fine. Pre-existing, and its own piece of
  work.
- **`Ctrl+Z` does not suspend on Windows** (`suspendProcess` is a no-op, as it was
  in the fork: `suspendSupported = false`). The terminal dance around it still
  runs, so the binding costs a release/re-acquire and changes nothing else.
- **Click-to-select is available while a child owns the terminal.** The mode
  handed to the child is the one `term.MakeRaw` saved, and that predates
  `enterVT`'s QuickEdit clear, so a click during an editor session can enter select
  mode, which suspends writes to the buffer. Checked on the machine that reported
  the original symptoms, and it does not outlive the handoff: the next keypress
  ends the selection, so vim leaves select mode on `:` and the writes resume well
  before the teardown reaches anything of ours. The behavior is the host's own and
  the same one any console program gets, so `inputNoSelectionMode` is deliberately
  *not* re-applied after `TTY.Restore` on the release path — doing so would take
  click-to-copy away from the child for no measured gain, and the final exit has to
  give the user's setting back either way. If a freeze that survives the child ever
  is reported, that re-application is the one-line change to make.
