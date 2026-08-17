# Dependencies

This document explains AlayaCore's key dependencies and why each is needed.

## Table of Contents

- [TUI Framework](#tui-framework)
- [Styling](#styling)
- [Low-level Text Processing](#low-level-text-processing)
- [Utility Libraries](#utility-libraries)

---

## TUI Framework

### `charm.land/bubbletea/v2` — Terminal UI Framework

The entire terminal adapter (~50 Go files) is built on top of it. Provides:

- **Event loop**: handles keyboard input, window resize, timers, etc.
- **Component model**: `tea.Model` interface (`Init`/`Update`/`View`) implemented by all UI components
- **Message passing**: `tea.Msg` for communication between components
- **Command system**: `tea.Cmd` for side effects (opening editor, async reads, etc.)
- **Terminal management**: automatic TTY switching, CDC mode, signal forwarding

**Irreplaceable.** Replacing it means rewriting the entire terminal adapter.

---

## Styling

### `charm.land/lipgloss/v2` — Style Rendering

Lip Gloss is Charm's styling library, providing CSS-like style definitions for terminal text. Referenced in 150+ places across the project.

Provides:

- **Style definitions**: foreground, background, bold, italic, underline, etc.
- **Border system**: rounded/thick/hidden borders, plus the custom `RenderOpenBox` helper for the "open" panel style (top/bottom rules only, no side borders)
- **Width/height constraints**: `Width()` / `Height()` / `MaxWidth()` for controlling render area
- **Text wrapping**: `WrapWriter` carries ANSI styles across line breaks

**Irreplaceable.** Deeply coupled with Bubble Tea; high replacement cost.

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

The input to `wrapContent` is Lip Gloss **rendered** output containing `\033[32m...\033[0m` sequences. Line breaking must **ignore ANSI code bytes** and measure only visible characters.

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

Since the project processes large amounts of Lip Gloss-rendered text (containing ANSI codes), `ansi` is essential.

---

### `github.com/rivo/uniseg` — Unicode Text Segmentation (direct dependency)

The **single width source of the input chain** (`input_field.go`). `FirstGraphemeClusterInString` streams grapheme clusters and returns each cluster's terminal display width in one step. It internally applies the UAX #29 properties (Grapheme_Extend, SpacingMark, ZWJ, regional indicators, Prepend, …), so a cluster renders as one unit — `❤️` (heart + variation selector) is one 2-cell cluster, a ZWJ family emoji is one 2-cell cluster, `e` + combining acute is one 1-cell cluster — and truncation, cursor placement, scrolling, and padding always agree and never split a cluster:

```go
// input_field.go — graphemeClusters: segmentation + width in one source
cluster, rest, width, nextState := uniseg.FirstGraphemeClusterInString(s, state)
```

Also used indirectly by Lip Gloss v2 for correct emoji/combining width handling; now a direct dependency of the input chain.

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

Unix signal handling and terminal mode settings. Required by Bubble Tea.

### `golang.org/x/net` — Networking

Used by the project's LLM communication layer.

### `gopkg.in/yaml.v3` — YAML Parsing

Used to load model configuration files and theme definitions.
