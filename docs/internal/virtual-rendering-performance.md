# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

Benchmarks run on: Intel(R) Core(TM) Ultra 9 285K, Linux amd64, Go 1.26.1.
Verified 2026-08-18 against the current self-built TUI stack (no Bubbles/lipgloss).

## Summary

All optimizations are working correctly:

- ✅ **Virtual rendering** — 16.6x faster than naive full render (3.0μs vs 50.2μs, 100 windows)
- ✅ **Incremental content append** — O(delta) per frame via `appendDeltaToLines`, avoids O(n) full re-wrap (~553x speedup on 5000-line content)
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines` in ~1.1μs (full `ensureLineHeights` with 1 dirty window), no full render needed
- ✅ **Streaming stays under 1ms** — average 4.1μs per full cycle (append + line tracking + GetAll), well within 250ms tick budget
- ✅ **Custom ScrollView** (<1KB) — stores the pre-clipped visible region;
  `View()` pads to the viewport height (~138ns, 4 allocs). `WithContent` is
  ~0.1ns (no re-split)
- ✅ **Soft-wrap fragment viewport** — `renderVirtual` clips to visual lines
  and emits continuous per-window fragments (`\n` only between windows);
  display widths cached per render, so `GetAll` renders 100 windows in 2.6μs
  (see [Soft-Wrap Fragment Rendering](#soft-wrap-fragment-rendering))

## How Streaming Works

During streaming, every `AppendFromTLV` call on a `textRenderer`:

1. Appends the delta to `contentParts` (O(1) for eventual consistency)
2. In **markdown mode** (the default for AT/AR), plain deltas — no `|`
   line and content tail not inside an open table — take the same
   incremental path; deltas that touch a table invalidate the cache and
   fall back to a full re-render (the table transform re-pads columns)
3. Wraps the delta as **plain text** via `appendDeltaToLines` — streaming
   content deliberately carries no styling in normal mode (markdown
   table rendering is plain text too), so the incremental path has no
   ANSI handling and no style state. The dim Body color shown while an
   overlay is open is layered on later, when `BuildInner` returns
   (`bodyStyled`), backed by a `colored` cache so steady frames don't
   recolor
4. Updates `wrappedLines` **incrementally** — only the new text is wrapped and appended
5. `TryLineCount` returns `len(wrappedLines) + 3` immediately — no render needed
   (+2 box rules, +1 header line of the expanded form)

This means line tracking during streaming is **always fast**, not just on cache hits:

```
Streaming frame arrives → appendDeltaToLines (O(delta), plain text)
TryLineCount → len(wrappedLines) + 3  (~1.1μs via ensureLineHeights, no full render)
```

A dedicated assertion test (`TestIncrementalPathIsUsed`) verifies that
`TryLineCount` returns a valid count after every delta append. If the
incremental path breaks, this test fails immediately.

Markdown mode keeps this property for ordinary text: only table-touching
deltas re-render, and benchmarks show mdMode plain-text streaming within
noise of raw mode (`BenchmarkMarkdownStreaming_PlainDeltas` vs
`BenchmarkMarkdownStreaming_RawMode`; identical alloc counts).

### When Full Re-wrap Happens

Full `wrapContent` from scratch only occurs when:

| Event | Why |
|-------|-----|
| **Terminal resize** | Width changed, all lines must be re-wrapped at new width |
| **Theme switch** | Styles changed, all lines must be re-styled and re-wrapped |
| **First render** | No cached wrappedLines yet |

During normal streaming, none of these happen — incremental path is used
exclusively.

## Benchmark Results

### Streaming Performance (Realistic 250ms Tick)

| Metric | Value |
|--------|-------|
| Average full cycle (append + line tracking + GetAll) | **4.1μs** |
| Incremental append only (AppendOrUpdate) | **52ns** |
| Small delta streaming (append + line tracking) | **1.2μs** |
| Long content incremental append (5000-line content) | **1.3μs** |
| Budget | < 1ms (target), 250ms (actual tick) |

### Incremental Append vs Full Re-wrap (5000-line content)

Measured via `BenchmarkAppendVsFullWrap_LongContent` (500 lines of wrapped content, ~5000 wrapped lines at 80 cols).

| Operation | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **Incremental append** | **1.3μs** | **865B** | **62** |
| Full re-wrap | 0.73ms | 796KB | 32,085 |
| **Speedup** | **553x** | **920x** | **518x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 0.73ms
per frame. At the 250ms tick interval this is still manageable, but burst
scenarios (multiple frames arriving between ticks) would accumulate latency.

### Streaming Update End-to-End (51 windows, 50 history + 1 streaming)

Measured via `BenchmarkStreamingUpdateWithIncremental` vs `BenchmarkStreamingUpdateWithoutIncremental`
(viewport=30; the non-incremental side is 100 windows with no viewport, i.e. full render).

| Scenario | Time | Memory | Allocs | Speedup |
|----------|------|--------|--------|:-------:|
| **Incremental (1 dirty window)** | **4.1μs** | 11.7KB | 121 | **baseline** |
| Full rebuild (all dirty, no incremental) | 0.60ms | 631KB | 25,480 | **146x slower** |

Window count does not change the incremental cost: a probe with the documented
101-window scenario (100 history + 1 streaming, viewport 30) measures **4.0μs**
— the same as 51 windows. Incremental path is O(delta), independent of history
length.

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual` (100 windows, viewport=30 lines).

| Scenario | Time | Memory | Speedup |
|----------|------|--------|:-------:|
| `GetAll` with virtual rendering (100 windows) | **3.0μs** | 10.5KB | **16.6x** |
| `GetAll` without virtual rendering (100 windows) | **50.2μs** | 285KB | baseline |

### Line Height Tracking

Measured via `BenchmarkJustEnsureLineHeights` (20 windows, 1 dirty window, cached path).

| Scenario | Time | Notes |
|----------|------|-------|
| Incremental (1 dirty window, cached) | **1.1μs** | `ensureLineHeights` via `TryLineCount` from `wrappedLines` |
| Incremental (1 dirty window, uncached) | ~150μs* | Falls through to full `Render()` |
| Full rebuild (all 100 windows) | ~7.1ms* | All windows rendered from scratch |

\* Historical estimates — fallback path, not exercised during normal streaming.

### Full Update Cycle (Delta + GetAll)

Measured via `BenchmarkWindowBufferDeltaWithGetAll` (100 windows, delta to last window).

| Metric | Value |
|--------|-------|
| Delta + GetTotalLines + GetAll (incremental) | **4.1μs** |
| Delta + GetTotalLines + GetAll (full rebuild) | **1.7ms*** |

\* Measured via `BenchmarkFullRebuildAfterAppend` (all windows invalidated) —
incremental is ~425x faster.

### Cursor Movement

Measured via `BenchmarkVirtualRenderingCursorMovementSingle` (100 windows, viewport=30).

| Metric | Value |
|--------|-------|
| Single cursor move (EnsureCursorVisible + updateContent) | **3.2μs** |
| Scroll 20 steps down + 20 steps up | **113μs** |

### Collapsed-Window Design (single-line fold headers)

The collapsed-window design replaces the bordered fold (3 content lines +
2 border lines) with a single collapse-arrow header line (`LABEL summary`:
text windows show the escaped head + "…" + tail of the content (40/60
split), tool windows the first input line; only streaming delta windows
use leading "…" since the user only cares about the latest chunk).
Measured via `BenchmarkFoldedSession*` (120 windows — 110 folded
tools/reasoning + 10 unfolded user/assistant, width 120, viewport 40):

| Scenario | Value |
|----------|------:|
| `GetAll` viewport render | **7.9μs** |
| 20 cursor moves (j/k) | **0.17ms** |
| Delta into a folded tool window (Uf preview) | **0.11μs** |

Why it's fast:

- **Folded windows are O(1)**: `UpdateLineCountFast` returns `1` immediately for
  folded windows — no wrapping, no border render, no renderer access. During
  streaming, deltas to folded windows cost nothing for line tracking (the
  folded line count stays `1`; the tool window's summary shows the first
  input line, which appends never change, and a folded text window only
  re-renders its single summary line).
- **No full-content wrap on fold**: `BuildCollapsed` only reads and
  tail-truncates the content instead of wrapping the entire content.
- **Cursor moves don't re-render borders**: only the arrow glyph is recolored
  (`renderCursorArrow`), reusing the cached content.
- **Fewer total lines**: 1 line per folded window, shrinking
  `lineHeights`/scrolling math proportionally.

Unfolded windows pay a small cost for the expand-arrow header line
(`LABEL` above the open box — ~1.4μs per delta via `BenchmarkWindowBufferDelta`),
which is dwarfed by the folded-window wins in real sessions.

### GetWindowLineRange

| Scenario | Time |
|----------|------|
| Single lookup (windowIndex=50, 100 windows) | **21ns** |
| Cached (3 lookups) | **56ns total** |

### ScrollView Component

ScrollView holds the **pre-clipped visible region** (produced by
`renderVirtual`) plus the document total line count for clamping — it no
longer re-splits or slices content.

| Metric | Value |
|--------|-------|
| `WithContent` (any size) | **~0.1ns**, 0 allocs (stores the pre-clipped string) |
| `View()` (n=10 to n=10000) | **~138ns**, split + padding (326B, 4 allocs) |
| `ScrollDown(1)` | **~7ns**, 0 allocs |

### Soft-Wrap Fragment Rendering

`renderVirtual` performs **exact viewport clipping**:

- only the windows overlapping `[yOffset, yOffset+height)` are rendered
  (typically 1–3 windows);
- each window's visual lines are joined **without `\n`** and padded to the
  full width (except the last row), so the terminal soft-wraps at the
  simulated breakpoints — copy restores the original text;
- display widths are measured once per render (`border.widths`) and reused
  for padding, so fragment output performs no per-line measurement;
- the dim fold arrow is pre-rendered (`border.arrow`) — no style-layer
  render per window per view.

Measured (120-window folded session / 100-window conversation, viewport
30–40):

| Benchmark | Value |
|-----------|------:|
| `WindowBufferGetAll` | **2.6μs** |
| `FoldedSessionGetAll` | **7.9μs** |
| `WindowBufferDeltaWithGetAll` | **4.1μs** |
| `VirtualRenderingCursorMovement` | **60μs** |
| `VirtualRenderingScroll` | **113μs** |
| `StreamingUpdateWithVirtualRendering` | **4.1μs** |

The render path (full wrap, resize, theme switch) is unchanged: display
widths are computed **lazily** — only when fragment output needs padding —
so `ensureLineHeights`/`Render` never pay the per-line measurement cost.

### wrapContent vs word-boundary Wrap

| Algorithm | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **wrapContent** (character-boundary) | **27.7μs** | 17.3KB | 1,780 |
| Wrap (word-boundary) | 35.4μs | 15.7KB | 1,781 |
| **Speedup** | **1.27x** | — | — |

### Resize Performance

| Scenario | Time |
|----------|------|
| Resize 50 windows (80↔120 cols) | **0.21ms** |

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 0.01%** of wall time during streaming (4.1μs per 250ms tick ≈ 0.002%)
3. **`updateContent()` skips unchanged content** efficiently — the one deliberate exception is the executing-tool spinner refresh (`InvalidateRunningToolSpinners`), which invalidates pending tool windows per tick so the header spinner keeps rotating during silent commands; it costs a ~100ns scan plus one window render, only while a tool executes (see [tool-spinner-refresh.md](tool-spinner-refresh.md))
4. **Incremental append is O(delta)** — no quadratic accumulation for long responses

## Key Design Decisions

### Incremental `appendDeltaToLines`

`textRenderer.AppendFromTLV` appends each delta as **plain text** to
`appendDeltaToLines`, which only wraps the delta and appends it to the existing
`wrappedLines` slice. This avoids re-wrapping the entire accumulated content,
and because streaming content carries no styling in normal mode (markdown
table rendering is plain text too), the incremental path never touches ANSI —
no `styleByTag`, no `WrapWriter` style reapplication, no style state to
maintain. The only styling exception is the overlay state: when an overlay is
open, `BuildInner` wraps the plain rows in the dim `Body` color at return time
(`bodyStyled` / `styleBodyLines`), caching the colored copy so steady frames
reuse it instead of recoloring every render. All text windows (AT/AR/SN/SE)
are plain, so this layered coloring stays out of the incremental path.

Markdown mode (default for AT/AR) gates this path: deltas with a `|` line,
or arriving while the content tail is inside an open table, invalidate the
wrapped-line cache and trigger a full re-render — column widths are a
whole-table property, so only the table transform needs the full content.

The old approach (before the `WindowRendering` interface refactoring) used the same
optimization. It was accidentally dropped during the refactoring and restored in
commit `1021326`.

### Two-Tier Caching

| Cache | Location | Contents | Invalidated by |
|-------|----------|----------|---------------|
| Renderer lines | `textRenderer.wrappedLines` | Wrapped plain-text lines (AT/AR) | Resize, theme change |
| Body-colored lines | `textRenderer.colored` | Dim-colored copy of `wrappedLines`, materialized only while an overlay is active (`styles.Body` carries a foreground) | Content append (`coloredDirty`), resize, theme change, blocked switch |
| Border output | `Window.border` | Visual lines (`lines`), display widths (`widths`, lazy), dim arrow, rendered string + lineCount | Content append, resize, theme |

Renderer lines are **updated incrementally** during streaming (not invalidated).
Border cache is marked invalid on every content change but rebuilt on next render.

`lineCount` lives in border cache so `WindowBuffer` can read it with direct field
access (no interface dispatch on the hot path).

### Why `ensureLineHeights` Defers Full Render

During streaming, `ensureLineHeights` first tries `UpdateLineCountFast` → `TryLineCount`.
If the renderer's `wrappedLines` is populated, this returns the line count in ~1.1μs
without rendering. The actual `w.Render()` — which joins wrapped lines, applies borders,
and renders the style layer — is deferred to `GetAll` → `renderVirtual`, which needs
the rendered output for the viewport anyway.
