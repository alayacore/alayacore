# Windows Console Support

Design record for the terminal adapter on Windows. The user-facing statement is
in [tui.md → Windows Consoles](../tui.md#windows-consoles); this file is for
whoever is about to change it, and mostly records decisions so they are not
re-litigated from scratch.

## Where things live

| File | Owns |
|---|---|
| `term_io.go` | the lifecycle: `TTY.MakeRaw` negotiates raw mode **and** sequence processing, `TTY.Restore` undoes both. Nothing else enters or leaves that state |
| `console_windows.go` | the only `GetConsoleMode`/`SetConsoleMode` calls in the tree, plus two pure functions (`outputVTMode`, `inputNoSelectionMode`) holding the bit arithmetic |
| `console_unix.go` | the same two hooks as no-ops, so the lifecycle above is written once |
| `exec.go` → `acquireTerminal` | re-negotiation after a child owned the console |
| `program.go` → `refreshSize` | the size re-read that stands in for the resize signal Windows does not have |

`golang.org/x/term` does the input half of raw mode and, as a detail worth
knowing, also sets `ENABLE_VIRTUAL_TERMINAL_INPUT` in its `makeRaw` — which is
what makes keystrokes arrive as the byte sequences `key_parser.go` parses. It
never touches the output handle; that is the half `console_windows.go` added
after the Bubble Tea fork that used to do it was removed (4edb5a85).

## Decisions

**Gate on the syscalls failing, not on reading the bit back.** A read-back
looks like the more rigorous check, and it is the one that lies: on a pseudo
console the mode word may not track `ENABLE_VIRTUAL_TERMINAL_PROCESSING` even
though every sequence is processed, so "set succeeded, bit not visible" would
turn a working Windows Terminal into a startup failure. `SetConsoleMode`
reports unsupported flags with `ERROR_INVALID_PARAMETER`, which is the signal
we act on.

**The input console mode is recorded once, by x/term.** `TTY.state` holds what
`term.MakeRaw` saved before changing the input handle, and `term.Restore` puts
that back — including the QuickEdit clear, which `enterVT` applies afterwards
and therefore does not need to save. A second copy of the same mode would
create two owners disagreeing about which value is the original.

**QuickEdit is cleared best-effort.** A refusal there leaves the click-to-select
behavior, which is a usability problem, not a broken frame; aborting startup
over it would be the wrong trade. ANSI processing is the opposite case —
without it the frame *is* garbage — so its failure is fatal.

**No host detection.** `GetConsoleMode` succeeds on every Windows console host,
which is precisely why the pre-existing `term.IsTerminal` check could not see
this bug. There is no probe that distinguishes conhost from a pseudo console:
`DECRQM` mode reporting is unanswered by the hosts that lack it, and inferring
from a missing answer is a timeout. The environment variables that happen to
correlate (`WT_SESSION`) identify one vendor's product, not the capability, and
would be wrong for every other terminal that does implement it. So the code
asks the console for the mode and believes the answer, instead of maintaining a
list.

**No paste-by-timing heuristic.** Two were tried and rejected while this was
being designed, and both are recorded so nobody spends a third round on them:

1. *"A CR that arrives in the middle of a read chunk is pasted content."* The
   chunk boundary is set by `unix.Poll`/a blocking `Read` and the loop's own
   progress, not by the user, so a fast typist or a held key produces the same
   shape as a paste. It also does not hold for a large paste, which arrives in
   several chunks and would submit wherever a boundary happens to fall.
2. *"Drain the queue before deciding"* (`GetNumberOfConsoleInputEvents` /
   `poll(0)`) — closes the hole in (1) by adding a second mechanism whose
   correctness depends on how the host paces bytes into the input queue.

What shipped instead is a split in the keymap: `Ctrl+J` (LF) inserts a line
break, `Enter` (CR) submits. It decides nothing about the host and guesses about
nothing; the residue is documented in tui.md.

**No color-profile ladder.** The style layer emits 24-bit truecolor
(`38;2;r;g;b`, pinned by `style_test.go`). Negotiating down to 256 or 16 colors
would mean a capability model in front of every style decision, for a failure
mode that is "a color is substituted", not "the screen is wrong". See the
unverified list below for what hosts actually do.

**Fail fast rather than degrade to a plain renderer.** A console that will not
accept sequence processing cannot host a cursor-addressed UI, and a second
render path with no host to exercise it in CI is worse than the error message
that says `--plainio`.

## What is proven where

CI (`test.yml`) runs on every push: `go vet ./...` for `GOOS=windows` (which
type-checks every package including test files), `go test
./internal/adapters/terminal/...` on a Windows runner, builds for all twelve
release targets, and the whole suite plus lint on Linux.

Covered by tests that run there:

- `console_windows_test.go` — the bit arithmetic: what `outputVTMode` adds, that
  it preserves everything else, that it is idempotent, and the same for
  `inputNoSelectionMode` including the `ENABLE_EXTENDED_FLAGS` requirement.
- `program_resize_poll_test.go` — `refreshSize` queues exactly one
  `WindowSizeMsg` per real change, does not advance the tracked size when the
  send is dropped (so the next tick retries), and is driven by a `tickMsg`
  through the actual event loop.
- `input_newline_test.go` — the `Ctrl+J`/`Enter` split, and that the line break
  stays out of the generic key path so overlay filter boxes are unaffected.

**Not proven anywhere but a real machine** — a runner gives `go test` pipes,
never a console window, so nothing here can observe the behavior:

- [ ] Colors render as the theme specifies in `cmd.exe` and in Windows
      Terminal; record whether a legacy host substitutes from its palette.
- [ ] Alt screen entered and left cleanly; the shell prompt is intact after
      exit, and `DISABLE_NEWLINE_AUTO_RETURN` demonstrably did not leak into
      the shell's own output.
- [ ] Clicking inside the `cmd` window while output is streaming does not
      freeze the UI (the QuickEdit clear).
- [ ] Dragging the window resizes the layout, in both hosts, within one tick.
- [ ] Pasting a small and a large (≥4 KB) multi-line block in `cmd`: nothing
      submits that the user did not ask for.
- [ ] `Ctrl+O` into an editor and back: still rendering, still reading keys.
- [ ] Function keys, arrows, Home/End, and `Ctrl+Home`/`Ctrl+End` arrive as the
      parser expects under `ENABLE_VIRTUAL_TERMINAL_INPUT`.

## Known gaps

- **mintty / MSYS2 pty**: `openTTY` falls back to `CONIN$`/`CONOUT$` when
  stdout is not a console, and a pty-backed process may have no console to open
  — so it errors out although mintty would render ANSI fine. Pre-existing, and
  its own piece of work.
- **`Program.inputPaused` is read only by the Unix input loop**
  (`program_input_windows.go` parks input by doing nothing), so
  `GOOS=windows golangci-lint run` reports it as unused. Real, not a bug;
  the clean fix is to move the parking state behind the build tag, which
  touches the suspend path.
- **`Ctrl+Z` does not suspend on Windows** (`suspendProcess` is a no-op, as it
  was in the fork: `suspendSupported = false`).
- **Editor handoff input race**: `exec_windows.go` documents that the blocking
  input reader can race a foreground child for keystrokes, because the read
  cannot be parked without console-level polling.
