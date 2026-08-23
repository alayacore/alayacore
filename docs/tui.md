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
| `J` / `Shift+Down` | Scroll down one line |
| `K` / `Shift+Up` | Scroll up one line |
| `Ctrl+D` | Scroll down half screen |
| `Ctrl+U` | Scroll up half screen |
| `g` | Go to first window, scroll to top |
| `G` | Follow the last window |
| `H` | Move cursor to top window in visible area |
| `M` | Move cursor to middle window in visible area |
| `L` | Move cursor to bottom window in visible area |
| `f` | Jump to next user prompt |
| `b` | Jump to previous user prompt |
| `e` | Open window content in external editor |

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt |
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

### Input Cursor & IME

The prompt input (and overlay filter boxes) render the **real terminal cursor** (the emulator's default steady block — themes do not configure cursor color, since the `cursor` field was dropped from `Theme` when body content rendering stopped carrying an explicit foreground color). This keeps input behavior identical to a shell prompt: Chinese/Japanese IME composition draws its inline preedit directly in the input field and the candidate window anchors to the input line, so it does not jump around while streaming output is being rendered.

## Multi-Modal Attachments

AlayaCore supports multi-modal input — attaching images, audio, video, or documents alongside text. Attachments are sent as TLV frames **before** the text frame, all within a single `TagUserEnd`-delimited message:

```
[TagUserI/V/A/D frames...] + [TagUserT text] + [TagUserEnd]
```

### Attachment Picker

Press `Ctrl+A` to open and toggle the attachment picker overlay. Two modes are available:

**Local Mode** (default):
Browse and select local files via a file browser with fuzzy search.
The input field shows the current directory path (with trailing `/`).
Type a path fragment to filter files, or type a new absolute path to navigate.

| Key | Action |
|-----|--------|
| `Tab` | Toggle focus between path input and file list |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `Backspace` | Delete last path segment (e.g. `/abc/def/` → `/abc/`) |
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

The path input is a bare input like every other overlay filter — no prompt prefix (the old `F`/`U` markers were removed). The current mode is discoverable from the help bar (`ctrl+a: switch to URL` / `switch to local`). File list rows render flush left, no `> ` marker; the selection is highlighted by the text color.

### Attachment Types

The attachment type is determined by file extension (or URL path extension):

| Type | Icon | TLV Tag | Extensions |
|------|------|---------|------------|
| Image | 📷 | `UI` | `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`, `.bmp`, `.svg` |
| Video | 🎬 | `UV` | `.mp4`, `.mpeg`, `.mpg`, `.avi`, `.mov`, `.webm`, `.mkv` |
| Audio | 🎵 | `UA` | `.mp3`, `.wav`, `.ogg`, `.flac`, `.aac`, `.m4a`, `.wma` |
| Document | 📄 | `UD` | `.pdf`, `.txt`, `.md`, others / unknown |

### Display

Attachments appear above the text input, separated by `---`, matching the rendering of user messages in the conversation history:

When a user message is collapsed, attachments remain visible as a compact badge
summary (for example, `📷1 🎵1`) before the text tail. Expand the window to
see the full attachment labels and content.

```
┌───────────────────────────────┐
│ 📷 Image  🎵 Audio            │
│ ---                           │
│ what are these?               │
└───────────────────────────────┘
```

### Sending

When you press `Enter`:
- Local files are read, base64-encoded into `data:` URIs, and sent as TLV frames
- URLs are sent as-is (no fetching)
- Text is sent as a `TagUserT` frame
- A `TagUserEnd` frame finalizes the message

Attachments are cleared after sending. Use `Ctrl+C` to discard both text and pending attachments without sending.

## Session Commands

See [commands.md](commands.md) for the full list of session commands (`:save`, `:cancel`, `:fork`, etc.).

Note: `:quit` / `:q`, `:help`, and `:suspend` are handled directly by each adapter where supported and never reach the session command dispatch (terminal shows a confirmation dialog for quit, opens help window for help, suspends the process for suspend; plainio intercepts `:quit`/`:q` locally — it waits for any running task, then exits with code 0 — while `:help` and `:suspend` are not adapter-handled, so `:help` is sent to the session as a regular command; terseio treats stdin as prompt text unless it starts with `:` — then the whole input is sent as a single command (and `:quit`/`:q` are intercepted locally for a clean exit); rawio passes all commands through as raw CI frames since it doesn't interpret frame payloads).

## Window Container

The display area organizes content into separate windows — one per message or tool call. Windows have synchronized widths and can be navigated independently.

### Tool Status Indicator

Every tool window's header line carries a status indicator right after the `TOOL CALL` label (`TOOL CALL ⠋`, `TOOL CALL ✓`, `TOOL CALL ✗`), separated by one space. While arguments are still streaming in and while the tool is executing, the indicator is the same braille dot-segment spinner used by the session-loading screen. The glyph is a pure wall-clock function (10 frames × 150ms), so it advances with every header re-render; re-renders come from two sources — incoming deltas (`Af` argument chunks, `Uf` execution previews) and, while a tool executes with no output, a tick-driven invalidation that keeps the spinner rotating even during silent commands (see `WindowBuffer.InvalidateRunningToolSpinners` and [tool-spinner-refresh.md](internal/tool-spinner-refresh.md)). There is no dedicated animation timer. When the tool finishes, the spinner is replaced by a check mark (`✓` on success) or cross (`✗` on error). The indicator shares the `TOOL CALL` label color (muted + bold) so `TOOL CALL` + indicator + tool name read as a single colored unit — the indicator carries no semantic color of its own.

### Tool Result Separator

Tool windows separate the tool call's arguments from its result with a dimmed `---` line. The arguments are shown without the status indicator or the `name: ` prefix (both live in the header line), so a window reads: header (`▼ TOOL CALL ⠋ execute_command`), argument line (`lscpu | grep …`), `---`, result. `write_file` and `edit_file` follow the same layout. Only `edit_file`'s argument block is a real diff: the removed rows (`- `) render in the theme's removed color and the added rows (`+ `) in the added color (each wrapped continuation row stays self-contained); context rows and the bare argument line stay plain. `write_file` shows the raw file content being written — plain, never diff-colored (`- `/`+ ` lines there are literal content).

### Auto-Follow

Auto-follow is enabled by default at startup. When enabled, the viewport
automatically scrolls to keep the newest content visible as it arrives.

Auto-follow is disabled by any navigation that actually moves the cursor or
scrolls the viewport. While auto-follow is active:

| Key | Behavior | Disables auto-follow? |
|-----|----------|-----------------------|
| `G` | Follow the last window | ✅ Re-enables |
| `j` / `↓` | Move cursor down | ❌ No-op (race protection) |
| `L` | Move cursor to bottom | ❌ No-op (race protection) |
| `J` / `Shift+Down` | Scroll down one line | ❌ No-op when at bottom |
| `Ctrl+D` | Scroll down half screen | ❌ No-op when at bottom |
| `k` / `↑` | Move cursor up | ✅ If cursor actually moves |
| `H` | Move to top of visible area | ✅ If cursor actually moves |
| `M` | Move to center of visible area | ✅ If cursor actually moves |
| `f` | Jump to next user prompt | ✅ If cursor actually moves |
| `b` | Jump to previous user prompt | ✅ If cursor actually moves |
| `g` / `Home` | Go to first window | ✅ If cursor actually moves |
| `K` / `Shift+Up` | Scroll up one line | ✅ Always |
| `Ctrl+U` | Scroll up half screen | ✅ Always |
| `e` | Open in editor | ✅ Always |
| `Space` | Toggle window fold | ❌ Never |
| `r` | Toggle markdown rendering | ❌ Never |
| `Tab` | Toggle focus | ❌ Never |

### Fold Mode

Press `Space` on any window to collapse it — the window becomes a single header line: the collapse arrow followed by a label (`TOOL CALL` + status indicator, `REASONING`, `ASSISTANT`, `USER PROMPT`, `SYSTEM NOTIFY` for system notifications, or `SYSTEM ERROR`) and a content summary. Labels are left-justified to a fixed column so summaries align across window types (tool windows show `TOOL CALL` + indicator followed by the tool name + arguments). The collapse arrow marks a collapsed window; press `Space` again to expand.

An expanded window shows a header line (expand arrow + label) above its content box, which uses only top/bottom rules — no side borders ("open" style). The cursor highlight only recolors the fold-state arrow with the selection color — rules never change color during navigation. The arrow glyphs themselves are theme-configurable (`fold_arrow` / `unfold_arrow`). See [performance analysis](internal/virtual-rendering-performance.md) for the rendering rationale (collapsed windows are O(1) to render and track).

### Collapsed Summary Truncation

Collapsed window summaries use **head + "…" + tail** (40/60 split of the available width) for all one-shot content, so the user sees both the topic phrase and the latest content — e.g. a long ASSISTANT message becomes `beginning of answer begi…er  and then some more content ending` rather than just the tail.

**The only exception is streaming delta content** — tool argument deltas (`Af`) and the UF execution preview (`Uf` while `ToolStatusPending`). For these, the head is "what already happened" and the tail is "what just arrived", so a **leading `…`** is more useful: `…"content":"hello"}`. The user just wants to see the latest chunk, not the start of the stream.

This gives a clean rule: **only delta → leading `…`; everything else → head+tail.**

The truncation marker (`…`) is rendered with the **dim** color (`t.Dim`) in both forms, while the surrounding content uses the muted color (`t.Muted`). This creates a clear visual hierarchy: actual content vs. truncation marker. The dim color is lighter than muted on a light background, so the `…` recedes — appropriate because it's metadata, not content. The truncation is grapheme-cluster-aware: ZWJ emoji (👨‍👩‍👧‍👦), combining marks (é), and wide CJK characters are never split mid-cluster.

### Markdown Rendering

Markdown rendering is **on by default** for assistant text (`ASSISTANT`) and reasoning (`REASONING`) windows; `--no-markdown` turns the default off (new windows start raw). Press `r` on an **unfolded** window to toggle between rendered and raw per window; the setting is independent per window and applies only when expanded — folded windows keep their raw one-line summary.

When enabled, GFM-style tables (a `|`-delimited header row followed by a delimiter row of dashes) are re-rendered as aligned, padded columns — e.g. `| name | gender | age |` becomes `| name            | gender | age |`. Tables inside fenced code blocks are never transformed. Column alignment markers (`:---`, `---:`, `:---:`) are honored.

When a table is wider than the terminal, the widest columns are shrunk first and over-long cells are truncated with `…` so the table keeps its structure.

Streaming stays incremental: deltas without table rows append through the same O(delta) wrap path as raw mode (benchmarked at raw-mode speed with identical allocations), and only deltas that touch a table — a `|` line, or any delta while the content tail is still inside an open table — trigger a full re-render of that window (one per tick, cost O(window) only for table-bearing windows).

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


## Tool Confirm Dialog

When a tool requires confirmation (configured via `--tool-confirm`), a dialog overlay appears:

| Key | Action |
|-----|--------|
| `y` | Allow the tool to run |
| `n`, `Esc` | Reject the tool |
| `e` | Open full tool input in external editor (view-only) |

The dialog shows the tool name in the title and a 2-line preview of the tool's input arguments. Press `e` to inspect the complete input in `$EDITOR` without closing the dialog.

## Line Wrapping

Content in each window is wrapped to the available width using the
**terminal's own soft-wrap** (see
`docs/internal/virtual-rendering-performance.md`). The viewport renders each
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

- ASCII / Latin characters occupy **1 cell**
- CJK characters (中文、日本語、한국어) occupy **2 cells**
- Emoji occupy **2 cells** (grapheme clusters per Unicode UAX #29)
- ANSI escape codes (colors, bold, etc.) occupy **0 cells**
- Tabs are expanded to **8 cells** (`TabWidth`) via `expandTabs` **before** any
  width-sensitive operation (truncation, wrapping). This matters because the
  underlying `x/ansi` width model counts a tab as 0 cells — expanding first
  keeps truncation budgets and the final render consistent.

Window rendering produces **visual line arrays** (`border.lines`) — one
element per terminal row. Display widths are measured once per render
(`border.widths`) and reused by the viewport for padding, so fragment
output never re-measures lines. `lineHeights` are the visual line counts,
so cursor navigation (j/k/H/M/L) and `EnsureCursorVisible` operate on
terminal rows exactly as before.

Incremental updates avoid re-wrapping the entire content on every token. Only the last line is combined with the new delta and re-wrapped, keeping per-token cost proportional to the delta size rather than total content.

## Help Window

Press `Ctrl+H` or type `:help` to open a help window listing all keybindings and commands. The filter input at the top lets you fuzzy-search for specific keys or commands (e.g. typing `gt` matches `:theme_set`):

| Key | Action |
|-----|--------|
| `Tab` | Toggle focus between filter input and list |
| `q`, `Esc` | Close help window |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `Enter` | Copy selected command to input (commands only) |

The help window is organized into three sections:

- **Commands** — colon commands available in the input field
- **Global Shortcuts** — keybindings that work from any context
- **Display Mode** — navigation and editing keys for the display area

The help window uses the same size, position, and overlay pattern as the model selector and theme selector.
