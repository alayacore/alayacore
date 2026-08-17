# Terminal UI

AlayaCore's terminal UI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss) and uses vim-like keybindings throughout.

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

### Input Cursor & IME

The prompt input (and overlay filter boxes) render the **real terminal cursor**
(steady block in the theme's `cursor` color) instead of a painted block. This
keeps input behavior identical to a shell prompt: Chinese/Japanese IME
composition draws its inline preedit directly in the input field and the
candidate window anchors to the input line, so it does not jump around while
streaming output is being rendered.

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

The prompt prefix indicates the current mode: `F` for local, `U` for URL.

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

### Tool Status Dot

Every tool window's header line carries a status dot right after the `TOOL` label (`TOOL•`), mirroring the status bar's `•` live / `·` idle convention. A hollow dot (`·`) marks a window whose arguments are still streaming in — the tool has not started executing yet (a large `write_file` payload can take a while to arrive). The dot turns solid (`•`) in the theme's primary color once the complete input has arrived and the tool is executing — the same color the status bar uses while a run is in progress — and takes the result color (green on success, red on error) when it finishes.

### Tool Result Separator

Tool windows separate the tool call's arguments from its result with a dimmed `---` line. The arguments are shown without the status dot or the `name: ` prefix (both live in the header line), so a window reads: header (`▼ TOOL• execute_command`), argument line (`lscpu | grep …`), `---`, result. `write_file` and `edit_file` follow the same layout — their diff content (the `-`/`+` lines) is the argument block, followed by `---` and the result.

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
| `Tab` | Toggle focus | ❌ Never |

### Fold Mode

Press `Space` on any window to collapse it — the window becomes a single header line: the collapse arrow followed by a label (`TOOL•`, `REASONING`, `ASSISTANT`, `USER`, `NOTIFY` for system notifications, or `ERROR`) and a content summary. Labels are left-justified to a fixed column so summaries align across window types (tool windows show `TOOL•` followed by the tool name + arguments). The collapse arrow marks a collapsed window; press `Space` again to expand.

An expanded window shows a header line (expand arrow + label) above its content box, which uses only top/bottom rules — no side borders ("open" style). The cursor highlight only recolors the fold-state arrow with the selection color — rules never change color during navigation. The arrow glyphs themselves are theme-configurable (`fold_arrow` / `unfold_arrow`). See [performance analysis](internal/virtual-rendering-performance.md) for the rendering rationale (collapsed windows are O(1) to render and track).

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

When styling text with lipgloss, each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work.


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
**terminal's own soft-wrap** (REFACTOR.md). The viewport renders each
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

This requires a **raw passthrough renderer**: the stock bubbletea v2
cell-buffer renderer truncates lines wider than the screen and
re-materializes wrapped rows as hard rows, which breaks both the display
and copy fidelity. AlayaCore runs a small fork of bubbletea v2
(`third_party/bubbletea`, via a `replace` directive) that adds a raw mode
to `tea.View`: the view content is written verbatim to the terminal and
left for the terminal to soft-wrap. Overlays are drawn with absolute
cursor-position sequences instead of line compositing (see REFACTOR.md).

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
