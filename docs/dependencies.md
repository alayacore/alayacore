# Dependencies

This document explains AlayaCore's key dependencies and why each is needed.

## Table of Contents

- [TUI Framework](#tui-framework)
- [Styling](#styling)
- [Low-level Text Processing](#low-level-text-processing)
- [Utility Libraries](#utility-libraries)

---

## TUI Framework

### Self-built TUI stack (`internal/adapters/terminal`) — Terminal UI

The terminal adapter runs on a self-hosted minimal TUI stack (see
`docs/tui-architecture.md`), replacing the former Bubble Tea + Lip Gloss
dependency:

- **`program.go`** — event loop: `Update`/`Cmd`/`Msg` dispatch, batches and
  sequences, timers (`Tick`), quit, panic recovery (terminal always
  restored)
- **`key_parser.go`** — byte stream → key messages; streaming escape
  sequence state machine; bracketed paste passthrough; UTF-8
- **`term_io.go`** — TTY opening (`/dev/tty` fallback, `CONIN$`/`CONOUT$` on
  Windows), raw mode via `golang.org/x/term`, and the platform's
  sequence-processing mode via the `enterVT`/`exitVT` hooks in
  `console_unix.go` / `console_windows.go`
- **`screen.go`** — alternate screen, cursor management, raw passthrough
  rendering (`ED2` + home + content verbatim + absolute CUP)
- **`exec.go`** — external editor handoff (`ExecProcess`) and Ctrl-Z
  suspend: release the terminal (exit alt screen, restore cooked mode),
  run the child in the foreground, then re-acquire and repaint
- **`style.go` / `wrap.go`** — the styling layer (below)

The stack keeps the external interface (`adapter.go`, `OutputWriter`,
protocol layer) unchanged; the app's `Terminal.Update` message types are
self-defined (`Update`, `Cmd`, `Msg`, `WindowSizeMsg`, ...) — see
`docs/tui-architecture.md` for the design rationale.

---

## Styling

### Self-built style layer (`style.go`, `wrap.go`) — Style Rendering

CSS-like style definitions for terminal text, byte-compatible with the
former Lip Gloss output (locked by `style_test.go`):

- **Style definitions**: foreground, background, bold, italic, underline,
  strikethrough; SGR generation via `github.com/charmbracelet/x/ansi`
- **Width/height helpers**: `Width()` / `Height()` for measuring rendered
  text
- **Text wrapping**: `WrapWriter` re-applies the active ANSI style after
  every newline, so every visual line is self-contained (the soft-wrap
  fragment pipeline depends on this)

---

## Low-level Text Processing

### `github.com/clipperhouse/displaywidth` — the one width table (direct dependency)

Every answer to "how many cells does this occupy" and every "cut this string at N cells" comes from this library, through `width.go`, from one `Options` value constructed there:

```go
// width.go
var widthModel = &displaywidth.Options{EastAsianWidth: false}

func cellWidth(s string) int                 // ansi.Strip, then this table
func takeCells(s string, cells int) string   // leading whole clusters
func tailCells(s string, cells int) string   // trailing whole clusters
```

Two properties follow, both pinned by `width_test.go`:

- **Measuring and cutting cannot disagree.** They used to: rows were sized with `ansi.StringWidth` and cut with `rivo/uniseg`'s cluster widths, and the two tables answer differently for some single clusters — a keycap (`1` + U+FE0F + U+20E3) is 1 cell to uniseg and 2 to displaywidth. A 4-cell budget then received 5 cells, and the row shifted the next one. `width_test.go` re-measures every cut at every budget and fails on an overrun; run against the previous implementation it names the keycap and the variation-selector cases.
- **The environment cannot retune the layout.** `x/ansi` reads `RUNEWIDTH_EASTASIAN` in its own package `init` and, when it says true, charges East-Asian-Ambiguous glyphs (`│ ─ … — · • ↓ ∞`) two cells; nothing of ours runs early enough to undo it (an `init` in this package, or an `os.Unsetenv` in `main` — measured, both too late). Holding our own options is what makes the adapter's numbers not move. `TestCellWidthIgnoresRunewidthEastAsian` proves it by re-executing the test binary with the variable set.

Cluster boundaries come from `github.com/clipperhouse/uax29/v2/graphemes`, which displaywidth and `x/ansi` both segment with; it stays an indirect dependency.

### `github.com/charmbracelet/x/ansi` — protocol and escape-aware line breaking

Everything that speaks to the terminal, plus the line breakers that must understand escapes to survive them:

**① Line breaking on styled text (`wrap.go`, `confirm_dialog.go`)**

```go
s = ansi.Hardwrap(s, width, true)   // hard-break at width
line = ansi.Truncate(line, limit, "")
```

The input contains `\033[32m...\033[0m` sequences. A breaker that measured those bytes as visible characters would break the line in the middle of a color code. `ansi` measures clusters, keeps the escapes attached, and re-emits them; `width.go` deliberately does not reimplement that (see its header on why `ansi.Cut`/`TruncateLeft` take the styled rows the cutters are handed).

**② Output sequences and SGR (`screen.go`, `style.go`, `wrap.go`)** — cursor addressing and erase, alt screen, bracketed paste, focus reporting, cursor style and color, OSC-8 hyperlinks, and the SGR attribute order the styling layer is byte-compatible with. 87 production call sites across 38 symbols; this, not width, is why the dependency is here.

**③ Parsing the escape sequences inside a wrapped row (`wrap.go`)** — `ansi.Parser` reads SGR and OSC-8 as they pass through `WrapWriter`, so the active style and hyperlink can be re-emitted after every newline.

**Why plain text alone cannot do the measuring**

| Scenario | `ansi` / `width.go` | `runewidth` alone |
|----------|--------|-------------|
| `Hardwrap("\033[32mHello\033[0m", 3, true)` | `"\033[32mHel\nlo\033[0m"` ✅ | `"\033[32mHel"` ❌ (counts ANSI as visible width) |
| `Truncate("\033[32mHello\033[0m", 3, "")` | `"\033[32mHel\033[0m"` ✅ | `"\033[32mH"` ❌ (truncates mid-ANSI) |
| `cellWidth("\033[32mHello\033[0m")` | `5` ✅ | `16` ❌ (counts ANSI bytes) |

Since the project processes large amounts of styled text (containing ANSI codes), escape-aware measurement is required on one side or the other; `width.go` gets it by stripping through `ansi.Strip` first, which understands the full ECMA-48 grammar including 8-bit C1 (displaywidth's own escape handling covers 7-bit introducers only, and differs there — measured).

### `github.com/mattn/go-runewidth` — transitive dependency only

Per-rune width lookup, with no knowledge of grapheme clusters: `❤️` (heart + variation selector) is 1 cell instead of 2, a ZWJ family emoji is 8 cells instead of 2. Wrong widths break truncation (overflow), cursor placement (off by a cell), and horizontal scrolling, so no code of ours measures with it. It stays in the build because `x/ansi` links it for its `WcWidth` methods (`StringWidthWc`, `TruncateWc`, ...), which this project never calls.

---

## Utility Libraries

### `golang.org/x/term` — Terminal Mode, Size, and Raw Mode

```go
// adapter.go — initial layout
w, h, err := term.GetSize(int(os.Stdout.Fd()))

// term_io.go — raw mode for the session
st, err := term.MakeRaw(int(t.in.Fd()))
```

The four calls the TUI cannot work without: `IsTerminal` (decide whether there
is a terminal to take over), `GetSize` (layout), `MakeRaw`/`Restore` (the
enter/exit pair around every frame). Note for Windows: its `makeRaw` also sets
`ENABLE_VIRTUAL_TERMINAL_INPUT` on the input handle, which is what made keystrokes
arrive as byte sequences when the console was read as a byte stream. It still sets
it, and `Restore` still clears it, but the program no longer depends on it: the
console is read as events and the sequences are produced here
(`console_events.go`). It does nothing at all to the output handle, which is what
`console_windows.go` is for.

### `golang.org/x/sys` — Platform Calls Below the Standard Library

Four uses, all of them the reason a plain `os`/`term` call is not enough:

- `unix.Poll` — the timeout-bounded input read (`program_input_unix.go`), which
  is what lets the loop notice the pause request while parked.
- `windows.GetConsoleMode`/`SetConsoleMode` — ANSI sequence processing and the
  QuickEdit clear (`console_windows.go`).
- `windows.GetNumberOfConsoleInputEvents` — the count of events already queued,
  which is what lets the Windows input read return in bounded time
  (`program_input_windows.go`). `ReadConsoleInputW` has no binding in the package,
  so that one call goes through `windows.NewLazySystemDLL`, the route the package
  takes internally.
- Windows job objects — `CreateJobObjectW`/`AssignProcessToJobObject`/
  `TerminateJobObject` through a `kernel32` lazy DLL, with `taskkill` as the
  fallback, to kill a tool's whole process tree
  (`tools/shell/terminate_windows.go`).

### `golang.org/x/net` — Networking

Used by the project's LLM communication layer.

### No YAML parser

A skill's `SKILL.md` frontmatter is read with the same key-value shape as
`model.conf`, runtime config, themes and MCP config — one `key: value` per line,
`#` only starting a comment at the beginning of a line, the first duplicate key
wins — implemented in `internal/skills/manifest.go`. It is not `config.ParseKeyValue`
itself: the manifest reader additionally accepts quoted values, folded (`>`) and
literal (`|`) blocks, and values continued on indented lines. A general YAML
parser was dropped with it: it rejected the unquoted colon in
`description: Use this skill when: …` (losing the skill outright) and ended
plain scalars at ` #`, advertising `description: Count # of items` to the model
as `Count` without any error. See [skills.md](skills.md#how-the-frontmatter-is-read).
