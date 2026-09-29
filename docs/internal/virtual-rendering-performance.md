# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

Benchmarks run on: Intel(R) Core(TM) Ultra 9 285K, Linux amd64, Go 1.26.1,
against the tree at `e0abb8f9` plus this revision's changes to it. Every figure
this revision moved was re-measured on that machine on 2026-09-29 — the whole
suite, not a sample — and the method is part of the figure:

```
go test ./internal/adapters/terminal/ -run '^$' -bench '<Name>' -benchmem -benchtime 1s -count 6
```

Quoted value is the **minimum** of those runs, because that is the one number a
benchmark run reliably reproduces; where a minimum is a lone outlier the row
says it quotes the median instead. Tables this revision did not move keep the
figures — and the run count — of the 2026-09-28 re-measurement at `-count 10`,
and each table says which count its numbers came from. Four tables pin a fixed
`-benchtime Nx` rather than `1s`, and each says so, because those benchmarks
append on every iteration and their memory columns move with `-benchtime`.
Memory columns are the `B/op` `go test` prints; where prose rounds to KB it
divides by 1000, which is the convention this file's older figures were written
in.

What this revision changed in the code, so the figures can be placed — six
changes, each removing work a frame did and threw away:

1. **`Window.Render` split into rows and their join.** The viewport path clips
   `cache.lines` and `ensureLineHeights` reads only `LineCount`, so both now ask
   `buildLines` for the rows; the joined string is built on first request behind
   `joined()` and the two always-identical fields `cache.inner`/`cache.rendered`
   became one. [What is left in the frame path](#what-is-left-in-the-frame-path)
   recorded this as the frame's largest allocation and it was.
2. **`BuildInner` folds the streaming delta parts at a threshold**
   (`maxContentParts`) instead of on every frame, which was a full copy of the
   message per frame to produce a string that frame never read.
3. **The summary paths measure their message once.** `measure` returns the width
   *and* the walking route in one pass, and `measured.head`/`.tail` spend that
   answer; `cellWidth` gained the byte-wise ASCII route `walkCells` already had,
   and the tail cut takes it *backwards*, which is sound only because one ASCII
   byte is one cluster. A CPU profile had put the escape probe alone at a third
   of the folded frame: the same message was priced four times over per frame.
4. **`windowFragment` no longer measures every row of the window** to draw ≤40
   of them. The per-window width cache is gone, and what a row needs after it
   rides along in `visualLine.Pad`, so `renderVirtual` writes that many spaces
   instead of measuring the row to work them out. This was the one change of the
   five with a cost; the cost is closed, and both the cost and the closing are
   measured where it landed.
5. **`ensureCursorVisible` asks one pass instead of five to eight.** Every
   quantity it decides with is a prefix sum over the same line's clusters, and
   each walk encoded the line as a string first.
6. **The wrap stopped allocating per line and per byte.** `restyleBreaks` built a
   `WrapWriter` for every original line of a message, `WrapWriter.Write` allocated
   a one-byte slice for every byte it handed the parser, and a line's row widths
   were a fresh `[]int` per line under a comment saying they were one scratch. All
   three are gone: the style pass is skipped when there is no escape in the content
   to re-apply, the byte comes from a field on the writer, and the scratch is
   outside the loop. [wrapContent](#wrapcontent) carries the figures, and the
   condition the skip needs — which is not the obvious one.

Plus the stale figures in comments across `window.go`, `window_renderer.go`,
`width.go` and the benchmark files. Every number below is measured on the tree
that includes those changes; where a figure existed before them and moved, both
values are given.

The previous revision's changes are still in the tree and still described here:
the cluster cutters in `width.go` and the input chain's cluster helpers became
folds over one streaming walk instead of a materialized list
([the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed)),
and `tailCells`' styled branch stopped overrunning its budget.

An interim re-measurement on an AMD Ryzen 7 5800U (2026-09-28, Go 1.26.4) is
superseded by this one and is in the git history of this file rather than in
it. Its ratios carried; its absolute times were the slower machine's, and its
attribution of the drift was wrong, which
[What moved, and what moved it](#what-moved-and-what-moved-it) corrects with a
bisect.

## Summary

Working as designed:

- ✅ **Virtual rendering** — ~12x less work than rendering every window, and
  30x lighter: 100 windows at viewport 30 costs 1.38μs and 3,776 B/op with the
  viewport clip, against 16.7μs and 113,105 B/op without it
  (`GetAllWithVirtual` / `GetAllWithoutVirtual`). Neither side moved in this
  revision; the ratio is quoted from inside one batch because the unclipped side
  is the noisiest benchmark in the file and has landed anywhere from 14.6μs to
  19.5μs across batches — see
  [What moved](#what-moved-and-what-moved-it)
- ✅ **Incremental content append** — O(delta) per frame via
  `appendDeltaToVisualLines`, avoiding an O(n) full re-wrap: ~529x on a 26KB
  message (325 wrapped rows), 214 B against 99 KB per frame
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines`,
  no render, against the 273ns a whole `ensureLineHeights` pass with 1 dirty
  window costs
- ✅ **Streaming stays under 1ms** — 1.61μs per full cycle (append + line
  tracking + viewport render) against a 250ms tick, so the frame is ~0.0006% of
  the budget
- ✅ **Custom ScrollView** — 40 bytes of state holding the pre-clipped visible
  region; `View()` pads to the viewport height in 124ns and 4 allocs,
  `WithContent` is 0.09ns because there is no re-split
- ✅ **Soft-wrap fragment viewport** — `renderVirtual` clips to visual lines
  and emits continuous per-window fragments (`\n` only between windows), and
  measures a row's width only where it pads one, so a viewport render of 100
  windows costs 1.71μs (`WindowBufferGetAll`) and never touches the windows
  outside it — nor the rows of the ones inside it that are off screen (see
  [Soft-Wrap Fragment Rendering](#soft-wrap-fragment-rendering))

Two things were not fine, and both are fixed rather than recorded:

- 🔧 **A folded text window's summary row used to materialize every grapheme
  cluster of the message, twice, per frame.** Reasoning windows fold by default
  and re-summarize on every delta, so at 128KB that frame cost 13.5ms and
  56.8 MB — 41x the time and 76x the memory the *same content expanded* costs,
  which is backwards for the state whose whole job is to be cheap. Four rounds
  of whole-message work came out: the cutters' cluster list, the whole-message
  escape copy they cut out of, the four separate pricings of the same message per
  frame, and then the last pricing too — a text renderer now keeps the cells its
  content draws and the line breaks in it, summed per delta, so a folded row reads
  a count instead of walking the message. The frame is now **12.5μs and 134 KB**,
  and the same defect's other face — a keystroke at the end of a long prompt
  line, ~1ms and ~4 MB — is down to **~117μs and ~22 KB**. A third tail cut, the
  one the streaming tool previews use, turned out to split grapheme clusters; it
  does not now.
  See [The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed)
- 🔧 **Frames did work they threw away.** Five changes, each removing a pass or
  a copy that nothing read: `Window.Render` split into rows and their join, so
  the viewport path stops building a string per window per frame; `BuildInner`
  folds the streaming delta parts at a threshold instead of every frame; the
  summary paths price their message once instead of four times, and the tail cut
  runs backwards on plain ASCII; `windowFragment` stops measuring every row of
  the window to draw forty of them; and `ensureCursorVisible` asks one pass for
  the five to eight prefix sums it used to ask separately. At 128KB of content
  the **expanded** streaming frame went 231μs/757,278 B → **18.2μs/80,156 B**
  (12.7x, 9.4x) and the folded one 709μs/134,136 B → **63.9μs/133,801 B**
  (11.1x), and on to 12.5μs when the summary's last pricing pass went with it.
  The fifth of those first traded a per-rebuild cost for a per-frame one
  — `GetAllDimmed/dimmed` went 2.4μs → 5.8μs and 13,104 B → 15,824 B, because a
  frame that redraws without rebuilding then re-measured the rows it pads — and
  the trade is since closed: the wrap counts what a row needs after it at the
  moment it breaks the row, and the frame reads the count. That benchmark is back
  at **2.3μs and 13,104 B**, the bytes it was before the trade.
  See [What is left in the frame path](#what-is-left-in-the-frame-path)

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
TryLineCount → len(wrappedLines) + 1  (273ns for the whole ensureLineHeights pass, no render)
```

A dedicated assertion test (`TestIncrementalPathIsUsed`) verifies that
`TryLineCount` returns a valid count after every delta append. If the
incremental path breaks, this test fails immediately.

Markdown mode keeps this property for ordinary text: only table-touching
deltas re-render, and benchmarks show mdMode plain-text streaming within
noise of raw mode (`BenchmarkMarkdownStreaming_PlainDeltas` vs
`BenchmarkMarkdownStreaming_RawMode`; 1072 allocations against 1071).

Line tracking is also where the ⚠️ in the summary does *not* bite, and the
distinction is worth stating precisely because the two halves travel together:
for a folded window it never touches the renderer at all, so it is genuinely
free. What is not free is the row that gets drawn when that window is inside the
viewport.

### When Full Re-wrap Happens

Full `wrapContent` from scratch only occurs when:

| Event | Why |
|-------|-----|
| **Terminal resize** | Width changed, all lines must be re-wrapped at new width |
| **Theme switch** | Styles changed, all lines must be re-styled and re-wrapped |
| **First render** | No cached wrappedLines yet |
| **Table-touching delta** (markdown mode) | Column widths and cell wrap points are whole-table properties, so the cache is invalidated and the content re-wrapped — step 2 above |
| **Markdown mode toggled** (`r`) | `ToggleMarkdownMode` invalidates the wrapped-line cache, so the next `BuildInner` re-renders from scratch |

During normal streaming of ordinary text, none of these happen — the
incremental path is used exclusively. A stream that is building a markdown
table takes the last row on every delta, which is what
`BenchmarkMarkdownStreaming_TableDeltas` prices.

## Benchmark Results

### Streaming Performance (Realistic 250ms Tick)

Minimum of 10 runs at `-benchtime 1s`, except the last row (see its section).

| Metric | Value | Memory | Allocs |
|--------|-------|--------|-------:|
| Average full cycle (append + line tracking + GetAll), `StreamingUpdateWithVirtualRendering` | **1.61μs** | 4,004 B | 40 |
| Incremental append only, `JustAppendUpdate` | **46ns** | 81 B | 0 |
| Small delta streaming (append + line tracking), `StreamingSmallDelta` | **267ns** | 217 B | 5 |
| Long content incremental append (26KB message), `AppendVsFullWrap_LongContent/incremental` | **0.30μs** (median, `-benchtime 300x`) | 214 B | 5 |
| Budget | < 1ms (target), 250ms (actual tick) | | |

`JustAppendUpdate` reports 81 B/op and 0 allocs/op because the bytes are the
amortized growth of the content buffer, not an allocation per call.

### Incremental Append vs Full Re-wrap (26KB message)

Measured via `BenchmarkAppendVsFullWrap_LongContent`: one window holding 500
repeats of a 52-character sentence — 26,000 bytes with no newline in it, which
wraps to 325 rows at 80 columns (326 counted lines with the window's own). This
section said "5000-line content" until 2026-09-28; the benchmark's content was
always 325 wrapped rows, and the benchmark's own comment said so too.

Both sides append on every iteration, so `-benchtime` is part of the figure:
**median of 6 runs at `-benchtime 300x`** — medians rather than minima because
the incremental side is sub-microsecond, where one fast run is not a typical one
(the six land between 274ns and 335ns).

| Operation | Time | Memory | Allocs |
|-----------|------|--------|-------:|
| **Incremental append** | **0.30μs** | **214 B** | **5** |
| Full re-wrap | 0.158ms | 99,374 B | 48 |
| **Speedup** | **~529x** | **~464x** | **~9.6x** |

Both sides moved when the wrap stopped allocating per line and per byte
([wrapContent](#wrapcontent)), and the full re-wrap by far the more: this row was
0.54ms, 242,110 B and 27,293 allocs against an incremental 1.10μs, 648 B and 58.
The allocation ratio is the one to read as an inversion rather than a loss —
**~471x became ~9.6x** — because most of what the full re-wrap allocated was one
slice per byte of content, and that was a defect rather than a cost. The time
ratio holds (~491x → ~529x) and the memory ratio improved (~374x → ~464x) for the
same reason: those per-byte slices were most of the 242 KB.

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 0.158ms
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

**Minimum of 3 runs at `-benchtime 200x`** — pinned because both sides append
per iteration and the non-incremental one's memory grows with the run: the same
pair at `300x` reads 7,386 B and 72 allocs for the side that re-wraps, while
the incremental side does not move (3,943 B, 40 allocs) because it is O(delta).
That growth used to be much steeper — the row read 14.9 KB and 855 allocs at
`300x` against 11,240 B and 601 at `200x` — because most of what the re-wrap
allocated was one slice per byte of content, and the content grows with the run.
The benchtime is still part of the figure; it is now a small part.

| Scenario | Time | Memory | Allocs |
|----------|------|--------|-------:|
| **Incremental (1 dirty window)** | **1.54μs** | 3,956 B | 40 |
| Full re-wrap of the streaming window | 5.33μs | 6,409 B | 71 |

Window count does not change the incremental cost, and the reason is structural
rather than empirical: `ensureLineHeights` touches the one dirty window and
`renderVirtual` only the windows the viewport overlaps, so nothing in the frame
iterates the history. A probe at 50, 100 and 400 history windows reported the
same per-frame memory and allocation count at each, with the times inside
run-to-run noise; those figures were 4,491 B and 93 allocs when the probe was
run and are 4,004 B and 40 today, the wrap's per-line and per-byte allocations
having gone ([wrapContent](#wrapcontent)). The structural claim is what the probe
was for, and it is unchanged: the incremental path is O(delta), independent of
history length.

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual`
(100 windows, viewport=30 lines). Minimum of 6 runs at `-benchtime 1s`: the
unclipped side is the noisiest benchmark in the file — its minimum has landed
anywhere from 14.6μs to 19.5μs across batches, this one at 16.7μs — because it
is the one that renders ~500 rows instead of 30. Take a ratio from inside one
batch, never from two. Neither side changed in this revision; the batch before
it measured 1.41μs and 17.5μs.

| Scenario | Time | Memory | Allocs | Speedup |
|----------|------|--------|-------:|:-------:|
| `GetAll` with virtual rendering (100 windows) | **1.38μs** | 3,776 B | 35 | **12.1x** |
| `GetAll` without virtual rendering (100 windows) | **16.7μs** | 113,105 B | 14 | baseline |

Memory is the sharper claim and it does not depend on the clock: **30x less
allocated per frame**, because the clipped side builds 30 rows and the unclipped
side builds all ~500.

### Line Height Tracking

| Scenario | Benchmark | Time | Memory | Allocs |
|----------|-----------|------|--------|-------:|
| Incremental, 1 dirty window (20 windows) | `JustEnsureLineHeights` | **273ns** | 212 B | 5 |
| Incremental, 1 dirty window (100 windows) | `EnsureLineHeightsIncremental` | **333ns** | 247 B | 5 |
| `lineHeights` array rebuilt over 100 windows | `EnsureLineHeightsFullRebuild` | **253ns** | 0 B | 0 |
| Every window re-wrapped from scratch (50 windows, 80↔120 cols) | `WindowBufferResize` | **0.072ms** | 81,601 B | 1,900 |

Two rows this table used to carry are gone rather than re-measured:
"~150μs (1 dirty window, uncached)" and "~7.1ms (all 100 windows rendered from
scratch)", both starred as historical estimates. Nothing in the tree measures
either, and the second is not what the benchmark named for it does —
`EnsureLineHeightsFullRebuild` sets `dirtyIndex = dirtyFullRebuild` but never
invalidates the windows, so every `Render` inside it hits the render cache and
the 253ns is the cost of rebuilding the height array from cached counts. A
genuine from-scratch pass is the resize row: that one does invalidate, and pays
a full re-wrap per window.

### Full Update Cycle (Delta + GetAll)

Measured via `BenchmarkWindowBufferDeltaWithGetAll` (100 windows, delta to last
window, viewport 30). Minimum of 10 runs at `-benchtime 1s`.

| Metric | Value | Memory | Allocs |
|--------|-------|--------|-------:|
| Delta + GetTotalLines + GetAll (incremental, 100 windows) | **1.89μs** | 6,357 B | 41 |

The full-rebuild side is not comparable and is no longer quoted here.
`BenchmarkFullRebuildAfterAppend` — which this section used to cite as "all
windows invalidated", 1.7ms, ~425x slower — invalidates and re-renders **one**
window of ~310 characters. It measures what a single window's full re-render
costs, not what a full rebuild of the buffer costs, so the two numbers were
never a ratio.

### Cursor Movement

Measured via `BenchmarkVirtualRenderingCursorMovementSingle`,
`BenchmarkVirtualRenderingCursorMovement` and `BenchmarkVirtualRenderingScroll`
(100 windows, viewport=30). Minimum of 10 runs at `-benchtime 1s`.

| Metric | Value | Memory | Allocs |
|--------|-------|--------|-------:|
| Single cursor move (EnsureCursorVisible + updateContent) | **1.99μs** | 6,254 B | 38 |
| 20 cursor moves through the buffer | **39.3μs** | 125,090 B | 760 |
| Scroll 20 steps down + 20 steps up | **64.0μs** | 177,282 B | 1,400 |

### Collapsed-Window Design (single-line fold headers)

The collapsed-window design replaces the bordered fold (3 content lines +
2 border lines) with a single marker line (`LABEL summary`:
text windows show the escaped head + "…" + tail of the content (40/60
split), tool windows the first input line; only streaming delta windows
use leading "…" since the user only cares about the latest chunk).

Measured via the benchmarks in `folded_bench_test.go`. The first three rows run
over one session shape — 120 windows at width 120 with a 40-row viewport,
holding **100 folded windows (80 tool + 20 reasoning) and 20 unfolded ones
(10 assistant + 10 user)**, which is what the fold defaults in
`WindowBuffer.AppendOrUpdate` produce. (This section and that file's header both
said "110 folded" until 2026-09-28; the counts come from the tags, and AT/UT
open expanded.) The fourth row is `BenchmarkFoldedTextStreamingDelta/2KB/folded`,
which builds its own single-window buffer and is the subject of the next
section. Minimum of 10 runs at `-benchtime 1s`; of 4 for the fourth row.

| Scenario | Value | Memory | Allocs |
|----------|------:|--------|-------:|
| `GetAll` viewport render of the whole session | **6.75μs** | 46,464 B | 51 |
| 20 cursor moves (j/k) through it | **145μs** | 929,787 B | 1,040 |
| Delta into a folded **tool** window (Uf preview) | **104ns** | 88 B | 2 |
| Delta into a folded **text** window, 2KB content | **3.75μs** | 4,776 B | 51 |

The three figures this section quoted before 2026-09-28 were deleted on the
grounds that "the folded-session benchmark these figures came from is gone from
the tree". It was not gone: `folded_bench_test.go` has held all three benchmarks
since the collapsed-window redesign, they run in the normal suite, and they
reproduce those figures (7.9μs → 6.75μs, 0.17ms → 149μs, 0.11μs → 104ns). They
are restored here, re-measured, with the fourth row that was missing.

Why the shape is cheap where it is cheap:

- **Folded windows are O(1) to track**: `UpdateLineCountFast` returns `1`
  immediately for folded windows — no wrapping, no row render, no renderer
  access. The tool window's summary shows the first input line, which appends
  never change, so its row is O(1) too: that is the 105ns row above.
- **No full-content wrap on fold**: `BuildCollapsed` wraps nothing. For a text
  window it still *reads* everything — see the next section.
- **Cursor moves don't rebuild a window's rows**: only the one navigational element
  is recolored — the fold marker and the label column on a folded line, all
  of row 0 (marker, label, timestamp, and the pinned row's count) on an
  expanded one — reusing the cached content (`renderCursor`,
  `renderCache.line0Cursor`).
- **Fewer total lines**: 1 line per folded window, and one row of chrome per
  expanded window (its label opens the window; there is no header line above
  it and no closing rule below it), shrinking `lineHeights`/scrolling math
  proportionally. The 120-window session above is 190 document lines.

Unfolded windows pay a small cost for their own line (the window's label
composed into it, plus the timestamp — 1.00μs per delta via
`BenchmarkWindowBufferDelta`). Which of the two states is dearer used to depend on
the length of the message, with a crossover between 2KB and 32KB. It does not any
more: the folded side prices its summary from counts kept as the content grew, so
it is the cheaper state in **time** at 2KB and 128KB and indistinguishable from
expanded at 32KB (0.97x, inside the run-to-run band), and the dearer one in
**memory** above 2KB, because summarizing still joins the message into one string
while an expanded frame appends one delta to rows that are already wrapped.
Minimum of 6 runs at `-benchtime 1500x`, both sides from the same binary:

| Content size | Folded | Expanded | Folded ÷ expanded |
|---|---:|---:|---:|
| 2KB | 3.38μs, 4,776 B | 3.92μs, 8,816 B | **0.86x time**, 0.54x memory |
| 32KB | 5.76μs, 35,496 B | 5.96μs, 24,177 B | **0.97x time**, 1.5x memory |
| 128KB | 12.6μs, 133,800 B | 14.2μs, 73,332 B | **0.89x time**, 1.8x memory |

Reasoning windows fold by default and are the ones that get long while streaming,
so the time column is the one that matters for them, and the memory column is what
is left to argue about. The previous revision had this the other way round in
memory (5,112 B against 20,064 B at 2KB, 134 KB against 757 KB at 128KB)
because the expanded frame still carried the whole-window work listed in
[What is left in the frame path](#what-is-left-in-the-frame-path); that work is
gone, and what is left on the folded side is itemized there too.

### The fold summary materialized every cluster (found and fixed)

`BenchmarkFoldedTextStreamingDelta` was added by the previous revision to price
the one frame shape nothing else in the tree measured: a folded **text** window
streaming. It rebuilds its buffer under `StopTimer`, so one iteration is exactly
one frame at a fixed content size and its memory columns mean the same thing at
any `-benchtime`. Each size runs folded and expanded on identical content; the
expanded side is the control. Minimum of 4 runs at `-benchtime 1s`.

| Content size | Folded: as found | cutters streamed | summary cut before escaping | frame priced from counts (now) | Expanded: as found | Expanded: now |
|---|---:|---:|---:|---:|---:|---:|
| 2KB | 99.0μs, 503,259 B, 95 | 17.5μs, 12,312 B, 63 | 15.1μs, 5,112 B, 55 | **3.38μs, 4,776 B, 51** | 7.84μs, 20,064 B, 97 | **3.92μs, 8,816 B, 75** |
| 32KB | 4.20ms, 13,739,011 B, 127 | 249μs, 222,752 B, 73 | 193μs, 35,833 B, 55 | **5.76μs, 35,496 B, 51** | 57.0μs, 221,878 B, 106 | **5.96μs, 24,177 B, 75** |
| 128KB | 13.5ms, 56,779,754 B, 143 | 934μs, 779,824 B, 77 | 770μs, 134,136 B, 55 | **12.6μs, 133,800 B, 51** | 220μs, 757,253 B, 111 | **14.2μs, 73,332 B, 75** |

The first three folded columns are the previous revision's, quoted as it
measured them (minimum of 4 at `-benchtime 1s`); the last three are this tree's
(minimum of 6 at `-benchtime 1500x`). The two benchtimes are comparable here and
would not be elsewhere: this benchmark rebuilds its buffer under `StopTimer`, so
one iteration is exactly one frame at a fixed content size, and the memory
columns do not move with `-benchtime` — the folded 133,800 B and expanded
73,332 B are the same figures a `-benchtime 1s` run reports.

The fourth folded column is labelled for the change that moved it most and spans
two: pricing the summary from counts kept as the content grew, and then the
wrap's per-line and per-byte allocations going
([wrapContent](#wrapcontent)). It read 3.75μs / 18.1μs / 63.9μs between them —
the middle and last of those were this document's own stale figures, left behind
when the pricing change landed and corrected here. End to end the folded frame is
**29x faster and 105x lighter at 2KB, 1,070x and 424x at 128KB**, and the
expanded control — which nobody set out to fix — is **15.5x faster and 10.3x
lighter at 128KB**, because three of the six changes were about work an expanded
frame did and discarded and the sixth was mostly about expanded frames.

**The inversion this benchmark was added to expose has flipped, and then flipped
again in memory only.** Folded used to be 12x–81x dearer than expanded. It is not
dearer in *time* at any size measured: 3.38μs against 3.92μs at 2KB, 5.76μs
against 5.96μs at 32KB — inside the run-to-run band, so call those two equal —
and 12.6μs against 14.2μs at 128KB. It is dearer in **memory** above 2KB, and by
more the longer the message: 1.5x at 32KB, 1.8x at 128KB. That is what the two
states now cost:

- an **expanded** frame appends one delta to the already-wrapped rows and draws
  the ≤40 of them the viewport shows — O(delta + viewport), independent of how
  long the message has become;
- a **folded** frame re-derives head + "…" + tail of the *whole* message and
  joins the streaming delta parts into one string to read it — O(budget) in the
  work it does over the message, O(content) in the bytes it copies to reach them.

Folding trades "draw forty rows" for "summarize everything". That trade now wins
on time at every size measured and loses on memory above 2KB, and the memory is
the join. Reasoning windows fold by default and stream for as long as the model
thinks, so the join is the per-tick cost of a long "thinking" phase.
[What is left in the frame path](#what-is-left-in-the-frame-path) prices what is
still O(content) in the folded frame and says what would make it O(budget).

Before either fix the two sides were the wrong way round — folding a text window
cost 12x to 81x *more* per frame than opening it — and the allocation count
barely moved (95 → 127 → 143) while the bytes scaled with the message. That
signature is what localized it: not more work per row, one big materialization
per call, twice.

There were two materializations, and they were found one at a time.

**First: the cutters built a cluster list.** A folded text window's row is
head + "…" + tail of its content: `textRenderer.BuildCollapsed` →
`collapsedSummary` → `headAndTailParts` → `takeCells` (head) and `tailCells`
(tail). Both answered from a `clusters(s)` helper that returned *every* grapheme
cluster of the string as a `[]cluster` — one struct per cluster, each holding
its own substring — so keeping ~30 cells from the front and ~45 from the back
built the whole message's cluster list twice. An allocation profile of the 32 KB
folded frame put **98.4% of everything it allocated in `clusters`**. Measured
alone, `clusters` over a 2 KB string was 44.6μs and 245,408 B: ~120 B of garbage
per input byte, for answers that are O(1).

The same helper was the input chain's width source (`graphemeClusters` wrapped
it and copied the result into a second slice), and `ensureCursorVisible` asks
four or five such questions per keystroke over the whole current line. So the
defect had two user-visible faces: a folded reasoning window re-summarizing
itself every tick, and typing at the end of a long prompt line.

`width.go` already owned a streaming cluster walk — `walkCells`, which
`hardwrapCells`, `keepCells` and `dropCells` were built on. The cutters were the
one family not using it, and the one path reading a *second* set of table
options (`widthModel` instead of the walker's `breakerModel`), which is exactly
the divergence this file's header exists to prevent. So:

- `walkCells`' callback now returns `bool`, and `false` stops the walk. A
  prefix question ends at its budget instead of at the end of the string.
- `takeCells` is a fold that stops at the budget; `tailCells` is one forward
  pass that stops at the first boundary whose remainder fits. Each returns its
  cut as a **copy**, not as the substring it came from: the summary is cached on
  the window, and a 30-cell prefix must not hold a 128 KB message alive.
- `widestCellCluster` is a max fold — zero allocations, was one list.
- `clusters` is gone from production and lives in `width_test.go` as the oracle
  the cutters are checked against, which is a better place for it: an oracle
  that shares an implementation with the code under test cannot disagree.
- `input_field.go` got `walkLineClusters` — one encoding of the line per walk,
  no list — and its six callers are folds over it. `truncatePlaceholder` turned
  out to be a second implementation of `takeCells` and is now that call.

That took the 128 KB frame from 13.5ms and 56.8 MB to 934μs and 780 KB. The
profile of what was left named the second materialization precisely: 65.6%
`utf8.AppendRune`, 16.9% `ReplaceAll`, 16.8% the `rawContent` join — no
`clusters` in it at all.

**Second: the summary escaped the whole message before cutting it.**
`headAndTailParts` did `strings.ReplaceAll(content, "\n", "\\n")` over the
entire content, then kept ~75 cells of the result; `expandTabs`, called on the
way there by `prepareContent`, rebuilt the entire content rune by rune through a
`strings.Builder` even when it contained no tab (its sibling `stripANSI` has the
early-out it was missing). Two thirds of the remaining garbage was that one
missing guard.

The order is simply wrong, and reversing it is free: **cut first, escape the
cut, cut again to the same budget from the same end.** Escaping can only widen a
character — a `\n` is 0 cells and its marker is 2, a `\r` is 0 cells and is
deleted, everything else keeps its width — so the escaped text that fits a
budget always sits inside the raw cut, and re-cutting lands on the same
boundary. `escapedWidth` answers "would it all fit once escaped" as
`cellWidth(s) + 2·count("\n")`, which needs no escaped string to exist.
`TestSummaryEscapeOrderIsEquivalent` keeps the escape-first implementation
verbatim as an oracle and runs both orders over the summary corpus at every
budget from 0 to 40, in both the raw and `prepareContent`'d shape: **2,050
(content, budget) pairs agree byte for byte**, heads, tails and the truncated
flag. `TestTailPartsEscapeOrderIsEquivalent` does the same for `tailParts` over
the single-rune-cluster entries: **1,312 pairs agree**.

**A correctness bug came out of the second one.** `tailParts` — the tail cut the
*streaming tool previews* use (`Af` argument deltas, `Uf` execution snapshots) —
was a third implementation of the same job, and it walked `[]rune` backwards
measuring one rune at a time (`cellWidth(string(runes[i]))`, a string allocation
per rune examined). Rune-wise, not cluster-wise, so it split multi-rune
clusters: `tailParts("aaaa 👨\u200d👩\u200d👧\u200d👦", 2)` returned ZWJ+boy, the
back half of a family emoji, on the row a user watches while a command runs.
`TestTailPartsNeverSplitsACluster` states the invariant over the escaped content
(a `\n` marker is two clusters once escaped, so a 2-cell budget may legitimately
keep only its `n`; a grapheme cluster may never be cut) and the old
implementation violates it in **48** of the checked cases. It is `tailCells`
now, so it is cluster-aligned with every other cut in the adapter.

| | per keystroke at the end of a 4000-cell line | one width sum over 10,000 runes |
|---|---|---|
| as found | ~1 ms and ~4 MB (`InputFieldInsertLongLine`: 19.6ms, 81.8 MB for a build + 20 keys) | 523μs, 2.1 MB, 21 allocs |
| after the cutters streamed | ~300μs and ~57 KB (6.10ms, 1.14 MB, 131 allocs) | 109μs, 20.5 KB, 1 alloc |
| after one pass asked them all (now) | **~117μs and ~22 KB** (**2.33ms, 442 KB, 46 allocs**) | **106μs, 20.5 KB, 1 alloc** |

`InputFieldMoveLongLine` (a build plus 200 arrow moves) went 1.27ms → 471μs →
**232μs** and 5.2 MB → 156 KB → **115 KB**; `InputFieldViewLongLine` (one
`View()` of a 2000-cell line) went 81μs → **24.9μs** and 306 KB → **5,064 B**.
Movement was always cheaper than insertion because `handleMovement` does not call
`ensureCursorVisible`; that asymmetry is unchanged and is now the only thing
separating the two.

The last row of the first column is the five-to-eight walks becoming one. Every
quantity `ensureCursorVisible` decides with is a prefix sum over the same line's
clusters — where the stored visible start anchors now that inserts have shifted
the boundaries, the cells before it, the cells before the caret, the width of
the cluster under the caret, whether the line fits at all — and each walk encoded
the line as a string before it could segment it, which is where the 1.14 MB went
(57 KB per keystroke is five 12 KB encodings). `probeLine` takes them in one
pass and stops early once the answers cannot change. It is a rewrite of
arithmetic that decides where the caret is drawn, so it is held to the old
function verbatim as an oracle over **83,712** swept states and 4,000 random
operation sequences, on exact equality of the visible start — not merely "the
invariants still hold", which `TestInputFieldFuzzInvariants` separately checks
over ~2.4M operations. The sweep found one real difference on the way: a caret
*inside* a multi-rune cluster is priced from the truncated prefix, not from the
cluster start, and the pass had to reproduce that.

An oracle is a comparison between two implementations of this package, so it cannot
say whether either survives the rest of the way out — the field's `View`, the window
layout, the screen's row diff, and a terminal's own width table.
`caret_e2e_test.go`, at the module root, drives the built binary inside tmux and
requires the column tmux reports the caret at and the text tmux shows there to
describe one window of the line, over ASCII, CJK, and a line mixing the two.

The cutters were not the only caller paying for the list, and the missing tab
guard was not only the summary's. Markdown expands tabs per table line and per
wrapped row, so both fixes landed there too: `RenderMarkdownTables_Large`
293μs/646,897 B/5,418 allocs as found → 135μs/197,941 B/3,096 → **124μs** on
this revision's byte-wise ASCII route through `cellWidth`, which is *below* what
it measured before `c637636c` moved the per-cell cut onto the list (210,282
B/3,503), and one 20-row table render 807 → 567 allocs, the figure the old notes
quoted. `FullWrap` 41,995 B → 29,066 B and `WindowBufferResize` 7,900 → 6,900
allocs are the same missing tab guard, one allocation per wrapped line; the
resize is **150μs** now against 164μs, because a full rebuild asks `buildLines`
for 50 windows' rows and no longer joins any of them.

**What pins it.** Five tests from that revision: two on allocation shape, two
differential against the implementations they replaced, one on the cluster
invariant the rune walk broke. Allocation shape:
`TestCutsCostTheCutNotTheString` (both cutters, both folds and the three new
`measure` entry points, over 100-cluster and 10,000-cluster inputs at the same
budget) and `TestLineQueriesCostOneEncoding` (the four input-chain queries over
100-rune and 10,000-rune lines) fail above a small ceiling — a materializing
implementation reports one allocation per cluster, ~10,000 against a ceiling of
8. The ceiling is not an exact count because `AllocsPerRun` reads process-wide
mallocs and a `-race` build adds one of its own; the first version of the first
test failed under `-race` for exactly that reason. Behaviour: the two
differential tests above and `TestTailPartsNeverSplitsACluster`, plus the
invariants that already existed and still run — budget over the whole breaker
corpus, maximality and cluster alignment against the oracle, the styled-cut
guarantees, and 20,000 random lines of the structural test.

**What pins this revision.** Eighteen more tests, one group per change. They are
listed with what each would catch, because a refactor that only has to be
*equivalent* is the kind that silently stops being so:

- *rows vs join* (`window_join_test.go`, 6): a viewport frame and a resize must
  leave `joinedDone` false — the two tests that fail if an eager join comes
  back; `Render` must still return exactly the rows joined, in both registers,
  with row 0 and only row 0 swapped; the join must not survive an invalidation;
  a folded window's `Render` returns its row rather than a copy; a window with
  no renderer draws nothing on both entry points.
- *delta folding* (`streaming_compaction_test.go`, 3): the same message renders
  the same rows whatever its fold history, compared through the one path that
  reads `r.content`; the pending list never exceeds `maxContentParts` over 1,280
  deltas; below the threshold the fast path folds nothing — and its leaving them
  pending is also the proof the fast path ran.
- *measuring once* (`width_test.go`, 4): `measure(s).head/tail` **is**
  `takeCells/tailCells(s, n)` over three corpora at ten budgets, and
  `measure(s).escape` is `hasEscape(s)`; `measured.walk` visits exactly what
  `walkCells` would, which is the only thing that could part them; the backward
  ASCII tail equals a forward-walk oracle written out in the test, over long
  bodies and the zero-width-byte cases where the two arguments differ; and the
  byte-wise ASCII rule is held against displaywidth's own table over every ASCII
  byte, every pair of the awkward ones, and the sequences where segmentation is
  not per-byte — this last one because `cellWidth` and `walkCells` now price
  ASCII through the *same* function, which made the existing
  `TestWalkCellsAgreesWithCellWidth` unable to catch a rule wrong for both.
- *row widths* (`frame_padding_test.go`, 2): row 0 of any window, in both fold
  states at four widths, is never followed by a continuation — the property that
  lets the frame measure the row it draws, since row 0 is the one the cursor
  swaps; and a cursor frame is byte-for-byte the rows as drawn, padding included.
- *the caret pass* (`cursor_probe_test.go`, 3): the oracle comparison described
  above, plus `probeLine`'s six answers each checked against the walk they
  replaced. Outside this package, `caret_e2e_test.go` (3 cases) asks a terminal
  where the caret ended up and what it drew there.
- *the row's own padding* (`row_padding_test.go`, 8): that every row asks for the
  spaces the frame would have measured for it — `max(0, width − cellWidth(text))`
  where a continuation follows and 0 where one does not — over 1,465 rows of a
  corpus of wide clusters, combining marks, tabs and styled text at eight widths;
  that `hardwrapCellsWidths` breaks exactly where `hardwrapCells` does and reports
  one width per row; that `wrapRows` rejoined is `wrapContent`; the same rule over
  every row of every window in both fold states and both style registers, and over
  every step of both streaming delta paths; that a box asks for none; that a row is
  still 24 bytes, which is the whole reason the count is an int32; and, at frame
  level, that the bytes written are the rows with the padding they ask for.

  The frame-level case needs wide content, and that is worth saying plainly:
  `TestCursorFramePadsTheRowsItDraws` cannot see any of it, because its fixture is
  ASCII and an ASCII row a hard wrap broke is exactly the width. A padding count of
  zero and a count nobody consulted look identical there — it passed unchanged
  through every mutation below.
- *the summary's counts* (`summary_pricing_test.go`, 5): the differential is the
  same code with the counts retired (`summaryBytes = -1`), which sends a folded row
  down `prepareContent` and `measure` — what it did before this — so the reference
  is the implementation replaced and not a restatement of it. Two renderers take the
  same bytes as two deltas split at every offset, and their folded rows must be
  identical at twelve widths in three style registers. 149 of those cases keep the
  counts and 246 retire them, which is the part that makes it a comparison: a corpus
  that quietly retired every count would diff the slow path against itself and prove
  nothing. Beside it — that the fit rule agrees with the row the summary actually
  draws, `cellWidth(escapeBreaks(content))`, which shares no code with `escapedWidth`
  (the rule `priceSummary` spends) or with the counts `summaryContent` keeps, so a
  line break priced at anything other than the two cells it draws fails here; that
  folding the pending deltas leaves the counts
  alone, with the content appended one byte at a time so that there is a delta
  boundary between every two bytes; that a byte which retires the counts retires
  them for good; and that a renderer built with its content already in it is
  measured rather than priced at zero cells.

Four of these were mutation-checked — the change reverted or broken on purpose,
to confirm the test fails: folding every frame instead of at the threshold, and
never folding; `asciiCells` pricing a control as one cell; the backward tail
scan using `>=` for its budget and ignoring zero-width bytes. Each was caught,
the first two by more than one test. The padding rule was mutation-checked seven
more ways: the count off by one; padding asked for by every row rather than only
the ones a continuation follows; the count dropped where a row is recolored, which
is the silent failure the dimmed register would otherwise hide, and is caught by
exactly one test; the row's width recorded after the running count was reset
instead of before; and the frame ignoring the count, writing one space too many,
and writing one too few. Each was caught. The summary's counts were
mutation-checked eight more ways and each was caught: letting a tab through, and
letting a carriage return through, so that the content measured is not the content
counted; pricing a line break as one cell; not counting line breaks at all;
dropping the `+2` the fit rule owes them; dropping the check that the bytes counted
are the bytes of the content being summarized, and weakening that check to `<=`;
and losing the counts at a fold. Two of the eight were caught by one test each, and
one — the fit rule's missing `+2` — was caught by none at all until a test was
written against a statement of the rule that the branch under test does not share
code with.

That oracle has moved once since, and the reason is worth keeping. It was
`escapedWidth`, which at the time stood beside the fit check as a second statement
of the same question. The price of a summary then became a value of its own
(`summaryPrice`, built by `priceSummary`) and `escapedWidth` moved inside it, so a
test written against `escapedWidth` would have been comparing the rule with itself
again — the same tautology, one level up. The oracle is now the escaped row measured
for real, `cellWidth(escapeBreaks(content))`, and seven mutations run against that
shape were all caught: the fit check reading the raw cell count instead of the
escaped width; `escapedWidth` pricing a break at one cell; the cached price
forgetting the breaks; `priceSummary` never escaping; the head taking 60% of the
budget rather than 40%; the measured fallback skipping `prepareContent`; and the
branch that fits returning the content unescaped.

Two other mutations survived the shape before this one, and neither is writable now.
`headAndTailParts` used to take a measurement and a line-break count as separate
arguments, so every caller had to keep them in step, and passing `0` for the count
at `userRenderer.BuildCollapsed` — or `7` at `renderUFOnlyCollapsed` — passed the
whole package. Nothing catches a pair assembled wrongly when the assembling is
spread across the callers. There is one constructor and one cached path now, and
neither can hand over a count belonging to a different string; the two cold call
sites read `headAndTailParts(priceSummary(content), room)` and have no integer to
get wrong.

**What is left, deliberately.** The folded frame is 12.5μs and 134 KB at 128 KB of
content — 0.005% of a 250ms tick, against the 5.4% and 56.8 MB it was — and one
thing in it is still proportional to the message rather than to the row:

- **the join.** `rawContent()` folds the streaming delta parts into one string
  before anything can cut it. A memory profile of the frame puts **97%** of its
  allocation in that call (131 KB of 134 KB), and with the pricing pass gone it is
  most of the time too: a 12.5μs frame against a join measured at 19μs on its own
  at this size. It is a garbage problem first and a latency one second, and it is
  why the revision that removed the pricing pass reports no change in bytes.

The pricing pass that used to be listed beside it is gone: `measure` walked the
whole message — 37μs of the then-64μs frame — to answer "does this fit in 70
cells?", a question settled after 70 cells. A text renderer now keeps the cells and
the line breaks, summed per delta, and a folded row reads the counts. **The folded
frame at 128 KB went 66μs → 12.5μs and at 32 KB 20μs → 5.9μs**; the expanded frame,
which builds no summary, did not move. [What is left in the frame
path](#what-is-left-in-the-frame-path) states the rule for when the counts may be
kept and what retires them.

Removing the join means deriving head and tail from `contentParts` directly, and
that is deliberately not attempted here: a grapheme cluster can straddle a part
boundary (a combining mark arriving in its own delta), and `prepareContent` is
not chunk-safe at all — `expandTabs` is a column state machine, so a tab's width
depends on everything before it, and an escape sequence can straddle too. The
counts above sidestep that rather than solve it: they are kept only for content on
which `prepareContent` is the identity and one byte is one cluster, so there is no
boundary for anything to straddle. Widening them to content that does straddle
would mean carrying the boundary along with the count, and at 12.5μs a frame the
join is not asking for it.

### What is left in the frame path

This section recorded three things in `Window.Render` and its caller that were
proportional to the whole window while a frame shows at most `viewportHeight`
rows of it. **All three are fixed.** They are kept here with what each cost and
what each was worth, because the third one has a price and the price is real.
Minimum of 6 runs at `-benchtime 1s`; the three figures per item are the 128 KB
expanded streaming frame, measured after each change in isolation.

1. **`BuildInner` compacted the delta parts on every render.** Its fast path
   merged `contentParts` into `r.content` through a `strings.Builder` — a full
   copy of the message per frame — with a comment saying it prevents unbounded
   part growth. A threshold serves that purpose without the copy, so it now
   folds at `maxContentParts` (64): a 128 KB message copies 2 KB per delta
   instead of 128 KB per frame. **235,094 B → 103,734 B, 124μs → 104μs.** The
   fold lives in one place (`mergeParts`) and the read in one place
   (`rawContent`), so they cannot drift; nothing in the tree depends on the fold
   having happened, because the only path that reads `r.content` directly is the
   full re-wrap, which folds first.
2. **`Window.Render` joined the whole window and the shipped frame path never
   read it.** The only production caller of `GetAll` (display.go) sets a viewport
   immediately before, so `renderVirtual` runs and takes its rows from
   `cache.lines[from:to]`; the joined string is read by `renderAll` and
   `renderCursor`, which that path does not reach. `Render` is now `buildLines`
   (rows) plus `joined()` (their projection, built on first request), and the
   callers that want rows — `windowFragment`, and `ensureLineHeights`, which on a
   resize renders every window including the ones far off screen — ask for rows.
   `cache.inner` and `cache.rendered` were only ever assigned the same string, so
   they became one field: two names for one value is a drift waiting to happen.
   **757,278 B → 235,094 B, 231μs → 124μs.**
3. **`windowFragment` measured the width of every row to draw ≤40 of them.** It
   filled `cache.widths` for all of `cache.lines` and then sliced `[from,to)`,
   and the cache was per render generation — so during streaming, when every
   delta rebuilds the rows, a frame measured ~2,600 rows to draw 40. The cache
   is gone rather than re-keyed: `renderVirtual` measures a row where it pads it,
   and most rows are not padded (only the ones a soft wrap continues).
   **103,734 B → 80,156 B, 75.7μs → 18.2μs.**

   **This one cost something, and the cost is now closed.** A buffer that redraws
   *without* rebuilding used to reuse its width cache and then measured its padded
   rows again each frame, and a dimmed row is measured through `ansi.Strip`, which
   copies it: `GetAllDimmed/dimmed` (20 windows, viewport 30) went 2.4μs → 5.8μs
   and 13,104 B → 15,824 B, and `/normal` 1.7μs → 2.3μs at unchanged memory. The
   trade was a per-frame cost bounded by the viewport against a per-rebuild cost
   bounded by the message, which is the direction the bound below argues for — but
   it was a regression on a named benchmark, and it is gone rather than merely
   recorded. Summing the row's clusters with `walkCells` avoids the copy and is
   *not* how: that measured 6.8μs against 5.8μs, because `breakerModel`'s
   escape-aware iterator is slower than the table's ASCII run. What works is not
   measuring at all. `hardwrapCells` charges cells per cluster in order to find the
   breaks, so what a row needs after it is known at the moment the row is made and
   expensive at every point after; it now rides along in `visualLine.Pad`, an int32
   that fits in the padding `Cont`'s bool already leaves in the struct, and
   `renderVirtual` writes that many spaces instead of measuring the row to work
   them out. **`GetAllDimmed/dimmed` is back at 2.4μs and 13,104 B — the same bytes
   it was before the trade, and the same it still is — and `/normal` at 1.8μs.** The
   counting was not free where it happened: measured at the time, `FullWrappingPath`
   was +5% (5.9μs → 6.2μs) and +2% memory, and `WindowBufferResize` held its time
   for 2.5 KB more, which still left it 25 KB under what it cost before this item.
   Item 6 has since taken both well below where they started — `FullWrappingPath`
   is 2.1μs, 2,576 B and 44 allocs against the 6.2μs and 328 the counting left it
   at, and `WindowBufferResize` 73μs and 81.6 KB against 152μs and 170 KB — so what
   this item paid is no longer on the books. Measuring the two against each other
   needed interleaving: run in separate batches, `WrapContent` and
   `FoldedToolStreamingDelta` each appeared to move by 20% and neither does when the
   binaries alternate rounds.

`WindowBufferResize` (50 windows re-wrapped, 80↔120 cols) is **150μs** against
164μs, which is items 2 and 3 together: a resize is the one operation that
rebuilds every window at once.

**What is left.** The expanded frame is now O(delta + viewport). The folded one is
O(budget) in time for content that can be priced as it arrives and O(content) for
content that cannot, and O(content) in bytes either way: at 128 KB the frame is
12.5μs and 134 KB, and a profile puts 97% of the bytes — and, with the pricing
pass gone, most of the time — in `rawContent`'s join.

The pricing pass is gone, by the third of the three changes itemized here when it
was still outstanding, which subsumed the other two. A text renderer keeps the
cells its content draws and the line breaks in it, summed per delta, so "does this
fit" and which route the cuts take are both answered from a count instead of a
walk. An early-exit walk would have made the pass shorter; this removes it, and
`prepareContent`'s two scans go with it, since a byte that would give either of
them work is a byte that retires the count.

The counts are kept only while they can be kept *exactly*, and that is a byte rule
rather than a judgement: every byte on the width table's byte-wise route, and none
that `prepareContent` would rewrite. There one byte is one cluster, so a delta's
cells add to the total exactly and no cluster can straddle two deltas — which is
the whole reason a per-delta sum is sound here and would not be over arbitrary
text. A combining mark arriving in the delta after its base, a tab, a carriage
return, an escape or any byte over 0x7E retires the counts for good, and the
message is measured again exactly as before. Content only grows, so nothing
un-retires them; and they are trusted only when the bytes they cover are the length
of the content being summarized, which is what keeps a renderer built with its
content already in it — a fixture, or a restore path added later — from being priced
as though it had accounted for bytes it never saw.

The join is the remaining half and is deliberately not attempted: see
[What is left, deliberately](#the-fold-summary-materialized-every-cluster-found-and-fixed)
for the two reasons a chunked summary is not a local change.

The bound is worth stating plainly, because it is the part that does not show up
in any single measurement: **assistant text and reasoning content are not
capped.** `docs/truncation.md` bounds *tool output* (64 KB in memory, then a
scratch file); nothing bounds an AT or AR window. A frame that is O(content)
therefore grows for as long as the model streams, and the total work of one long
answer is quadratic in its length. At 128 KB the expanded frame is 18μs and
would stay there at 4 MB, because it is O(delta + viewport); the folded one is
12.5μs and would be ~0.4ms, because the join it still does is O(content) even
though the summary's pricing is not. Both are inside a 250ms tick, and the
argument for closing the gap is about growth rather than about any figure in this
file. Full rebuilds are the other O(content) shape — a resize, a theme switch or
a fold toggle re-wraps every window — and `WindowBufferResize` is the benchmark
that prices one.

### GetWindowLineRange

Minimum of 20 runs at `-benchtime 1s`; both are allocation-free.

| Scenario | Benchmark | Time |
|----------|-----------|------|
| Single lookup (windowIndex=50, 100 windows) | `WindowBufferGetWindowLineRange` | **25.8ns** |
| Three lookups (indices 50, 25, 75) | `GetWindowLineRangeCached` | **72.9ns total** |

### ScrollView Component

ScrollView holds the **pre-clipped visible region** (produced by
`renderVirtual`) plus the document total line count for clamping — it no
longer re-splits or slices content. The struct is 40 bytes.

| Metric | Benchmark | Value |
|--------|-----------|-------|
| `WithContent` (n=10 to n=10000) | `ScrollViewWithContent` | **0.09ns**, 0 allocs (stores the pre-clipped string) |
| `View()` (n=10 to n=10000) | `ScrollViewView` | **125ns**, split + padding (326 B, 4 allocs) |
| `ScrollDown(1)` | `ScrollViewScroll` | **6.9ns**, 0 allocs |

All four `View()` sizes report the same 326 B and 4 allocs, which is the
property the design is after: the cost does not depend on the document.

### Soft-Wrap Fragment Rendering

`renderVirtual` performs **exact viewport clipping**:

- only the windows overlapping `[yOffset, yOffset+height)` are rendered
  (typically 1–3 windows);
- each window's visual lines are joined **without `\n`** and padded to the
  full width (except the last row), so the terminal soft-wraps at the
  simulated breakpoints — copy restores the original text;
- a row's display width is measured where the frame pads it, and only there:
  padding is the one use a frame has for a width, and most rows are not padded
  (only the ones a soft wrap continues). This used to be a per-window cache of
  every row's width, refilled on the first fragment after a rebuild — which
  during streaming is every frame, so a 128 KB message had ~2,600 rows measured
  to draw 40 of them. The trade the change makes, and its price on a small
  buffer that redraws without rebuilding, are both measured in
  [What is left in the frame path](#what-is-left-in-the-frame-path);
- the window's own line (marker, label, timestamp) is built once, at render
  time, and only its row is swapped in the cursor's register — no style
  render per window per view;
- the sticky window line (docs/tui.md → *Sticky Window Line*) is composited in
  this same pass: the decision reuses the `winStart` the loop already computes
  and the body row it displaces is one row less to assemble. The pinned row is
  the one thing on screen that depends on where the viewport is — it carries
  the count of the window's hidden lines above — so it is not `lines[0]`: it is
  memoized on that count in the window's render cache, which makes a frame that
  does not move the viewport a read (`pinned` 2122ns against `unpinned` 2037ns,
  46 allocations against 48 — the same frame within noise, and
  `TestStickyPinAddsNoAllocations` holds the allocation side) and a frame that
  moves it by a row one row rebuild (3376ns, 85 allocations). The rebuild is
  the window line's build, not the body's:
  `TestStickyPinCostIsIndependentOfTheWindowSize` holds a 400-row message to a
  40-row one's allocations. Geometry (`lineHeights`, `totalLines`) is not
  involved, which is the point: a viewport-dependent line height would
  invalidate those caches on every scroll step.

Measured, minimum of 10 runs at `-benchtime 1s`. The 100-window conversation is
at viewport 30; the folded session is the 120-window one at viewport 40.

| Benchmark | Value |
|-----------|------:|
| `WindowBufferGetAll` | **1.71μs** |
| `WindowBufferDeltaWithGetAll` | **1.89μs** |
| `VirtualRenderingCursorMovement` | **39.3μs** |
| `VirtualRenderingScroll` | **64.0μs** |
| `StreamingUpdateWithVirtualRendering` | **1.61μs** |
| `FoldedSessionGetAll` | **6.75μs** |

The folded row was dropped from this table on 2026-09-28 as unmeasurable and is
restored: the benchmark exists (see
[Collapsed-Window Design](#collapsed-window-design-single-line-fold-headers)).
It is the most expensive of the six because it is the largest frame — 40 rows at
120 columns where the others are 30 rows at 80 — and *not* because folding is
costly: nothing in that benchmark invalidates a window, so every summary is read
out of the window's render cache. What a summary costs to rebuild is
[The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed).

The render path (full wrap, resize, theme switch) is unchanged: display
widths are computed **lazily** — only when fragment output needs padding —
so `ensureLineHeights`/`Render` never pay the per-line measurement cost.

### wrapContent

`wrapContent` is the only wrapping path: the word-boundary wrapper this used to
be compared against was deleted with the `Style` block width that reached it
(see `style.go`), and it was the last line break not measured with `width.go`'s
table. `BenchmarkWrapContent` measures it — 1,760 bytes of code-like content
whose lines all already fit, wrapping to 101 rows at 60 columns:

| | Time | Memory | Allocs |
|---|---:|---:|---:|
| before the two findings below | 21.6μs | 9,590 B | 1,772 |
| today | **1.3μs** | **0 B** | **0** |

Minimum of 10 runs at `-benchtime 1s`, both rows on the reference machine. This
input now costs nothing at all, and that is the correct answer rather than a
lucky one: every line already fits, so `hardwrapCells` returns the string it was
given (`linesFit` early-out), and the content carries no escape, so the style
pass has nothing to re-apply and returns that same string
(`canRestyleNothing`). Two functions each handing back their input is what 0 B
and 0 allocs mean.

The A/B against the wrapper it replaced is below, and it needed correcting on
two counts. The rows were labelled `ansi.Hardwrap (before)` against
`hardwrapCells (after)`, but the numbers in them were `wrapContent`'s — the
whole call, breaker plus the `WrapWriter` style pass that follows it. Measured
separately, `ansi.Hardwrap` alone on this input is 7,616 B and 8 allocs and
`hardwrapCells` alone is **0 B and 0 allocs**, so neither row was the function
it named. And the time claim (1.4x faster) does not reproduce: both sides were
re-measured here, back to back, on one machine.

`wrapContentOld` is the pre-change function reconstructed verbatim
(`ansi.Hardwrap(s, width, true)` then the same `WrapWriter` pass); median of 15
runs at `-benchtime 5000x`, same process, same input.

| | Time | Memory | Allocs |
|---|---:|---:|---:|
| before: `ansi.Hardwrap` + WrapWriter | 30.2μs | 17,244 B | 1,780 |
| after: `hardwrapCells` + WrapWriter (the `wrapContent` of that revision) | 24.4μs | **9,577 B** | 1,772 |
| — the break step alone, before | 6.7μs | 7,616 B | 8 |
| — the break step alone, after | **2.0μs** | **0 B** | **0** |

So the break got **3.3x faster and stopped allocating**, and the call as a whole
moved ~1.2x in time and ~1.8x in memory, because step 2 — the `WrapWriter` style
pass — dominated it. The time ratio is the soft number here — repeated batches of
the same A/B put it between 1.1x and 1.5x — while the memory and allocation
columns are exact and repeat. That is the load-bearing part, and the reason is
visible in the last two rows: this input's lines all already fit, so
`hardwrapCells` returns the string it was given (`linesFit` early-out) while
`ansi.Hardwrap` rebuilt it.

Step 2 dominating the call is what made it worth counting its allocations, and
that counting is the two findings below. It no longer dominates: on content with
no escape in it, step 2 does not run at all.

**What the 1,772 allocations were.** Both were found by counting allocations per
LINE of a wrapped message rather than per call, which is the granularity the
callers that dominate a re-wrap pay at: `wrapVisualLines` calls `wrapRows` once
per original line, and `wrapRows` calls `restyleBreaks` on what the break
produced.

- *the style pass built a writer per line.* `restyleBreaks` constructed a
  `WrapWriter` — the writer, its two handler closures, a buffer, and a parser from
  the pool — on every call. `WrapWriter`'s doc said the parser "is not allocated
  per line as the audit suggested", which was true of `wrapContent` and `Wrap`,
  which call it once per invocation, and false of this path. The comment now says
  what the code does.
- *the writer allocated a slice per byte.* `WrapWriter.Write` handed the underlying
  writer `[]byte{b}`, one byte at a time, because the parser has to be advanced
  through each byte in turn. That is one allocation per byte of styled content: 31
  for a 31-character line, which with the construction above is why a plain
  single-row line cost **38** allocations in total. It writes from a one-byte field
  on the writer now, which is safe because an `io.Writer` must not retain what it
  is given.

A plain single-row line so went from 38 allocations to 0, and `wrapVisualLines` on
a 200-line message from **7,815 to 211**. The 200 that remain are one per line —
the rows slice `wrapRows` splits the wrapped line into. Two hundred more used to
sit beside them, a `[]int` of widths per line, and are now one scratch outside the
loop; that loop's comment already claimed the scratch, while the code declared it
inside the loop and passed `nil`. Counting per line is what turned up both, and
`TestWrapVisualLinesKeepsOneWidthScratch` is what keeps them found. It asserts the
slope rather than a count, because counts move and slopes do not: doubling the
lines must not add anything like one allocation per line.

Skipping the style pass needs a condition, and the obvious one is wrong. Every
byte the writer adds beyond the ones it is given is guarded by the pen or the
hyperlink being set, and only a CSI `m` or an OSC 8 sets either, so the question
is whether the string carries an introducer. `hasEscape` answers it for a 7-bit
ESC and for a C1 control in its two-byte UTF-8 form, `C2 80..9F`. The parser is
byte-oriented, though, and also reads a LONE byte in `0x80..0x9F` as a C1
introducer — which `hasEscape` deliberately does not, because in valid UTF-8 such
a byte is a continuation, and reading it as an introducer would put most CJK text
on the escape route (文 ends in `0x87`). The condition is therefore
`!hasEscape(s) && utf8.ValidString(s)`, and the second half is load-bearing:
`"\x9b31m red\nacross a break"` comes back from the writer restyled, and
`hasEscape` finds nothing in it. Over 4,000 generated strings, of those the writer
changes, 108 are declined by `hasEscape` and **10 only by `utf8.ValidString`** —
which is the count that says the first version of this fast path, written with
`hasEscape` alone, was unsound and would have dropped a restyle.
`TestNothingTheWriterChangesEvadesCanRestyleNothing` asserts the implication in
that direction, and fails if either half stops being needed by anything in the
corpus, so neither can be deleted quietly.

The larger effect is on the paths that wrap short lines, where both early-outs
apply, and it shows up in allocation far more than in time. The two
document-level figures this paragraph has always carried, re-measured on the
reference machine at each revision that moved them:

| | full re-wrap of the 26 KB message | markdown streaming |
|---|---|---|
| before the width table | 607 KB, 27,335 allocs | 18.4 KB, 1,195 allocs |
| at `4cb2b34a`, then with the missing tab guard | 476 KB, then 359 KB | 15.1 KB, then 12.8 KB / 1,072 |
| before the two findings above | 350 KB, ~34,500 allocs | 13.1 KB, 1,097 allocs |
| today | **190 KB, 49 allocs** | **4.2 KB, 128 allocs** |

All at `-benchtime 1s`, minimum of 10 runs for time and the exact columns for
memory; the re-wrap benchmark's content grows with benchtime, so its allocation
column is only comparable within a row. **Neither of the first two reductions is
the width table's**, which is what this paragraph said until 2026-09-28: measured
at `c637636c^` and at `c637636c` on this machine, both figures are unchanged
across that commit (607 KB and 18.4 KB on both sides). They moved later, in two
steps; see [What moved](#what-moved-and-what-moved-it). The width table's own
measurable win is the one in the A/B above.

The last row is the two findings, and the allocation column is the part to read.
The re-wrap benchmark's content is 26,000 bytes in a SINGLE original line, so the
per-byte slice allocation was ~26,000 allocations on its own, and head's count
varied from 33,501 to 36,648 across the 10 runs because it scaled with content
that grows per iteration. Today's is 49 and 50 — flat across all 10 runs, because
nothing in the path is per byte any more. Time moved with it:
`AppendVsFullWrap_LongContent/full-rewrap` 526μs → 253μs, `FullWrap` 63.9μs →
19.8μs, `FullWrappingPath` 6.1μs → 2.1μs, and the expanded side of a folded text
window's frame 18.3μs → 14.4μs at 128 KB. Those are interleaved `benchstat`
comparisons of the two binaries over 8 to 12 rounds, all at p=0.000; the folded
side, which does not wrap a body, is unchanged at every size.

### Resize Performance

Minimum of 10 runs at `-benchtime 1s`.

| Scenario | Value | Memory | Allocs |
|----------|-------|--------|-------:|
| Resize 50 windows (80↔120 cols), before the two findings in [wrapContent](#wrapcontent) | 0.155ms | 170,400 B | 7,100 |
| Resize 50 windows (80↔120 cols), today | **0.072ms** | **81,601 B** | **1,900** |

This row read 0.15ms, 168,545 B and 6,800 allocs before it was re-measured on
the reference machine; the memory and allocation columns move with the tree, the
time column with the machine and its state.

This is the from-scratch re-wrap path: `WithWidth` invalidates every window, so
each pays a full `wrapContent`. Its 50 windows are expanded text windows of 65
bytes each, which is the shape that pays a re-wrap per window — and 65 bytes is
short enough that the per-line cost of the style pass was most of what a window
paid, which is why this row halved. A folded window answers with the summary row
instead, and that row's cost is the subject of
[The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed).

## What moved, and what moved it

The 2026-09-28 interim re-measurement concluded that most of this file's
2026-08-18 figures were "stale before" — wrong before the width-table change
ever landed. On the reference machine that verdict does not survive checking:
**nearly all of them reproduce exactly at the tree they were taken from**, and
what changed is the code, in two commits.

Method: each figure was re-measured at `10c5e88b` (the commit before the
one-row window) with the benchmark source verified identical to today's, then at
`373d006b` — HEAD when this archaeology was done — and the transition was
bisected where it mattered. Memory and allocation columns are machine-
independent, so those comparisons are exact; times carry a machine-and-state
factor and are marked where they drift. The two commits that followed
(`5da8383e` and this one's) moved five of the rows again; that is listed under
the table rather than folded into it, so the attribution below stays about the
history it was done for.

| Row | 2026-08-18 figure | At `10c5e88b` (this machine) | At `373d006b` | What moved it |
|-----|------------------|------------------------------|------|---------------|
| `GetAll` without virtual, memory | 285 KB | 284,608 B ✓ | 113,104 B | `9cc28cb1` |
| `GetAll` with virtual, memory | 10.5 KB | 10,536 B ✓ | 3,776 B | `9cc28cb1` |
| `GetAll` without virtual, allocs | — | 15 ✓ | 14 | `9cc28cb1` |
| `GetAll` with virtual, allocs | — | 59 | 35 | `9cc28cb1` |
| `GetAll` without virtual, time | 50.2μs | 37.6μs | 14.8μs | `9cc28cb1` (2.5x); the residual 1.3x is 2026-08-18's own tree state |
| virtual-vs-not speedup | 16.6x | 14.6x | 10.9x | the baseline got 2.5x cheaper — the clip did not get worse |
| `WindowBufferGetAll` | 2.6μs | 2.56μs ✓ | 1.62μs | `9cc28cb1` |
| `VirtualRenderingScroll` | 113μs | 107μs ✓ | 67.4μs | `9cc28cb1` |
| `VirtualRenderingCursorMovementSingle` | 3.2μs | 3.02μs ✓ | 1.96μs | `9cc28cb1` then `4cb2b34a` (allocs 64 → 51 → 38) |
| `StreamingUpdateWithIncremental`, allocs | 121 | 123 ✓ | 93 | `9cc28cb1`/`4cb2b34a` |
| Full re-wrap, memory | 796 KB | 797,202 B ✓ at `-benchtime 1s` | 476 KB at `300x` (616 KB at `1s`) | `9cc28cb1` (607→569 KB), `4cb2b34a` (570→476 KB), and the rest is benchtime |
| Full re-wrap, allocs | 32,085 | 32,241 ✓ at `1s` | 27,328 at `300x` (32,148 at `1s`) | same |
| Incremental append, memory | 865 B | 977 B at `300x` (940 B at `200x`) | 779 B at `300x` | `4cb2b34a`; the 865 B figure does not reproduce at either tree at any benchtime measured here |
| Markdown streaming, memory | 18.1 KB | 18.4 KB ✓ | 15.1 KB | `4cb2b34a` |
| Markdown streaming, allocs | 1195 | 1195 ✓ | 1139 | `4cb2b34a` |
| `GetWindowLineRange` | 21ns | — | 21.6ns ✓ | nothing — never stale |
| `JustEnsureLineHeights` | 1.1μs | — | 1.03μs ✓ | nothing — never stale |
| `WindowBufferResize` | 0.21ms | — | 0.21ms ✓ | nothing — never stale |
| `ScrollView` (all three rows) | 138ns / 326 B / 4 allocs, ~0.1ns, ~7ns | — | 128ns / 326 B / 4 allocs, 0.09ns, 7.1ns ✓ | nothing — never stale |

Two commits, then, and neither is the one the previous revision named:

- **`9cc28cb1` "a window opens on one line"** — a window's chrome went from a
  bordered box (3 content rows + 2 border rows) to one row that carries its own
  label. Everything that renders *every* window got proportionally cheaper, and
  it lands in this one commit: the unclipped frame 284,608 B → 113,104 B and
  37.6μs → 14.4μs; the clipped one 10,536 B and 59 allocs → 3,776 B and 35;
  `WindowBufferGetAll` 10,472 B and 2.56μs → 6,088 B and ~1.6μs;
  `VirtualRenderingScroll` 413,764 B and 107μs → 243,202 B and 71μs. This is
  also why the headline speedup fell from ~15x to ~11x: virtual rendering's job
  is to avoid rendering windows, and there was simply less per window to avoid.
- **`4cb2b34a` "a frame's rows land where the layout counted them"** — the
  allocation counts fell: full re-wrap 570 KB → 476 KB and 27,335 → 27,328
  allocs, markdown streaming 18.4 KB → 15.1 KB and 1195 → 1139, incremental
  append 977 B → 779 B and 65 → 61, `StreamingUpdateWithIncremental` 96 → 93
  allocs (after `9cc28cb1` had already taken it from 123).

**`c637636c`, the width-table change, moved none of those.** Measured at that
commit and its parent on this machine, the full re-wrap is 607 KB on both sides
and markdown streaming is 18.4 KB on both sides. What it did change is
`wrapContent` itself, and only for content whose lines already fit: 17.2 KB →
9.6 KB per call, and the break step from 8 allocations to none (see
[wrapContent](#wrapcontent)). It was bought for correctness — one table for
measuring and cutting — and it delivered a real allocation win, just not the one
this file credited it with.

It also cost allocations somewhere its own commit message did not look, and this
revision recovers them. The cutters it moved onto the cluster list are the ones
markdown uses per table cell, so the transform paid for the list on every cell
of every row:

| Benchmark | `c637636c^` | `c637636c` | after the cutter rewrite | this tree |
|---|---:|---:|---:|---:|
| `RenderMarkdownTables_Small` | 4,632 B, 101 allocs | 9,432 B, 132 | 4,632 B, 101 | **4,440 B, 90** |
| `RenderMarkdownTables_Large` | 210,282 B, 3,503 | 646,952 B, 5,418 | 210,119 B, 3,503 | **197,941 B, 3,096** |

The last column is below the first, which is not something a fix usually gets to
claim: the summary reorder also gave `expandTabs` the early-out `stripANSI`
already had, and the table transform expands tabs per line and per wrapped row.

That commit measured its cost as "948ns vs 913ns for a 30-cell cut out of a
240-cluster line" — true, and blind to the shape: the cut was cheap in *time*
and expensive in *allocation*, and only on inputs long enough for the list to
matter. The correctness fix stays; the allocation cost is gone.

**And then the previous revision moved five of the rows again**, for a third
reason: the cutters stopped materializing clusters and the summary stopped
escaping the whole message before cutting it (`5da8383e` and `8be4bbfc`). Full
re-wrap 476 KB → 359 KB and 27,328 → 27,308 allocs at `300x`; incremental append
779 B → 648 B and 61 → 58; markdown streaming 15.1 KB → 12.8 KB and 1,139 →
1,072; `WindowBufferResize` 208,516 B → 195,135 B and 7,900 → 6,900 allocs;
`JustEnsureLineHeights` 1.03μs → 788ns. The mechanism is in
[the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed); the
reason a *tab* guard shows up in a re-wrap and a resize is that
`wrapVisualLines` expands tabs per original line, and almost none of them
contain one.

**This revision moved two of those again**, and neither is a fourth mechanism —
they are the byte-wise ASCII route through `cellWidth` and the rows/join split.
Full re-wrap 359 KB → 242 KB and 27,308 → 27,293 allocs at `300x`, because the
re-wrap measures every row it produces; `WindowBufferResize` 195,135 B →
168,545 B and 6,900 → 6,800 allocs, because a full rebuild no longer joins 50
windows' text to count their lines.

**And then the wrap's own allocations moved nearly all of them again**, for a
reason that is neither a width table nor a cutter: `restyleBreaks` built a
`WrapWriter` for every original line of a message, and `WrapWriter.Write`
allocated a one-byte slice for every byte it handed the parser — so a re-wrap
paid once per line and once per byte of content. Full re-wrap 242 KB →
**99,374 B** and 27,293 → **48** allocs at `300x`; incremental append 648 B →
**214 B** and 58 → **5**; markdown streaming 12.8 KB → **4.2 KB** and 1,072 →
**128**; `WindowBufferResize` 168,545 B → **81,601 B** and 6,800 → **1,900**
allocs; `JustEnsureLineHeights` 788ns → **273ns** and 638 B → **212 B**. Bold is
the current tree again, and the two paragraphs above are history. The mechanism,
and the condition that lets the style pass be skipped at all, are in
[wrapContent](#wrapcontent).

The benchtime trap is worth keeping visible, because the previous revision fell
into it and it is easy to fall into again: `AppendVsFullWrap_LongContent`
appends on every iteration, so at `-benchtime 1s` (~1,800 iterations) its full
re-wrap reported 616 KB and 32,148 allocs at the tree this was found in, while
at `300x` it reported 476 KB and 27,328. The 2026-08-18 figures (796 KB, 32,085
allocs) were taken at the default `1s`, and they reproduce to within 0.2% at
`10c5e88b` with that benchtime. So that row was never stale either — it was
compared against a `300x` measurement, which made one benchmark's two benchtimes
look like 2.5x of drift.

Those were the figures then. Today the same pair reads ~181 KB and 50 allocs at
`1s` against 99,374 B and 48 at `300x`, because the allocation that scaled with
the content was one slice per byte and is gone ([wrapContent](#wrapcontent)).
Read that honestly rather than as the trap getting smaller in every sense: the
*absolute* spread fell a long way (140 KB and 4,820 allocs between the two
benchtimes, against 82 KB and 2 now), but the memory *ratio* grew, from 1.29x to
1.82x, because the part of the figure that does not depend on benchtime shrank
much faster than the part that does. The trap is still worth knowing and the
benchtime still belongs beside any figure from that benchmark.

There is a second trap beside it, and this revision fell into both. `StopTimer`
excludes setup from the **timing** and from nothing else. Two consequences:

- `-benchtime 1s` runs iterations until the *timed* work fills a second, so a
  benchmark that rebuilds a 128 KB buffer under `StopTimer` runs ~65,000
  iterations of ~1.3 ms of setup to time 18μs of frame — about 85 seconds of wall
  clock per `-count`. Making the frame faster makes the benchmark *slower to run*.
  Pin `-benchtime Nx` for these; the four tables that do say so.
- A CPU profile taken with `-test.cpuprofile` covers the setup too. Profiling
  `BenchmarkFoldedTextStreamingDelta` that way attributed 4.4% of the frame to
  `deltaHasPipeLine`, which is not in the frame at all — it is the 128 KB
  `AppendFromTLV` in the setup, once per iteration. Every attribution in this
  revision was re-taken against probes that build the renderer once outside the
  loop and call only the part under examination.

What is *not* stale, and was re-verified by reading the code rather than by
timing it: the incremental path is O(delta) and independent of window count,
folded windows are O(1) **for line tracking**, `updateContent` skips unchanged
content, and the streaming cycle is orders of magnitude inside the 250ms tick.
The one structural claim this file carried that the re-measurement overturned is
the folded *row* — see
[The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed).

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 0.01%** of wall time during streaming (1.61μs per 250ms tick ≈ 0.0006%). The one thing that ever threatened this claim was the folded text summary, which spent 5.4% of a tick at 128KB of reasoning text while it materialized every cluster twice per frame and then escaped the whole message to cut 75 cells out of it; it spends 0.005% now ([the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed)). The caveat that remains is about growth rather than this figure: window content is not capped, so a frame costs more the longer the message it draws — see [What is left in the frame path](#what-is-left-in-the-frame-path)
3. **`updateContent()` skips unchanged content** efficiently — the one deliberate exception is the executing-tool spinner refresh (`InvalidateRunningToolSpinners`), which invalidates pending tool windows per tick so the header spinner keeps rotating during silent commands; it costs a 33ns scan and 0 allocations, plus the one window's row in the frame that follows, only while a tool executes (see [tool-spinner-refresh.md](tool-spinner-refresh.md)). That row is a tool window's, so it reads the first input line — the cheap summary, not the O(content) one
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
expensive than the old single-line-per-row transform. Measured at
`-benchtime 200x` on this machine, with the historical columns re-measured in
worktrees of the commits named, so every column is one machine's. The fourth
column is this tree, i.e. after the cutter rewrite, and the third is where the
width-table change (`c637636c`) left it — the cutters it moved onto a
materialized cluster list were the table's per-cell truncation, and the rewrite
gives those allocations back:

| Benchmark (allocs/op) | Before the reflow (`4719fdcc`) | At the reflow (`e691eb90`) | After the width table (`c637636c`) | This tree |
|---|---:|---:|---:|---:|
| `MarkdownStreaming_PlainDeltas` | 1,195 | 1,195 | 1,195 | **128** |
| `MarkdownStreaming_RawMode` | 1,194 | 1,194 | 1,194 | **127** |
| `MarkdownStreaming_TableDeltas` | 17,003 | 52,819 | — | **6,253** |
| `RenderMarkdownTables_Large` | 1,971 | 3,503 | 5,418 | **3,096** |
| `RenderMarkdownTables_Small` | 56 | 101 | 132 | **90** |

The last two rows are where this tree last stood and did not move again; the
first three fell when the wrap stopped allocating per line and per byte
([wrapContent](#wrapcontent)), the streaming rows from 1,072 / 1,071 / 47,980.
`RenderMarkdownTables_*` render a table without wrapping a window's content, so
they are untouched by it — which is a useful control, since it says the fall is
the wrap's and not something that moved under all of markdown.

Read the two `RenderMarkdownTables` rows as a pair: the reflow's real cost was
1,971 → 3,503
allocs on the large transform (~1.8x, because a record can now span several
visual lines), the width table then took it to 5,418 for no functional gain, and
this revision takes it to **3,096** — below where it started, because the
transform expands tabs per table line and per wrapped row and `expandTabs` no
longer rebuilds a string that has no tab in it.

Allocation counts are exact and repeatable — every run of every column reported
the same integers — which is why the table is stated in allocations.

**The timings are not quoted deliberately.** Repeated `-benchtime=200x -count=5`
runs on this machine put them in a ±40% band (`RawMode` 12.4–21.9μs,
`TableDeltas` 0.99–1.16ms, `Large` 0.139–0.161ms). An earlier revision quoted
single-run microseconds and had to be "corrected" twice against pure noise.
Ratios are stable, point estimates are not.

What the allocations prove: markdown-mode streaming of non-table deltas costs
**exactly** what raw mode costs, one alloc above the raw baseline — 128 against
127 on this tree, the same one-off as at `4719fdcc` — so the table path is
provably entered by nothing but table-touching deltas. The table-bearing rows are
up because a record can now span several visual lines and each column's grapheme
widths are measured. Note that the *plain* rows fell (1,195 → 1,072 → 128) in
steps after the reflow rather than at it: the identity held across every change,
and the absolute counts moved under it. That a one-alloc gap survives a fall of
that size is why the table is stated in allocations at all.

**The headline property still holds exactly: plain-text streaming in markdown
mode costs the same as raw mode** — the table path is only entered by deltas
that actually touch a table.

Mind the unit when reading `MarkdownStreaming_TableDeltas`: one iteration is a
whole 22-delta stream (header, delimiter, 20 rows), and every one of those
deltas touches a table, so an iteration performs 22 full re-renders. Its
0.99–1.16ms is therefore the cost of **streaming an entire table**, not of one
re-render:

| | |
|---|---|
| one delta during the stream (the 1.13ms median ÷ 22) | **~51μs** |
| the 250ms tick budget | 0.021% of it |

So a window whose table re-renders on every single tick still spends well under
a tenth of one percent of its budget on layout. This table used to carry a third
row — "one re-render of the finished table, ~44μs (567 allocs)" — measured
separately from the streaming benchmark, and it is worth saying what became of
it, because it turned out to be the most accurate number in the file. No
benchmark in the tree renders that one table on its own, so the row stayed
deleted; but a direct measurement of `renderMarkdownTables` over the same 20
rows at 120 columns gives **567 allocs** on this tree, exactly the figure the
row quoted. In between it was 807, because the width-table change put a
materialized cluster list under the per-cell truncation, and the cutter rewrite
took it back out. Its time here is 24.1μs against the ~44μs that row recorded on
a slower machine. The per-delta figure above is the one that is derivable from a
benchmark in the tree, so it carries the claim.

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

The folded text summary is the cache's thin spot, and it is worth naming
because the table above reads as if every row were covered: the summary lives in
the window-row cache like anything else, so it is rebuilt only when the cache is
invalidated — but during streaming a delta invalidates it every frame, and its
rebuild reads the whole message. That is a pass now rather than a
materialization, so the cache is doing its job and the thing it caches costs
what it costs to derive; the numbers are in
[the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed).

`lineCount` lives in the render cache so `WindowBuffer` can read it with direct field
access (no interface dispatch on the hot path).

### Why `ensureLineHeights` Defers Full Render

During streaming, `ensureLineHeights` first tries `UpdateLineCountFast` → `TryLineCount`.
If the renderer's `wrappedLines` is populated, this returns the line count in 333ns
for the whole pass, without rendering. Otherwise it calls `Window.buildLines`,
which composes the window's own row and wraps the content into visual rows but
does **not** join them — `GetAll` → `renderVirtual` clips those rows to the
viewport, and the joined string only `renderAll` and the cursor's register want
is built behind `joined()`, on first request. A resize is the case that pays: it
rebuilds every window, and joining 50 of them to count their lines was the
dearer half of it.

The deferral is also what keeps a folded window's summary out of line tracking:
a full `lineHeights` rebuild over a 100-window buffer costs 252ns and allocates
nothing (`EnsureLineHeightsFullRebuild`), because a folded window answers `1`
without a renderer call at all and an unfolded one answers from the count its
last render cached. Deriving the summary arrives in the frame instead, and only
for the folded windows the viewport actually shows.
