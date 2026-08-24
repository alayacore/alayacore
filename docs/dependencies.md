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
- **`term_io.go`** — TTY opening (`/dev/tty` fallback), raw mode via
  `golang.org/x/term`, byte reading
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

### `github.com/charmbracelet/x/ansi` — ANSI-aware String Operations

Handles width measurement, truncation, and line-breaking on **text that already contains ANSI escape codes**. Used in three places:

**① Text wrapping (`wrap.go`)**

```go
func wrapContent(s string, width int) string {
	s = ansi.Hardwrap(s, width, true)  // hard-break at width
	// ...
}
```

The input to `wrapContent` is **styled** output containing `\033[32m...\033[0m` sequences. Line breaking must **ignore ANSI code bytes** and measure only visible characters.

**② Confirmation dialog (`confirm_dialog.go`)**

```go
wrapped := ansi.Hardwrap(styled, innerWidth, true)
line = ansi.Truncate(line, limit, "")
```

Same scenario — text with ANSI styles.

**③ Input field (`input_field.go`)**

```go
// input_field.go — padding from the same uniseg width source as truncation
valWidth := runesWidth(visible)
```

The text here is **plain text** (user input, no ANSI codes) — the one case where `ansi` is *not* involved. Its width comes from the `uniseg` grapheme-cluster width source (the single width model shared by truncation, cursor placement, and scrolling).

So `ansi` is needed for the ANSI-bearing text paths (①② above); the input field is the plain-text exception.

**Why can't `go-runewidth` replace it?**

| Scenario | `ansi` | `runewidth` |
|----------|--------|-------------|
| `Hardwrap("\033[32mHello\033[0m", 3, true)` | `"\033[32mHel\nlo\033[0m"` ✅ | `"\033[32mHel"` ❌ (counts ANSI as visible width) |
| `Truncate("\033[32mHello\033[0m", 3, "")` | `"\033[32mHel\033[0m"` ✅ | `"\033[32mH"` ❌ (truncates mid-ANSI) |
| `StringWidth("\033[32mHello\033[0m")` | `5` ✅ | `16` ❌ (counts ANSI bytes) |

Since the project processes large amounts of styled text (containing ANSI codes), `ansi` is essential.

---

### `github.com/rivo/uniseg` — Unicode Text Segmentation (direct dependency)

The **single width source of the input chain** (`input_field.go`). `FirstGraphemeClusterInString` streams grapheme clusters and returns each cluster's terminal display width in one step. It internally applies the UAX #29 properties (Grapheme_Extend, SpacingMark, ZWJ, regional indicators, Prepend, …), so a cluster renders as one unit — `❤️` (heart + variation selector) is one 2-cell cluster, a ZWJ family emoji is one 2-cell cluster, `e` + combining acute is one 1-cell cluster — and truncation, cursor placement, scrolling, and padding always agree and never split a cluster:

```go
// input_field.go — graphemeClusters: segmentation + width in one source
cluster, rest, width, nextState := uniseg.FirstGraphemeClusterInString(s, state)
```

Direct dependency of the input chain for grapheme segmentation and width.

### `github.com/mattn/go-runewidth` — transitive dependency only

Per-rune width lookup (O(1) table query, fast). It was the input chain's original width source, but it measures **one rune at a time** and has no knowledge of grapheme clusters: multi-rune display units are measured wrong — `❤️` (heart + variation selector) is 1 cell instead of 2, a ZWJ family emoji is 8 cells instead of 2. Wrong widths break truncation (overflow), cursor placement (off by a cell), and horizontal scrolling. The input chain therefore uses `uniseg` (grapheme-cluster aware) as its single width source; `go-runewidth` remains only as a transitive dependency of `x/ansi` (wrap/confirm-dialog subsystems) and no project code imports it directly anymore.

---

## Utility Libraries

### `golang.org/x/term` — Terminal Size Detection

```go
// adapter.go
w, h, err := term.GetSize(int(os.Stdout.Fd()))
```

Gets terminal dimensions at startup for initial layout.

### `golang.org/x/sys` — System Calls

Unix signal handling and terminal mode settings. Required by the TUI runtime (`program.go`).

### `golang.org/x/net` — Networking

Used by the project's LLM communication layer.

### `gopkg.in/yaml.v3` — YAML Parsing

Used to parse the YAML frontmatter of skill `SKILL.md` files
(`internal/skills/manifest.go`). Model configs (`model.conf`), runtime
config, themes, and MCP config use the key-value format
(`config.ParseKeyValue`), not YAML.
