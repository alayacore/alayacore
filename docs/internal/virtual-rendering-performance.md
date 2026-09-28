# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

Benchmarks run on: Intel(R) Core(TM) Ultra 9 285K, Linux amd64, Go 1.26.1.
Verified 2026-08-18 against the then-current self-built TUI stack (no
Bubbles/lipgloss).

The microseconds below are that machine's and that date's. A re-measurement on
2026-09-28 (AMD Ryzen 7 5800U, Go 1.26.4) found several of them stale by more
than the hardware difference. Three were stale in a way no re-run could have
fixed, because the prose no longer described the benchmark: two benchmarks had
changed shape since their figures were written, and one had been deleted
outright. Those are corrected in place below; the rest are listed in
[Stale figures](#stale-figures-2026-09-28-re-measurement). Read the ratios and
the allocation counts, which travel between machines; treat the absolute times
as indicative until the section is re-run on the reference machine.

## Summary

All optimizations are working correctly:

- ✅ **Virtual rendering** — ~10x faster than rendering every window, and 30x
  lighter (100 windows, viewport 30: 3.8KB against 113KB per `GetAll`; the
  ratio and the memory travel between machines, the times are the
  re-measurement's, 1.5μs against 15.0μs on a Ryzen 7 5800U). This bullet said
  16.6x / 3.0μs / 50.2μs until 2026-09-28; the non-virtual side had got ~3x
  cheaper since it was written, which is the whole gap. See
  [Stale figures](#stale-figures-2026-09-28-re-measurement)
- ✅ **Incremental content append** — O(delta) per frame via `appendDeltaToVisualLines`, avoids O(n) full re-wrap (~510x on 5000-line content, 779B against 474KB per frame; it was ~553x before the width-table change made the incremental side cheaper again)
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines` in ~1.1μs (full `ensureLineHeights` with 1 dirty window), no full render needed
- ✅ **Streaming stays under 1ms** — single-digit μs per full cycle (append + line tracking + GetAll), well within the 250ms tick budget. This bullet quoted 4.1μs; the re-measurement gets 3.0μs on a slower machine, so the figure was stale, but the claim it supports is off by three orders of magnitude either way
- ✅ **Custom ScrollView** (<1KB) — stores the pre-clipped visible region;
  `View()` pads to the viewport height (~138ns, 4 allocs). `WithContent` is
  ~0.1ns (no re-split)
- ✅ **Soft-wrap fragment viewport** — `renderVirtual` clips to visual lines
  and emits continuous per-window fragments (`\n` only between windows);
  display widths cached per render, so `GetAll` renders 100 windows without
  touching the ones outside the viewport (see
  [Soft-Wrap Fragment Rendering](#soft-wrap-fragment-rendering); this bullet
  quoted 2.6μs, the re-measurement gets 1.4μs on a slower machine)

## How Streaming Works

During streaming, every `AppendFromTLV` call on a `textRenderer`:

1. Appends the delta to `contentParts` (O(1) for eventual consistency)
2. In **markdown mode** (the default for AT/AR), plain deltas — no `|`
   line and content tail not inside an open table — take the same
   incremental path; deltas that touch a table invalidate the cache and
   fall back to a full re-render (the table transform re-flows columns and
   wraps cells, so column widths and wrap points are whole-table properties)
3. Wraps the delta as **plain text** via `appendDeltaToVisualLines` — streaming
   content deliberately carries no styling in normal mode (markdown
   table rendering is plain text too), so the incremental path has no
   ANSI handling and no style state. The dim Body color shown while an
   overlay is open is layered on later, when `BuildInner` returns
   (`bodyStyled`), backed by a `colored` cache so steady frames don't
   recolor
4. Updates `wrappedLines` **incrementally** — only the new text is wrapped and appended
5. `TryLineCount` returns `len(wrappedLines) + 1` immediately — no render needed
   (the +1 is the window's own line, above the content; a window draws no
   other chrome)

This means line tracking during streaming is **always fast**, not just on cache hits:

```
Streaming frame arrives → appendDeltaToVisualLines (O(delta), plain text)
TryLineCount → len(wrappedLines) + 1  (~1.1μs via ensureLineHeights, no full render)
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

Re-measured 2026-09-28 on AMD Ryzen 7 5800U; the full-cycle row had drifted and
the other three came out within the machine factor of the figures they replace.

| Metric | Value |
|--------|-------|
| Average full cycle (append + line tracking + GetAll), `StreamingUpdateWithVirtualRendering` | **3.1μs** |
| Incremental append only, `JustAppendUpdate` | **47ns**, 0 allocs |
| Small delta streaming (append + line tracking) | **1.7μs** |
| Long content incremental append (5000-line content) | **1.6μs** |
| Budget | < 1ms (target), 250ms (actual tick) |

### Incremental Append vs Full Re-wrap (5000-line content)

Measured via `BenchmarkAppendVsFullWrap_LongContent` (500 lines of wrapped content, ~5000 wrapped lines at 80 cols). Re-measured 2026-09-28 on AMD Ryzen 7 5800U, `-benchtime 300x`; this benchmark appends on every iteration, so the memory and allocation columns move with `-benchtime` and only the ratio is stable across runs.

| Operation | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **Incremental append** | **1.6μs** | **779B** | **61** |
| Full re-wrap | 0.81ms | 474KB | 27,327 |
| **Speedup** | **~510x** | **~610x** | **~448x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 0.81ms
per frame. At the 250ms tick interval this is still manageable, but burst
scenarios (multiple frames arriving between ticks) would accumulate latency.

### Streaming Update End-to-End (51 windows, 50 history + 1 streaming)

Measured via `BenchmarkStreamingUpdateWithIncremental` vs `BenchmarkStreamingUpdateWithoutIncremental`.
Both sides are 51 windows (50 history + 1 streaming) at viewport 30; they differ
in one thing — the non-incremental side invalidates the streaming window before
each append, so every iteration re-wraps it from scratch instead of appending to
`wrappedLines`. (This section used to describe the non-incremental side as
"100 windows with no viewport, i.e. full render" and quote 0.60ms against 4.1μs
for a 146x speedup. That was not what the benchmark measured, and the ratio was
comparing a viewported 51-window update against something else entirely.)

| Scenario | Memory | Allocs |
|----------|--------|--------|
| **Incremental (1 dirty window)** | 4.5KB | 93 |
| Full re-wrap of the streaming window | 12.7KB | 608 |

Window count does not change the incremental cost: a probe with the documented
101-window scenario (100 history + 1 streaming, viewport 30) measures the same
as 51 windows. The incremental path is O(delta), independent of history length.

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual` (100 windows, viewport=30 lines). Re-measured 2026-09-28 on AMD Ryzen 7 5800U; the non-virtual side had got ~3x cheaper since the figures above it were written, which is why the speedup is no longer 16.6x.

| Scenario | Time | Memory | Speedup |
|----------|------|--------|:-------:|
| `GetAll` with virtual rendering (100 windows) | **1.4μs** | 3.8KB | **10.8x** |
| `GetAll` without virtual rendering (100 windows) | **14.9μs** | 113KB | baseline |

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
| Delta + GetTotalLines + GetAll (incremental, 100 windows) | **3.7μs** |

The full-rebuild side is not comparable and is no longer quoted here.
`BenchmarkFullRebuildAfterAppend` — which this section used to cite as "all
windows invalidated", 1.7ms, ~425x slower — invalidates and re-renders **one**
window of ~310 characters. It measures what a single window's full re-render
costs, not what a full rebuild of the buffer costs, so the two numbers were
never a ratio.

### Cursor Movement

Measured via `BenchmarkVirtualRenderingCursorMovementSingle` and
`BenchmarkVirtualRenderingScroll` (100 windows, viewport=30). Re-measured
2026-09-28 on AMD Ryzen 7 5800U.

| Metric | Value |
|--------|-------|
| Single cursor move (EnsureCursorVisible + updateContent) | **2.8μs** |
| Scroll 20 steps down + 20 steps up | **82μs** |

### Collapsed-Window Design (single-line fold headers)

The collapsed-window design replaces the bordered fold (3 content lines +
2 border lines) with a single marker line (`LABEL summary`:
text windows show the escaped head + "…" + tail of the content (40/60
split), tool windows the first input line; only streaming delta windows
use leading "…" since the user only cares about the latest chunk).
No benchmark measures this session shape any more: the folded-session
benchmark these figures came from is gone from the tree, and a number nobody
can reproduce is worse than none. What is checkable by reading the code is why
the shape is cheap:

- **Folded windows are O(1)**: `UpdateLineCountFast` returns `1` immediately for
  folded windows — no wrapping, no row render, no renderer access. During
  streaming, deltas to folded windows cost nothing for line tracking (the
  folded line count stays `1`; the tool window's summary shows the first
  input line, which appends never change, and a folded text window only
  re-renders its single summary line).
- **No full-content wrap on fold**: `BuildCollapsed` only reads and
  tail-truncates the content instead of wrapping the entire content.
- **Cursor moves don't rebuild a window's rows**: only the one navigational element
  is recolored — the fold marker and the label column on a folded line, all
  of row 0 (marker, label, timestamp, and the pinned row's count) on an
  expanded one — reusing the cached content (`renderCursor`,
  `renderCache.line0Cursor`).
- **Fewer total lines**: 1 line per folded window, and one row of chrome per
  expanded window (its label opens the window; there is no header line above
  it and no closing rule below it), shrinking `lineHeights`/scrolling math
  proportionally.

Unfolded windows pay a small cost for their own line (the window's label
composed into it, plus the timestamp — ~1.4μs per delta via
`BenchmarkWindowBufferDelta`), which is dwarfed by the folded-window wins in
real sessions.

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
- display widths are measured once per render (`Window.cache.widths`) and reused
  for padding, so fragment output performs no per-line measurement;
- the window's own line (marker, label, timestamp) is built once, at render
  time, and only its row is swapped in the cursor's register — no style
  render per window per view;
- the sticky window line (docs/tui.md → *Sticky Window Line*) is composited in
  this same pass: the decision reuses the `winStart` the loop already computes
  and the body row it displaces is one row less to assemble. The pinned row is
  the one thing on screen that depends on where the viewport is — it carries
  the count of the window's hidden lines above — so it is not `lines[0]`: it is
  memoized on that count in the window's render cache, which makes a frame that
  does not move the viewport a string compare (46 allocations either way, see
  `TestStickyPinAddsNoAllocations`) and a frame that moves it by a row one row
  rebuild (2012ns pinned-still → 3057ns pinned-scrolling against 2024ns
  unpinned, `BenchmarkStickyLineViewportRender`). The rebuild is the window
  line's build, not the body's: `TestStickyPinCostIsIndependentOfTheWindowSize`
  holds a 400-row message to a 40-row one's allocations. Geometry
  (`lineHeights`, `totalLines`) is not involved, which is the point: a
  viewport-dependent line height would invalidate those caches on every scroll
  step.

Measured (100-window conversation, viewport 30). Re-measured 2026-09-28 on AMD
Ryzen 7 5800U; the folded-session row is gone with its benchmark.

| Benchmark | Value |
|-----------|------:|
| `WindowBufferGetAll` | **1.5μs** |
| `WindowBufferDeltaWithGetAll` | **3.7μs** |
| `VirtualRenderingCursorMovement` | **50μs** |
| `VirtualRenderingScroll` | **82μs** |
| `StreamingUpdateWithVirtualRendering` | **3.1μs** |

The render path (full wrap, resize, theme switch) is unchanged: display
widths are computed **lazily** — only when fragment output needs padding —
so `ensureLineHeights`/`Render` never pay the per-line measurement cost.

### wrapContent

`wrapContent` is the only wrapping path: the word-boundary wrapper this used to
be compared against was deleted with the `Style` block width that reached it
(see `style.go`), and it was the last line break not measured with `width.go`'s
table. `BenchmarkWrapContent` measures the remaining one.

The move onto `width.go`'s table made it faster, not slower, because the common
call — one original line that already fits — now returns the string it was given
instead of rebuilding it. Measured back-to-back on one machine (AMD Ryzen 7
5800U, so comparable to itself and not to the figures above), against the
library wrapper it replaced:

| | Time | Memory | Allocs |
|---|---:|---:|---:|
| `ansi.Hardwrap` (before) | 50.6μs | 17.2KB | 1,780 |
| `hardwrapCells` (after) | **35.6μs** | **9.6KB** | 1,772 |

The larger effect is on the paths that wrap short lines, where the early-out
applies. It shows up in allocation rather than in time: the full re-wrap of a
5000-line document went from 560KB and 27,034 allocations per operation to
474KB and 27,327, and markdown streaming from 18.1KB to 14.8KB. (`GetAll` over
100 windows did not move — 113KB before and after; the 285KB this document
quoted for it was already stale, see
[Stale figures](#stale-figures-2026-09-28-re-measurement).)

### Resize Performance

| Scenario | Time |
|----------|------|
| Resize 50 windows (80↔120 cols) | **0.21ms** |

## Stale figures (2026-09-28 re-measurement)

Every benchmark this document names was re-run on AMD Ryzen 7 5800U, Go 1.26.4,
taking the minimum of repeated runs. Three of them were run against the tree
before the width-table change as well, so each row can say whether it drifted on
its own or was moved by that change. The "measured here" column is the same
figure the section tables above quote; the before/after pair comes from a
back-to-back A/B at `-benchtime 200x`, so for `AppendVsFullWrap_LongContent` —
which appends on every iteration, so its memory and allocation columns move with
`-benchtime` — the pair and the table can differ in the last digit. Only its
ratio is stable.

The machine factor was calibrated on the benchmarks that nothing in the
renderer touches — `GetWindowLineRange` 21ns → 31ns, `JustEnsureLineHeights`
1.1μs → 1.55μs, `WindowBufferResize` 0.21ms → 0.28ms — so this machine is
roughly **1.3–1.5x slower** than the reference. A figure that came out *lower*
here is therefore stale by more than the hardware, and the memory and
allocation columns are hardware-independent outright.

| Row | Documented | Measured here | Same benchmark before the change | Reading |
|-----|-----------|---------------|----------------------------------|---------|
| `GetAll` without virtual, memory | 285KB | **113KB** | 113KB | stale before; unchanged |
| `GetAll` without virtual, time | 50.2μs | **14.9μs** | 15.4μs | stale before by ~4.7x once the machine factor is allowed for |
| `GetAll` with virtual, memory | 10.5KB | **3.8KB** | 3.8KB | stale before; unchanged |
| virtual-vs-not speedup | 16.6x | **~10x** | ~11x | the headline ratio no longer holds |
| `WindowBufferGetAll` | 2.6μs | **1.5μs** | 1.6μs | stale before |
| Incremental append, memory | 865B | **779B** | 940B | stale before, then reduced further by the change |
| Full re-wrap, memory | 796KB | **474KB** | 560KB | stale before, then −15% from the change |
| Full re-wrap, allocs | 32,085 | **27,327** | 27,034 | stale before; unchanged within run-to-run drift |
| `StreamingUpdateWithIncremental`, allocs | 121 | **93** | 96 | stale before, then −3 from the change |
| `VirtualRenderingScroll` | 113μs | **82μs** | not re-measured | lower on a slower machine, so stale |
| incremental-vs-full-re-wrap speedup | 553x | **~510x** | ~407x | a ratio, so it travels: it moved because the incremental side got faster (940B → 779B), and it had already drifted before that |

Two of the rows above were wrong in a way no re-measurement could have caught,
because the prose described a benchmark that no longer exists in that form: the
non-incremental streaming side, and `FullRebuildAfterAppend`. Both are corrected
where they appear. `FoldedSessionGetAll` had no benchmark behind it at all and
its figures are gone rather than re-measured.

What is *not* stale: the structural claims — the incremental path is O(delta)
and independent of window count, folded windows are O(1) for line tracking,
`updateContent` skips unchanged content, and the streaming cycle is orders of
magnitude inside the 250ms tick. Those are properties of the code and were
re-verified by reading it; only the microseconds attached to them drifted.

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 0.01%** of wall time during streaming (a few μs per 250ms tick ≈ 0.001%)
3. **`updateContent()` skips unchanged content** efficiently — the one deliberate exception is the executing-tool spinner refresh (`InvalidateRunningToolSpinners`), which invalidates pending tool windows per tick so the header spinner keeps rotating during silent commands; it costs a ~100ns scan plus one window render, only while a tool executes (see [tool-spinner-refresh.md](tool-spinner-refresh.md))
4. **Incremental append is O(delta)** — no quadratic accumulation for long responses

## Key Design Decisions

### Incremental `appendDeltaToVisualLines`

`textRenderer.AppendFromTLV` appends each delta as **plain text** to
`appendDeltaToVisualLines`, which only wraps the delta and appends it to the existing
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
wrapped-line cache and trigger a full re-render — column widths and cell wrap
points are a whole-table property, so only the table transform needs the full
content.

Because cells now wrap instead of being truncated, a table-bearing window emits
more visual rows, and its per-delta re-render is correspondingly more
expensive than the old single-line-per-row transform. Measured on the 20-row
benchmark (`-benchtime=200x`, best of 3):

| Benchmark | Before | After | Change |
|---|---|---|---|
| `MarkdownStreaming_PlainDeltas` | 1195 allocs | 1195 allocs | **identical** |
| `MarkdownStreaming_RawMode` | 1194 allocs | 1194 allocs | baseline for the row above |
| `MarkdownStreaming_TableDeltas` | 17.0k allocs | 52.8k allocs | ~3x |
| `RenderMarkdownTables_Large` | 2.0k allocs | 3.5k allocs | ~1.7x |

**The allocation counts are the load-bearing claim; the timings are not
quoted deliberately.** Repeated `-benchtime=200x -count=7` runs put these
figures anywhere in a ±30% band (observed: `RawMode` 20.4–28.3μs, `TableDeltas`
1.52–1.81ms, `Large` 0.27–0.36ms) — an earlier revision of this table quoted
single-run microsecond figures and had to be "corrected" twice against pure
noise. Ratios are stable, point estimates are not.

What the allocations prove: markdown-mode streaming of non-table deltas costs
**exactly** what raw mode costs, one alloc above the raw baseline — the same
one-off as before this change, so the table path is provably entered by nothing
but table-touching deltas. The table-bearing rows are up because a record can
now span several visual lines and each column's grapheme widths are measured.

The headline property still holds exactly: **plain-text streaming in markdown
mode costs the same as raw mode** — the table path is only entered by deltas
that actually touch a table.

Mind the unit when reading `MarkdownStreaming_TableDeltas`: one iteration is a
whole 22-delta stream (header, delimiter, 20 rows), and every one of those
deltas touches a table, so an iteration performs 22 full re-renders. Its
1.5–1.8ms is therefore the cost of **streaming an entire table**, not of one
re-render. Measured separately on the same 20-row table at 120 columns:

| | |
|---|---|
| one re-render of the finished table | ~44µs (567 allocs) |
| one delta during the stream (whole stream ÷ 22) | ~70µs |
| the 250ms tick budget | 0.02–0.03% of it |

Both figures are best-of-3 at `-benchtime=2000x`, and their spread across
rounds was under 10%. So a window whose table re-renders on every single tick
still spends well under a tenth of one percent of its budget on layout.


The old approach (before the `WindowRendering` interface refactoring) used the same
optimization. It was accidentally dropped during the refactoring and restored in
commit `1021326`.

### Two-Tier Caching

| Cache | Location | Contents | Invalidated by |
|-------|----------|----------|---------------|
| Renderer lines | `textRenderer.wrappedLines` | Wrapped plain-text lines (AT/AR) | Resize, theme change |
| Body-colored lines | `textRenderer.colored` | Dim-colored copy of `wrappedLines`, materialized only while an overlay is active (`styles.Body` carries a foreground) | Content append (`coloredDirty`), resize, theme change, blocked switch |
| Window rows | `Window.cache` | Visual lines (`lines`), display widths (`widths`, lazy), rendered string + lineCount, the memoized cursor row and the memoized pinned row | Content append, resize, theme, receipt time (the pinned row also by the hidden-line count it carries — a scroll, not a content change) |

Renderer lines are **updated incrementally** during streaming (not invalidated).
The render cache is marked invalid on every content change but rebuilt on next render.
The pinned row is the one entry whose *input* is the viewport: it is keyed on the
count, so a scroll that does not change the count reuses it and a scroll that does
rebuilds that row alone.

`lineCount` lives in the render cache so `WindowBuffer` can read it with direct field
access (no interface dispatch on the hot path).

### Why `ensureLineHeights` Defers Full Render

During streaming, `ensureLineHeights` first tries `UpdateLineCountFast` → `TryLineCount`.
If the renderer's `wrappedLines` is populated, this returns the line count in ~1.1μs
without rendering. The actual `w.Render()` — which joins wrapped lines, composes
the window's own row, and renders the style layer — is deferred to `GetAll` →
`renderVirtual`, which needs the rendered output for the viewport anyway.
