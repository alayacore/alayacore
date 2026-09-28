# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

Benchmarks run on: Intel(R) Core(TM) Ultra 9 285K, Linux amd64, Go 1.26.1,
against the tree at `373d006b` plus this revision's changes to it. Every figure
in this file was re-measured on that machine on 2026-09-28 — the whole suite,
not a sample — and the method is part of the figure:

```
go test ./internal/adapters/terminal/ -run '^$' -bench '<Name>' -benchmem -benchtime 1s -count 10
```

Quoted value is the **minimum** of those runs, because that is the one number a
benchmark run reliably reproduces; where a minimum is a lone outlier the row
says it quotes the median instead. Four tables pin a fixed `-benchtime Nx`
rather than `1s`, and each says so, because those benchmarks append on every
iteration and their memory columns move with `-benchtime`. Memory columns are
the `B/op` `go test` prints; where prose rounds to KB it divides by 1000, which
is the convention this file's older figures were written in.

What this revision changed in the code, so the figures can be placed: the
cluster cutters in `width.go` and the input chain's cluster helpers became folds
over one streaming walk instead of a materialized list
([the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed)),
`tailCells`' styled branch stopped overrunning its budget, one benchmark was
added (`BenchmarkFoldedTextStreamingDelta`) and two allocation-count tests
(`TestCutsCostTheCutNotTheString`, `TestLineQueriesCostOneEncoding`), and the
stale figures in comments across `window.go`, `window_buffer.go`, `program.go`
and the benchmark files were corrected. Every number below is measured on the
tree that includes those changes; where a figure existed before them and moved,
both values are given.

An interim re-measurement on an AMD Ryzen 7 5800U (2026-09-28, Go 1.26.4) is
superseded by this one and is in the git history of this file rather than in
it. Its ratios carried; its absolute times were the slower machine's, and its
attribution of the drift was wrong, which
[What moved, and what moved it](#what-moved-and-what-moved-it) corrects with a
bisect.

## Summary

Working as designed:

- ✅ **Virtual rendering** — ~11x less work than rendering every window, and
  30x lighter: 100 windows at viewport 30 costs 1.34μs and 3,776 B/op with the
  viewport clip, against 15.2μs and 113,104 B/op without it
  (`GetAllWithVirtual` / `GetAllWithoutVirtual`). The ratio is 11.4x rather
  than the 16.6x this file used to quote because the *baseline* got 2.5x
  cheaper, not because the clip got worse — see
  [What moved](#what-moved-and-what-moved-it)
- ✅ **Incremental content append** — O(delta) per frame via
  `appendDeltaToVisualLines`, avoiding an O(n) full re-wrap: ~456x on a 26KB
  message (325 wrapped rows), 779 B against 477 KB per frame
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines`
  in 1.04μs (a whole `ensureLineHeights` pass with 1 dirty window), no render
- ✅ **Streaming stays under 1ms** — 2.62μs per full cycle (append + line
  tracking + viewport render) against a 250ms tick, so the frame is ~0.001% of
  the budget
- ✅ **Custom ScrollView** — 40 bytes of state holding the pre-clipped visible
  region; `View()` pads to the viewport height in 124ns and 4 allocs,
  `WithContent` is 0.09ns because there is no re-split
- ✅ **Soft-wrap fragment viewport** — `renderVirtual` clips to visual lines
  and emits continuous per-window fragments (`\n` only between windows), with
  display widths cached per render, so a viewport render of 100 windows costs
  1.62μs (`WindowBufferGetAll`) and never touches the windows outside it (see
  [Soft-Wrap Fragment Rendering](#soft-wrap-fragment-rendering))

One thing was not fine, and this revision fixes it rather than recording it:

- 🔧 **A folded text window's summary row used to materialize every grapheme
  cluster of the message, twice, per frame.** Reasoning windows fold by default
  and re-summarize on every delta, so at 128KB that frame cost 13.5ms and
  56.8 MB — 41x the time and 76x the memory the *same content expanded* costs,
  which is backwards for the state whose whole job is to be cheap. The cutters
  are folds over one streaming walk now: **934μs and 780 KB**, and the same
  defect's other face (a keystroke at the end of a long prompt line, ~1ms and
  ~4 MB) is down to ~300μs and ~57 KB. Two allocation-count tests pin the shape.
  See [The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed)

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
TryLineCount → len(wrappedLines) + 1  (1.04μs for the whole ensureLineHeights pass, no render)
```

A dedicated assertion test (`TestIncrementalPathIsUsed`) verifies that
`TryLineCount` returns a valid count after every delta append. If the
incremental path breaks, this test fails immediately.

Markdown mode keeps this property for ordinary text: only table-touching
deltas re-render, and benchmarks show mdMode plain-text streaming within
noise of raw mode (`BenchmarkMarkdownStreaming_PlainDeltas` vs
`BenchmarkMarkdownStreaming_RawMode`; 1139 allocations against 1138).

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
| Average full cycle (append + line tracking + GetAll), `StreamingUpdateWithVirtualRendering` | **2.62μs** | 4,644 B | 97 |
| Incremental append only, `JustAppendUpdate` | **50ns** | 82 B | 0 |
| Small delta streaming (append + line tracking), `StreamingSmallDelta` | **1.09μs** | 805 B | 62 |
| Long content incremental append (26KB message), `AppendVsFullWrap_LongContent/incremental` | **1.28μs** (median, `-benchtime 300x`) | 779 B | 61 |
| Budget | < 1ms (target), 250ms (actual tick) | | |

`JustAppendUpdate` reports 82 B/op and 0 allocs/op because the bytes are the
amortized growth of the content buffer, not an allocation per call.

### Incremental Append vs Full Re-wrap (26KB message)

Measured via `BenchmarkAppendVsFullWrap_LongContent`: one window holding 500
repeats of a 52-character sentence — 26,000 bytes with no newline in it, which
wraps to 325 rows at 80 columns (326 counted lines with the window's own). This
section said "5000-line content" until 2026-09-28; the benchmark's content was
always 325 wrapped rows, and the benchmark's own comment said so too.

Both sides append on every iteration, so `-benchtime` is part of the figure:
**median of 6 runs at `-benchtime 300x`**. (The incremental side's minimum over
those runs, 900ns, sits below the rest of the field at 1237–1380ns, which is why
this table quotes medians.)

| Operation | Time | Memory | Allocs |
|-----------|------|--------|-------:|
| **Incremental append** | **1.28μs** | **779 B** | **61** |
| Full re-wrap | 0.58ms | 476,559 B | 27,328 |
| **Speedup** | **~456x** | **~612x** | **~448x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 0.58ms
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
pair at `300x` reads 17.1 KB and 863 allocs for the side that re-wraps, while
the incremental side does not move (4.5 KB, 93 allocs) because it is O(delta).

| Scenario | Time | Memory | Allocs |
|----------|------|--------|-------:|
| **Incremental (1 dirty window)** | **2.59μs** | 4,491 B | 93 |
| Full re-wrap of the streaming window | 10.0μs | 12,708 B | 608 |

Window count does not change the incremental cost, and the reason is structural
rather than empirical: `ensureLineHeights` touches the one dirty window and
`renderVirtual` only the windows the viewport overlaps, so nothing in the frame
iterates the history. A probe at 50, 100 and 400 history windows reports the
same 4,491 B and 93 allocs per frame, with the times inside run-to-run noise
(2.4–3.6μs). The incremental path is O(delta), independent of history length.

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual`
(100 windows, viewport=30 lines). Minimum of 20 runs at `-benchtime 1s`: the
unclipped side is the noisiest benchmark in the file (its 20 runs span
15.2–19.6μs) because it is the one that renders ~500 rows instead of 30.

| Scenario | Time | Memory | Allocs | Speedup |
|----------|------|--------|-------:|:-------:|
| `GetAll` with virtual rendering (100 windows) | **1.34μs** | 3,776 B | 35 | **11.4x** |
| `GetAll` without virtual rendering (100 windows) | **15.2μs** | 113,104 B | 14 | baseline |

Memory is the sharper claim and it does not depend on the clock: **30x less
allocated per frame**, because the clipped side builds 30 rows and the unclipped
side builds all ~500.

### Line Height Tracking

| Scenario | Benchmark | Time | Memory | Allocs |
|----------|-----------|------|--------|-------:|
| Incremental, 1 dirty window (20 windows) | `JustEnsureLineHeights` | **1.04μs** | 767 B | 59 |
| Incremental, 1 dirty window (100 windows) | `EnsureLineHeightsIncremental` | **1.22μs** | 862 B | 69 |
| `lineHeights` array rebuilt over 100 windows | `EnsureLineHeightsFullRebuild` | **297ns** | 0 B | 0 |
| Every window re-wrapped from scratch (50 windows, 80↔120 cols) | `WindowBufferResize` | **0.19ms** | 208,516 B | 7,900 |

Two rows this table used to carry are gone rather than re-measured:
"~150μs (1 dirty window, uncached)" and "~7.1ms (all 100 windows rendered from
scratch)", both starred as historical estimates. Nothing in the tree measures
either, and the second is not what the benchmark named for it does —
`EnsureLineHeightsFullRebuild` sets `dirtyIndex = dirtyFullRebuild` but never
invalidates the windows, so every `Render` inside it hits the render cache and
the 297ns is the cost of rebuilding the height array from cached counts. A
genuine from-scratch pass is the resize row: that one does invalidate, and pays
a full re-wrap per window.

### Full Update Cycle (Delta + GetAll)

Measured via `BenchmarkWindowBufferDeltaWithGetAll` (100 windows, delta to last
window, viewport 30). Minimum of 10 runs at `-benchtime 1s`.

| Metric | Value | Memory | Allocs |
|--------|-------|--------|-------:|
| Delta + GetTotalLines + GetAll (incremental, 100 windows) | **2.96μs** | 7,099 B | 116 |

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
| Single cursor move (EnsureCursorVisible + updateContent) | **2.01μs** | 6,254 B | 38 |
| 20 cursor moves through the buffer | **39.1μs** | 125,090 B | 760 |
| Scroll 20 steps down + 20 steps up | **65.6μs** | 177,281 B | 1,400 |

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
| `GetAll` viewport render of the whole session | **7.08μs** | 46,465 B | 51 |
| 20 cursor moves (j/k) through it | **146μs** | 929,790 B | 1,040 |
| Delta into a folded **tool** window (Uf preview) | **105ns** | 88 B | 2 |
| Delta into a folded **text** window, 2KB content | **17.5μs** | 12,312 B | 63 |

The three figures this section quoted before 2026-09-28 were deleted on the
grounds that "the folded-session benchmark these figures came from is gone from
the tree". It was not gone: `folded_bench_test.go` has held all three benchmarks
since the collapsed-window redesign, they run in the normal suite, and they
reproduce those figures (7.9μs → 7.08μs, 0.17ms → 146μs, 0.11μs → 105ns). They
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
composed into it, plus the timestamp — 1.32μs per delta via
`BenchmarkWindowBufferDelta`). A folded text window's frame is still the dearer
of the two in **time** — 2.1x at 2KB, 4.7x at 32KB, 3.0x at 128KB — because
summarizing a message means reading it, while an expanded window's rows are
already wrapped and a frame only joins the ones the viewport shows. It is no
longer the dearer one in **memory**, which is where the inversion lived: 12 KB
against the expanded side's 20 KB at 2KB, and within 4% of it at 128KB
(next section).

### The fold summary materialized every cluster (found and fixed)

`BenchmarkFoldedTextStreamingDelta` was added by this revision to price the one
frame shape nothing else in the tree measured: a folded **text** window
streaming. It rebuilds its buffer under `StopTimer`, so one iteration is exactly
one frame at a fixed content size and its memory columns mean the same thing at
any `-benchtime`. Each size runs folded and expanded on identical content; the
expanded side is the control. Minimum of 4 runs at `-benchtime 1s`.

| Content size | Folded, before | Folded, now | Expanded (control, now) | Folded: before → now |
|---|---:|---:|---:|---:|
| 2KB | 99.0μs, 503,259 B, 95 allocs | **17.5μs, 12,312 B, 63** | 8.20μs, 20,179 B, 98 | 5.7x faster, **41x lighter** |
| 32KB | 4.20ms, 13,739,011 B, 127 | **249μs, 222,752 B, 73** | 53.3μs, 220,198 B, 107 | 16.9x faster, **62x lighter** |
| 128KB | 13.5ms, 56,779,754 B, 143 | **934μs, 779,824 B, 77** | 310μs, 752,095 B, 112 | 14.5x faster, **73x lighter** |

Before the fix the two sides were the wrong way round — folding a text window
cost 12x to 81x *more* per frame than opening it, and the allocation count
barely moved (95 → 127 → 143) while the bytes scaled with the message. That
signature is what localized it: not more work per row, one big materialization
per call, twice.

**What was wrong.** A folded text window's row is head + "…" + tail of its
content: `textRenderer.BuildCollapsed` → `collapsedSummary` →
`headAndTailParts` → `takeCells` (head) and `tailCells` (tail). Both cutters
answered from a `clusters(s)` helper that returned *every* grapheme cluster of
the string as a `[]cluster` — one struct per cluster, each holding its own
substring — so keeping ~30 cells from the front and ~45 from the back of a
message built the whole message's cluster list twice. An allocation profile of
the 32KB folded frame put **98.4% of everything it allocated in `clusters`**.
Measured alone, `clusters` over a 2KB string was 44.6μs and 245,408 B: ~120 B of
garbage per input byte, for answers that are O(1).

The same helper was the input chain's width source (`graphemeClusters` wrapped
it and copied the result into a second slice), and `ensureCursorVisible` asks
four or five such questions per keystroke over the whole current line. So the
defect had two user-visible faces: a folded reasoning window re-summarizing
itself every tick, and typing at the end of a long prompt line.

**The fix, and why it is a design fix rather than a tuning one.** `width.go`
already owned a streaming cluster walk — `walkCells`, which `hardwrapCells`,
`keepCells` and `dropCells` were built on. The cutters were the one family not
using it, and they were also the one path reading a *second* set of table
options (`widthModel` instead of the walker's `breakerModel`), which is exactly
the divergence this file's header exists to prevent. So:

- `walkCells`' callback now returns `bool`, and `false` stops the walk. A
  prefix question ends at its budget instead of at the end of the string.
- `takeCells` is a fold that stops at the budget; `tailCells` is one forward
  pass that stops at the first boundary whose remainder fits. Each returns its
  cut — a prefix `s[:end]`, a suffix `s[start:]` — as a **copy**, not as the
  substring itself: the folded summary is cached on the window, and a 30-cell
  prefix must not hold a 128 KB message alive behind it.
- `widestCellCluster` is a max fold — zero allocations, was one list.
- `clusters` is gone from production. It survives in `width_test.go` as the
  *oracle* the cutters are checked against, which is a better place for it: an
  oracle that shares an implementation with the code under test cannot disagree
  with it.
- `input_field.go` got the streaming form it needed — `walkLineClusters`, one
  encoding of the line per walk, no list — and `runesWidth`, `clusterStartAt`,
  `firstRuneStartAtLeast`, `runeIndexAtWidth`, the cursor's cluster width and
  `buildVisibleText` are folds over it. `truncatePlaceholder` turned out to be a
  second implementation of `takeCells` and is now that call.

The cost of a question now follows the answer, not the string:

| | per keystroke at the end of a 4000-cell line | one width sum over 10,000 runes |
|---|---|---|
| before | ~1 ms and ~4 MB (`InputFieldInsertLongLine`: 19.6ms, 81.8 MB for a build + 20 keys) | 523μs, 2.1 MB, 21 allocs |
| now | ~300μs and ~57 KB (same benchmark: **5.99ms, 1.14 MB, 131 allocs**) | **113μs, 20.5 KB, 1 alloc** |

`InputFieldMoveLongLine` (a build plus 200 arrow moves) went 1.27ms → **484μs**
and 5.2 MB → **156 KB**; `InputFieldViewLongLine` (one `View()` of a 2000-cell
line) went 81μs → **24.4μs** and 306 KB → **5,064 B**. Movement was always
cheaper than insertion because `handleMovement` does not call
`ensureCursorVisible`; that asymmetry is unchanged and is now the only thing
separating the two.

The cutters were not the only caller paying for the list. Markdown cell
truncation calls `takeCells` per cell, so the table transform was carrying it
too: `RenderMarkdownTables_Large` measured 646,897 B and 5,418 allocs before
this fix and **210,119 B and 3,503** after — which is exactly what it measured
before the width-table change introduced the materializing cutters (see
[What moved](#what-moved-and-what-moved-it)). The allocation regression that
change was recorded as costing is recovered, and its correctness fix stays.

**What pins it.** Two allocation-count tests, one per half:
`TestCutsCostTheCutNotTheString` (width_test.go) runs both cutters and both
folds over 100-cluster and 10,000-cluster inputs at the same budget and fails
above a small ceiling — an implementation that materializes reports one
allocation per cluster, ~10,000 against a ceiling of 8 — and
`TestLineQueriesCostOneEncoding` (cluster_structural_test.go) does the same for
the four input-chain queries over 100-rune and 10,000-rune lines. The ceiling is
not an exact count because `AllocsPerRun` reads process-wide mallocs and a
`-race` build can add one of its own. The behavioural invariants are unchanged
and still run: budget (`TestCutNeverOverrunsItsBudget`, now over the whole
breaker corpus), maximality and cluster alignment against the oracle, the
styled-cut guarantees, and 20,000 random lines of the structural test.

One real bug came out of widening the budget test to that corpus, and it was
older than this revision: `tailCells` routed styled strings through `dropCells`,
whose contract is to *keep* the cluster that straddles the cut (it and
`keepCells` partition a string, so one of them has to). A tail cut therefore
inherited the opposite rule from a head cut — `tailCells` on a styled row of
2-cell clusters asked for 3 cells and returned 4. The styled branch now walks
with the same rule the plain branch uses, dropping the straddler and keeping the
escapes on both sides of the cut. `takeCells` was already correct, which is why
nothing had tripped over it: the head and the tail of the same summary
disagreed about the budget, and only the tail could push a row wide.

**What is left, deliberately.** A folded frame is still O(content) in time and
in copies, because summarizing a message means reading it: `rawContent` joins
the delta parts, `prepareContent` strips and expands tabs, `headAndTailParts`
escapes newlines over the whole content, and `tailCells` walks to the end to
find where the tail begins. That is ~6 passes and ~780 KB at 128 KB of content —
934μs, 0.4% of a 250ms tick, against the 5.4% and 56.8 MB it was. Cutting it
further means deriving the head and tail from slices of the raw content instead
of from an escaped copy of all of it, which is a smaller, separate change; the
frame budget does not ask for it yet. What the fix removed is the part that
scaled at ~430 B per byte of message, and the tests above are what keep it
removed.

### GetWindowLineRange

Minimum of 20 runs at `-benchtime 1s`; both are allocation-free.

| Scenario | Benchmark | Time |
|----------|-----------|------|
| Single lookup (windowIndex=50, 100 windows) | `WindowBufferGetWindowLineRange` | **21.1ns** |
| Three lookups (indices 50, 25, 75) | `GetWindowLineRangeCached` | **54.6ns total** |

### ScrollView Component

ScrollView holds the **pre-clipped visible region** (produced by
`renderVirtual`) plus the document total line count for clamping — it no
longer re-splits or slices content. The struct is 40 bytes.

| Metric | Benchmark | Value |
|--------|-----------|-------|
| `WithContent` (n=10 to n=10000) | `ScrollViewWithContent` | **0.09ns**, 0 allocs (stores the pre-clipped string) |
| `View()` (n=10 to n=10000) | `ScrollViewView` | **124ns**, split + padding (326 B, 4 allocs) |
| `ScrollDown(1)` | `ScrollViewScroll` | **7.0ns**, 0 allocs |

All four `View()` sizes report the same 326 B and 4 allocs, which is the
property the design is after: the cost does not depend on the document.

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
  does not move the viewport a read (`pinned` 2084ns against `unpinned` 2003ns,
  46 allocations against 48 — the same frame within noise, and
  `TestStickyPinAddsNoAllocations` holds the allocation side) and a frame that
  moves it by a row one row rebuild (3317ns, 85 allocations). The rebuild is
  the window line's build, not the body's:
  `TestStickyPinCostIsIndependentOfTheWindowSize` holds a 400-row message to a
  40-row one's allocations. Geometry (`lineHeights`, `totalLines`) is not
  involved, which is the point: a viewport-dependent line height would
  invalidate those caches on every scroll step.

Measured, minimum of 10 runs at `-benchtime 1s`. The 100-window conversation is
at viewport 30; the folded session is the 120-window one at viewport 40.

| Benchmark | Value |
|-----------|------:|
| `WindowBufferGetAll` | **1.62μs** |
| `WindowBufferDeltaWithGetAll` | **2.96μs** |
| `VirtualRenderingCursorMovement` | **39.1μs** |
| `VirtualRenderingScroll` | **65.6μs** |
| `StreamingUpdateWithVirtualRendering` | **2.62μs** |
| `FoldedSessionGetAll` | **7.08μs** |

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
**20.2μs, 9,597 B, 1,772 allocs** (minimum of 10 runs at `-benchtime 1s`).

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
| after: `hardwrapCells` + WrapWriter (today's `wrapContent`) | 24.4μs | **9,577 B** | 1,772 |
| — the break step alone, before | 6.7μs | 7,616 B | 8 |
| — the break step alone, after | **2.0μs** | **0 B** | **0** |

So the break got **3.3x faster and stopped allocating**, and the call as a whole
moved ~1.2x in time and ~1.8x in memory, because step 2 (the `WrapWriter` style
pass) dominates it. The time ratio is the soft number here — repeated batches of
the same A/B put it between 1.1x and 1.5x — while the memory and allocation
columns are exact and repeat. That is the load-bearing part, and the reason is
visible in the last two rows: this input's lines all already fit, so
`hardwrapCells` returns the string it was given (`linesFit` early-out) while
`ansi.Hardwrap` rebuilt it.

The larger effect is on the paths that wrap short lines, where that early-out
applies, and it shows up in allocation rather than in time. Two document-level
figures sit near it and are worth stating with their attribution fixed: the full
re-wrap of the 26KB message allocates 477 KB and 27,328 per operation today
against 607 KB and 27,335 before the width table, and markdown streaming
15.1 KB and 1,139 against 18.4 KB and 1,195. **Neither reduction is the width
table's**, which is what this paragraph said until 2026-09-28: measured at
`c637636c^` and at `c637636c` on this machine, both figures are unchanged across
that commit (607 KB and 18.4 KB on both sides). They moved later, at `4cb2b34a`
— see [What moved](#what-moved-and-what-moved-it). The width table's own
measurable win is the one in the table above.

### Resize Performance

Minimum of 10 runs at `-benchtime 1s`.

| Scenario | Value | Memory | Allocs |
|----------|-------|--------|-------:|
| Resize 50 windows (80↔120 cols) | **0.19ms** | 208,516 B | 7,900 |

This is the from-scratch re-wrap path: `WithWidth` invalidates every window, so
each pays a full `wrapContent`. Its 50 windows are expanded text windows of 65
bytes each, which is the shape that pays a re-wrap per window; a folded window
answers with the summary row instead, and that row's cost is the subject of
[The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed).

## What moved, and what moved it

The 2026-09-28 interim re-measurement concluded that most of this file's
2026-08-18 figures were "stale before" — wrong before the width-table change
ever landed. On the reference machine that verdict does not survive checking:
**nearly all of them reproduce exactly at the tree they were taken from**, and
what changed is the code, in two commits.

Method: each figure was re-measured at `10c5e88b` (the commit before the
one-row window) with the benchmark source verified identical to today's, then at
HEAD, and the transition was bisected where it mattered. Memory and allocation
columns are machine-independent, so those comparisons are exact; times carry a
machine-and-state factor and are marked where they drift.

| Row | 2026-08-18 figure | At `10c5e88b` (this machine) | HEAD | What moved it |
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

| Benchmark | `c637636c^` | `c637636c` | This tree |
|---|---:|---:|---:|
| `RenderMarkdownTables_Small` | 4,632 B, 101 allocs | 9,432 B, 132 | **4,632 B, 101** |
| `RenderMarkdownTables_Large` | 210,282 B, 3,503 | 646,952 B, 5,418 | **210,119 B, 3,503** |

That commit measured its cost as "948ns vs 913ns for a 30-cell cut out of a
240-cluster line" — true, and blind to the shape: the cut was cheap in *time*
and expensive in *allocation*, and only on inputs long enough for the list to
matter. The correctness fix stays; the allocation cost is gone.

The benchtime trap is worth keeping visible, because the previous revision fell
into it and it is easy to fall into again: `AppendVsFullWrap_LongContent`
appends on every iteration, so at `-benchtime 1s` (~1,800 iterations) its full
re-wrap reports 616 KB and 32,148 allocs while at `300x` it reports 476 KB and
27,328. The 2026-08-18 figures (796 KB, 32,085 allocs) were taken at the default
`1s`, and they reproduce to within 0.2% at `10c5e88b` with that benchtime. So
that row was never stale either — it was compared against a `300x` measurement,
which made one benchmark's two benchtimes look like 2.5x of drift.

What is *not* stale, and was re-verified by reading the code rather than by
timing it: the incremental path is O(delta) and independent of window count,
folded windows are O(1) **for line tracking**, `updateContent` skips unchanged
content, and the streaming cycle is orders of magnitude inside the 250ms tick.
The one structural claim this file carried that the re-measurement overturned is
the folded *row* — see
[The fold summary materialized every cluster](#the-fold-summary-materialized-every-cluster-found-and-fixed).

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 0.01%** of wall time during streaming (2.62μs per 250ms tick ≈ 0.001%). The one thing that ever threatened this claim was the folded text summary, which spent 5.4% of a tick at 128KB of reasoning text while it materialized every cluster twice per frame; it is a fold now and spends 0.4% ([the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed))
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
| `MarkdownStreaming_PlainDeltas` | 1,195 | 1,195 | 1,195 | **1,139** |
| `MarkdownStreaming_RawMode` | 1,194 | 1,194 | 1,194 | **1,138** |
| `MarkdownStreaming_TableDeltas` | 17,003 | 52,819 | — | **51,115** |
| `RenderMarkdownTables_Large` | 1,971 | 3,503 | 5,418 | **3,503** |
| `RenderMarkdownTables_Small` | 56 | 101 | 132 | **101** |

Read the last two rows as a pair: the reflow's real cost was 1,971 → 3,503
allocs on the large transform (~1.8x, because a record can now span several
visual lines), the width table then took it to 5,418 for no functional gain,
and the cutter rewrite puts it back at 3,503 — the same integer, byte for byte
(210,119 B against 210,282 B at `e691eb90`). The `PlainDeltas` fall from 1,195
to 1,139 is `4cb2b34a`'s, not this one's.

Allocation counts are exact and repeatable — every run of every column reported
the same integers — which is why the table is stated in allocations.

**The timings are not quoted deliberately.** Repeated `-benchtime=200x -count=5`
runs on this machine put them in a ±25% band (`RawMode` 18.4–24.5μs,
`TableDeltas` 1.02–1.30ms, `Large` 0.152–0.180ms). An earlier revision quoted
single-run microseconds and had to be "corrected" twice against pure noise.
Ratios are stable, point estimates are not.

What the allocations prove: markdown-mode streaming of non-table deltas costs
**exactly** what raw mode costs, one alloc above the raw baseline — 1,139 against
1,138 on this tree, the same one-off as at `4719fdcc` — so the table path is
provably entered by nothing but table-touching deltas. The table-bearing rows are
up because a record can now span several visual lines and each column's grapheme
widths are measured. Note that the *plain* rows fell (1,195 → 1,139) at
`4cb2b34a`, not at the reflow: the identity held across the change, and the
absolute counts moved under it.

**The headline property still holds exactly: plain-text streaming in markdown
mode costs the same as raw mode** — the table path is only entered by deltas
that actually touch a table.

Mind the unit when reading `MarkdownStreaming_TableDeltas`: one iteration is a
whole 22-delta stream (header, delimiter, 20 rows), and every one of those
deltas touches a table, so an iteration performs 22 full re-renders. Its
1.02–1.30ms is therefore the cost of **streaming an entire table**, not of one
re-render:

| | |
|---|---|
| one delta during the stream (the 1.27ms median ÷ 22) | **~58μs** |
| the 250ms tick budget | 0.023% of it |

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
took it back out (24.1μs here against the ~44μs that row recorded on a slower
machine). The per-delta figure above is the one that is derivable from a
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
If the renderer's `wrappedLines` is populated, this returns the line count in 1.03μs
for the whole pass, without rendering. The actual `w.Render()` — which joins wrapped
lines, composes the window's own row, and renders the style layer — is deferred to
`GetAll` → `renderVirtual`, which needs the rendered output for the viewport anyway.

The deferral is also what keeps a folded window's summary out of line tracking:
a full `lineHeights` rebuild over a 100-window buffer costs 297ns and allocates
nothing (`EnsureLineHeightsFullRebuild`), because a folded window answers `1`
without a renderer call at all and an unfolded one answers from the count its
last render cached. Deriving the summary arrives in the frame instead, and only
for the folded windows the viewport actually shows.
