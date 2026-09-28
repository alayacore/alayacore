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
  30x lighter: 100 windows at viewport 30 costs 1.37μs and 3,776 B/op with the
  viewport clip, against 14.6μs and 113,104 B/op without it
  (`GetAllWithVirtual` / `GetAllWithoutVirtual`). The ratio is 10.7x rather
  than the 16.6x this file used to quote because the *baseline* got 2.5x
  cheaper, not because the clip got worse — see
  [What moved](#what-moved-and-what-moved-it)
- ✅ **Incremental content append** — O(delta) per frame via
  `appendDeltaToVisualLines`, avoiding an O(n) full re-wrap: ~521x on a 26KB
  message (325 wrapped rows), 648 B against 359 KB per frame
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines`
  in 788ns (a whole `ensureLineHeights` pass with 1 dirty window), no render
- ✅ **Streaming stays under 1ms** — 2.45μs per full cycle (append + line
  tracking + viewport render) against a 250ms tick, so the frame is ~0.001% of
  the budget
- ✅ **Custom ScrollView** — 40 bytes of state holding the pre-clipped visible
  region; `View()` pads to the viewport height in 124ns and 4 allocs,
  `WithContent` is 0.09ns because there is no re-split
- ✅ **Soft-wrap fragment viewport** — `renderVirtual` clips to visual lines
  and emits continuous per-window fragments (`\n` only between windows), with
  display widths cached per render, so a viewport render of 100 windows costs
  1.60μs (`WindowBufferGetAll`) and never touches the windows outside it (see
  [Soft-Wrap Fragment Rendering](#soft-wrap-fragment-rendering))

One thing was not fine, and this revision fixes it rather than recording it:

- 🔧 **A folded text window's summary row used to materialize every grapheme
  cluster of the message, twice, per frame.** Reasoning windows fold by default
  and re-summarize on every delta, so at 128KB that frame cost 13.5ms and
  56.8 MB — 41x the time and 76x the memory the *same content expanded* costs,
  which is backwards for the state whose whole job is to be cheap. Two
  materializations were removed, not one: the cutters' cluster list, and the
  whole-message escape copy they cut out of. The frame is now **770μs and
  134 KB** — lighter than the expanded side at every size — and the same
  defect's other face (a keystroke at the end of a long prompt line, ~1ms and
  ~4 MB) is down to ~300μs and ~57 KB. A third tail cut, the one the streaming
  tool previews use, turned out to split grapheme clusters; it does not now.
  Five tests pin all of it.
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
TryLineCount → len(wrappedLines) + 1  (788ns for the whole ensureLineHeights pass, no render)
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
| Average full cycle (append + line tracking + GetAll), `StreamingUpdateWithVirtualRendering` | **2.45μs** | 4,506 B | 93 |
| Incremental append only, `JustAppendUpdate` | **46ns** | 81 B | 0 |
| Small delta streaming (append + line tracking), `StreamingSmallDelta` | **838ns** | 665 B | 58 |
| Long content incremental append (26KB message), `AppendVsFullWrap_LongContent/incremental` | **1.10μs** (median, `-benchtime 300x`) | 648 B | 58 |
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
(the six land between 1063ns and 1172ns).

| Operation | Time | Memory | Allocs |
|-----------|------|--------|-------:|
| **Incremental append** | **1.10μs** | **648 B** | **58** |
| Full re-wrap | 0.57ms | 359,240 B | 27,308 |
| **Speedup** | **~521x** | **~554x** | **~471x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 0.57ms
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
pair at `300x` reads 14.9 KB and 855 allocs for the side that re-wraps, while
the incremental side does not move (4.4 KB, 89 allocs) because it is O(delta).

| Scenario | Time | Memory | Allocs |
|----------|------|--------|-------:|
| **Incremental (1 dirty window)** | **2.38μs** | 4,368 B | 89 |
| Full re-wrap of the streaming window | 13.0μs | 11,240 B | 601 |

Window count does not change the incremental cost, and the reason is structural
rather than empirical: `ensureLineHeights` touches the one dirty window and
`renderVirtual` only the windows the viewport overlaps, so nothing in the frame
iterates the history. A probe at 50, 100 and 400 history windows reports the
same 4,491 B and 93 allocs per frame, with the times inside run-to-run noise
(2.4–3.6μs). The incremental path is O(delta), independent of history length.

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual`
(100 windows, viewport=30 lines). Minimum of 20 runs at `-benchtime 1s`: the
unclipped side is the noisiest benchmark in the file — the 20 runs quoted here
span 14.6–15.7μs, and its minimum has landed anywhere from 14.6μs to 19.5μs
across batches — because it is the one that renders ~500 rows instead of 30.
Take a ratio from inside one batch, never from two.

| Scenario | Time | Memory | Allocs | Speedup |
|----------|------|--------|-------:|:-------:|
| `GetAll` with virtual rendering (100 windows) | **1.37μs** | 3,776 B | 35 | **10.7x** |
| `GetAll` without virtual rendering (100 windows) | **14.6μs** | 113,104 B | 14 | baseline |

Memory is the sharper claim and it does not depend on the clock: **30x less
allocated per frame**, because the clipped side builds 30 rows and the unclipped
side builds all ~500.

### Line Height Tracking

| Scenario | Benchmark | Time | Memory | Allocs |
|----------|-----------|------|--------|-------:|
| Incremental, 1 dirty window (20 windows) | `JustEnsureLineHeights` | **788ns** | 638 B | 55 |
| Incremental, 1 dirty window (100 windows) | `EnsureLineHeightsIncremental` | **1.01μs** | 722 B | 65 |
| `lineHeights` array rebuilt over 100 windows | `EnsureLineHeightsFullRebuild` | **299ns** | 0 B | 0 |
| Every window re-wrapped from scratch (50 windows, 80↔120 cols) | `WindowBufferResize` | **0.17ms** | 195,135 B | 6,900 |

Two rows this table used to carry are gone rather than re-measured:
"~150μs (1 dirty window, uncached)" and "~7.1ms (all 100 windows rendered from
scratch)", both starred as historical estimates. Nothing in the tree measures
either, and the second is not what the benchmark named for it does —
`EnsureLineHeightsFullRebuild` sets `dirtyIndex = dirtyFullRebuild` but never
invalidates the windows, so every `Render` inside it hits the render cache and
the 299ns is the cost of rebuilding the height array from cached counts. A
genuine from-scratch pass is the resize row: that one does invalidate, and pays
a full re-wrap per window.

### Full Update Cycle (Delta + GetAll)

Measured via `BenchmarkWindowBufferDeltaWithGetAll` (100 windows, delta to last
window, viewport 30). Minimum of 10 runs at `-benchtime 1s`.

| Metric | Value | Memory | Allocs |
|--------|-------|--------|-------:|
| Delta + GetTotalLines + GetAll (incremental, 100 windows) | **2.79μs** | 6,921 B | 111 |

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
| Single cursor move (EnsureCursorVisible + updateContent) | **2.04μs** | 6,254 B | 38 |
| 20 cursor moves through the buffer | **39.0μs** | 125,090 B | 760 |
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
| `GetAll` viewport render of the whole session | **6.75μs** | 46,464 B | 51 |
| 20 cursor moves (j/k) through it | **149μs** | 929,788 B | 1,040 |
| Delta into a folded **tool** window (Uf preview) | **104ns** | 88 B | 2 |
| Delta into a folded **text** window, 2KB content | **15.1μs** | 5,112 B | 55 |

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
composed into it, plus the timestamp — 1.12μs per delta via
`BenchmarkWindowBufferDelta`). A folded text window's frame is still the dearer
of the two in **time** — 1.9x at 2KB, 3.4x at 32KB, 3.5x at 128KB — because
summarizing a message means reading it, while an expanded window's rows are
already wrapped and a frame only joins the ones the viewport shows. In
**memory** the order is now the one the design intends: 5,112 B against the
expanded side's 20,064 B at 2KB, and 134 KB against 757 KB at 128KB — because
the expanded frame still carries the whole-window work listed in
[What is left in the frame path](#what-is-left-in-the-frame-path).

### The fold summary materialized every cluster (found and fixed)

`BenchmarkFoldedTextStreamingDelta` was added by this revision to price the one
frame shape nothing else in the tree measured: a folded **text** window
streaming. It rebuilds its buffer under `StopTimer`, so one iteration is exactly
one frame at a fixed content size and its memory columns mean the same thing at
any `-benchtime`. Each size runs folded and expanded on identical content; the
expanded side is the control. Minimum of 4 runs at `-benchtime 1s`.

| Content size | Folded, as found | after the cutters streamed | after the summary stopped escaping first | Expanded (control) |
|---|---:|---:|---:|---:|
| 2KB | 99.0μs, 503,259 B, 95 | 17.5μs, 12,312 B, 63 | **15.1μs, 5,112 B, 55** | 7.84μs, 20,064 B, 97 |
| 32KB | 4.20ms, 13,739,011 B, 127 | 249μs, 222,752 B, 73 | **193μs, 35,833 B, 55** | 57.0μs, 221,878 B, 106 |
| 128KB | 13.5ms, 56,779,754 B, 143 | 934μs, 779,824 B, 77 | **770μs, 134,136 B, 55** | 220μs, 757,253 B, 111 |

End to end: **6.6x faster and 98x lighter at 2KB, 17.5x and 423x at 128KB.** The
folded side is now the *lighter* of the two at every size, which is what folding
is supposed to mean; it is still the dearer one in time (2.0x / 3.4x / 3.5x),
because summarizing a message means reading it while an expanded window's rows
are already wrapped and a frame only joins the ones on screen.

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
| now | ~300μs and ~57 KB (same benchmark: **6.10ms, 1.14 MB, 131 allocs**) | **109μs, 20.5 KB, 1 alloc** |

`InputFieldMoveLongLine` (a build plus 200 arrow moves) went 1.27ms → **471μs**
and 5.2 MB → **156 KB**; `InputFieldViewLongLine` (one `View()` of a 2000-cell
line) went 81μs → **25.4μs** and 306 KB → **5,064 B**. Movement was always
cheaper than insertion because `handleMovement` does not call
`ensureCursorVisible`; that asymmetry is unchanged and is now the only thing
separating the two.

The cutters were not the only caller paying for the list, and the missing tab
guard was not only the summary's. Markdown expands tabs per table line and per
wrapped row, so both fixes landed there too: `RenderMarkdownTables_Large`
293μs/646,897 B/5,418 allocs as found → **135μs/197,941 B/3,096**, which is
*below* what it measured before `c637636c` moved the per-cell cut onto the list
(210,282 B/3,503), and one 20-row table render 807 → 567 allocs, the figure the
old notes quoted. `FullWrap` 41,995 B → 29,066 B and `WindowBufferResize`
7,900 → 6,900 allocs are the same missing tab guard, one allocation per wrapped
line.

**What pins it.** Five tests: two on allocation shape, two differential against
the implementations they replaced, one on the cluster invariant the rune walk
broke. Allocation shape:
`TestCutsCostTheCutNotTheString` (both cutters and both folds, over 100-cluster
and 10,000-cluster inputs at the same budget) and
`TestLineQueriesCostOneEncoding` (the four input-chain queries over 100-rune and
10,000-rune lines) fail above a small ceiling — a materializing implementation
reports one allocation per cluster, ~10,000 against a ceiling of 8. The ceiling
is not an exact count because `AllocsPerRun` reads process-wide mallocs and a
`-race` build adds one of its own; the first version of the first test failed
under `-race` for exactly that reason. Behaviour: the two differential tests
above and `TestTailPartsNeverSplitsACluster`, plus the invariants that already
existed and still run — budget over the
whole breaker corpus, maximality and cluster alignment against the oracle, the
styled-cut guarantees, and 20,000 random lines of the structural test.

**What is left, deliberately.** One full-content copy per folded frame:
`rawContent()` joins the streaming delta parts into a single string before
anything can measure or cut it, and a profile of the 128 KB frame now puts 95%
of its remaining allocation in that join. The frame is 770μs and 134 KB,
0.3% of a 250ms tick, against the 5.4% and 56.8 MB it was. Removing the last
copy means deriving head and tail from `contentParts` directly — the head from
the first parts, the tail from the last — and the trap is a grapheme cluster
that straddles a part boundary, which is a real case for streaming deltas (a
combining mark arriving in its own chunk). That is a change with its own test
burden, and the frame budget does not ask for it yet. The expanded path has the
same join in `BuildInner`, plus two more O(window) items a frame does not need
(see [What is left in the frame path](#what-is-left-in-the-frame-path)).

### What is left in the frame path

Fixing the folded summary moved the "O(window) per frame" title to the expanded
window, which is the common case: at 128 KB of content the folded frame now
allocates 134,136 B (~1.05x the message) and the expanded one 757,253 B (~5.9x
it). Three things in `Window.Render` and its caller are proportional to the
whole window while a frame shows at most `viewportHeight` rows of it. None is
fixed here; all three are recorded so the next reader does not have to find them
again, and each is a separate change with its own test burden.

1. **`BuildInner` compacts the delta parts on every render.** Its fast path
   merges `contentParts` into `r.content` through a `strings.Builder` — a full
   copy of the message per frame — with a comment saying it prevents unbounded
   part growth. A threshold would serve that purpose; per-frame compaction
   picks the one moment the content is largest and the frames are most
   frequent. This is the folded side's remaining copy too (`rawContent`).
2. **`Window.Render` joins the whole window into `cache.inner`/`cache.rendered`
   and the shipped frame path never reads it.** The only production caller of
   `GetAll` (display.go) sets a viewport immediately before, so `renderVirtual`
   runs and takes its rows from `cache.lines[from:to]`; `cache.inner` is read by
   `renderAll` and `renderCursor`, which that path does not reach
   (`windowFragment` calls `Render` with `isCursor` false and discards the
   return). `renderAll` is the fallback for a zero-height viewport, so this is
   cold rather than dead — but it is built on every render, and it is the single
   biggest allocation in an expanded frame. Making it lazy behind the accessor
   that returns it is the change; the care it needs is that `Render`'s
   cache-hit early return hands `cache.rendered` straight back.
3. **`windowFragment` measures the width of every row to draw ≤40 of them.** It
   fills `cache.widths` for all of `cache.lines` and then slices `[from:to)`.
   The cache is per render generation, so during streaming that is a full
   `cellWidth` pass over the window per frame — time, not memory (the slice is
   8 B per row). Measuring the visible range only needs the cache keyed on the
   range as well as the generation, or no cache at all: 40 rows is nothing.

The reason all three survive is that they are O(window) with a small constant
and no allocation blow-up, while the two that were fixed were O(window) with a
constant of ~120 B and ~430 B per byte. The bound is worth stating plainly,
because it is the part that does not show up in any single measurement:
**assistant text and reasoning content are not capped.** `docs/truncation.md`
bounds *tool output* (64 KB in memory, then a scratch file); nothing bounds an
AT or AR window. So per-frame cost grows for as long as the model streams, and
the total work of one long answer is quadratic in its length. At 128 KB the
expanded frame is 220μs and the folded one 770μs, both well inside a 250ms
tick; at 4 MB they would not be. That is the argument for making a frame
O(viewport) rather than O(window), and it is an argument about growth, not about
any figure in this file.

### GetWindowLineRange

Minimum of 20 runs at `-benchtime 1s`; both are allocation-free.

| Scenario | Benchmark | Time |
|----------|-----------|------|
| Single lookup (windowIndex=50, 100 windows) | `WindowBufferGetWindowLineRange` | **20.9ns** |
| Three lookups (indices 50, 25, 75) | `GetWindowLineRangeCached` | **54.4ns total** |

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
| `WindowBufferGetAll` | **1.60μs** |
| `WindowBufferDeltaWithGetAll` | **2.79μs** |
| `VirtualRenderingCursorMovement` | **39.0μs** |
| `VirtualRenderingScroll` | **65.6μs** |
| `StreamingUpdateWithVirtualRendering` | **2.45μs** |
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
**22.1μs, 9,590 B, 1,772 allocs** (minimum of 10 runs at `-benchtime 1s`).

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
re-wrap of the 26KB message allocates **359 KB and 27,308** per operation today
against 607 KB and 27,335 before the width table, and markdown streaming
**12.8 KB and 1,072** against 18.4 KB and 1,195. **Neither reduction is the width
table's**, which is what this paragraph said until 2026-09-28: measured at
`c637636c^` and at `c637636c` on this machine, both figures are unchanged across
that commit (607 KB and 18.4 KB on both sides). They moved later, in two steps —
at `4cb2b34a` (476 KB, 15.1 KB) and again with this revision's missing tab guard
(359 KB, 12.8 KB); see [What moved](#what-moved-and-what-moved-it). The width
table's own measurable win is the one in the table above.

### Resize Performance

Minimum of 10 runs at `-benchtime 1s`.

| Scenario | Value | Memory | Allocs |
|----------|-------|--------|-------:|
| Resize 50 windows (80↔120 cols) | **0.17ms** | 195,135 B | 6,900 |

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

**And then this revision moved five of the rows again**, for a third reason: the
cutters stopped materializing clusters and the summary stopped escaping the
whole message before cutting it (`5da8383e` and the commit carrying these
notes). Full re-wrap 476 KB → **359 KB** and 27,328 → **27,308** allocs at
`300x`; incremental append 779 B → **648 B** and 61 → **58**; markdown streaming
15.1 KB → **12.8 KB** and 1,139 → **1,072**; `WindowBufferResize` 208,516 B →
**195,135 B** and 7,900 → **6,900** allocs; `JustEnsureLineHeights` 1.03μs →
**788ns**. The mechanism is in
[the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed); the
reason a *tab* guard shows up in a re-wrap and a resize is that
`wrapVisualLines` expands tabs per original line, and almost none of them
contain one.

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
2. **Render overhead is well under 0.01%** of wall time during streaming (2.45μs per 250ms tick ≈ 0.001%). The one thing that ever threatened this claim was the folded text summary, which spent 5.4% of a tick at 128KB of reasoning text while it materialized every cluster twice per frame and then escaped the whole message to cut 75 cells out of it; it spends 0.3% now ([the finding](#the-fold-summary-materialized-every-cluster-found-and-fixed)). The caveat that remains is about growth rather than this figure: window content is not capped, so a frame costs more the longer the message it draws — see [What is left in the frame path](#what-is-left-in-the-frame-path)
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
| `MarkdownStreaming_PlainDeltas` | 1,195 | 1,195 | 1,195 | **1,072** |
| `MarkdownStreaming_RawMode` | 1,194 | 1,194 | 1,194 | **1,071** |
| `MarkdownStreaming_TableDeltas` | 17,003 | 52,819 | — | **47,980** |
| `RenderMarkdownTables_Large` | 1,971 | 3,503 | 5,418 | **3,096** |
| `RenderMarkdownTables_Small` | 56 | 101 | 132 | **90** |

Read the last two rows as a pair: the reflow's real cost was 1,971 → 3,503
allocs on the large transform (~1.8x, because a record can now span several
visual lines), the width table then took it to 5,418 for no functional gain, and
this revision takes it to **3,096** — below where it started, because the
transform expands tabs per table line and per wrapped row and `expandTabs` no
longer rebuilds a string that has no tab in it. The `PlainDeltas` fall from
1,195 to 1,072 is that same guard plus `4cb2b34a`'s earlier cut.

Allocation counts are exact and repeatable — every run of every column reported
the same integers — which is why the table is stated in allocations.

**The timings are not quoted deliberately.** Repeated `-benchtime=200x -count=5`
runs on this machine put them in a ±40% band (`RawMode` 12.4–21.9μs,
`TableDeltas` 0.99–1.16ms, `Large` 0.139–0.161ms). An earlier revision quoted
single-run microseconds and had to be "corrected" twice against pure noise.
Ratios are stable, point estimates are not.

What the allocations prove: markdown-mode streaming of non-table deltas costs
**exactly** what raw mode costs, one alloc above the raw baseline — 1,072 against
1,071 on this tree, the same one-off as at `4719fdcc` — so the table path is
provably entered by nothing but table-touching deltas. The table-bearing rows are
up because a record can now span several visual lines and each column's grapheme
widths are measured. Note that the *plain* rows fell (1,195 → 1,072) in two
later steps, not at the reflow: the identity held across every change, and the
absolute counts moved under it.

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
If the renderer's `wrappedLines` is populated, this returns the line count in 1.03μs
for the whole pass, without rendering. The actual `w.Render()` — which joins wrapped
lines, composes the window's own row, and renders the style layer — is deferred to
`GetAll` → `renderVirtual`, which needs the rendered output for the viewport anyway.

The deferral is also what keeps a folded window's summary out of line tracking:
a full `lineHeights` rebuild over a 100-window buffer costs 299ns and allocates
nothing (`EnsureLineHeightsFullRebuild`), because a folded window answers `1`
without a renderer call at all and an unfolded one answers from the count its
last render cached. Deriving the summary arrives in the frame instead, and only
for the folded windows the viewport actually shows.
