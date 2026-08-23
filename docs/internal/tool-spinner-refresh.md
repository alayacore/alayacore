# Tool Spinner Refresh During Silent Execution

## Problem

While a tool executes with no output (e.g. `sleep 60`, a long compile, a
slow MCP call), the tool window's header spinner (`TOOL CALL ⠋`) freezes
at the last rendered frame.

Root cause chain:

1. The spinner glyph is **baked into the window's border cache** at render
   time (`BuildCollapsed`/`BuildExpanded` → `statusDot()` →
   `toolSpinnerFrame()`, a pure wall-clock function: 10 frames × 150ms).
2. Border caches are only rebuilt when a window is invalidated, and that
   is **delta-driven**: `Af` argument deltas, `Uf` preview deltas, and
   final `UF` frames all call `w.Invalidate()`. A silent tool emits none
   of these.
3. The idle-tick path short-circuits by design (perf commit `7134442b`):
   `handleDisplayRefresh` → `DrainDirty() == false` → no render, and
   `DisplayModel.updateContent` early-exits on `!contentDirty &&
   !IsDirty`. Even the `Program.run` same-content check would skip a
   byte-identical frame.

The session-loading screen does not freeze because `Terminal.View()`
rebuilds `renderLoadingView()` fresh on every tick (a separate, fully
tick-driven path that bypasses the display cache). The tool spinner had
no such driver.

## Fix

`WindowBuffer.InvalidateRunningToolSpinners()` (called from
`handleDisplayRefresh` on every tick):

- Scans windows whose renderer is a `*toolRenderer` with status
  `ToolStatusPending` (executing) and calls `w.Invalidate()` +
  `markDirty(i)` — the border cache is rebuilt on the next `GetAll` with
  the current wall-clock frame.
- Returns `false` when no tool is executing, so the idle tick keeps the
  100% skip behavior: benchmarked at **76ns/op, 0 allocs** for a 100-window
  idle buffer (pending case: **100ns/op, 0 allocs**; the subsequent
  viewport render is ~6.5μs on the dev machine, paid only while a tool
  runs).

### Design notes (read before refactoring)

- **Two independent dirty signals.** `outputWriter.DrainDirty()` consumes
  the output writer's own atomic flag (set by frame handlers); the
  invalidation sets `WindowBuffer.dirty` (consumed by
  `DisplayModel.updateContent` via `IsDirty`). `handleDisplayRefresh`
  therefore merges the two explicitly (`spinnerRefresh || DrainDirty()`).
  The invalidation is done **before** `DrainDirty` so a stream of
  unrelated deltas cannot starve the spinner — the two signals are OR'd
  into one render decision.
- **Status granularity.** Only `ToolStatusPending` is refreshed. Arguments
  streaming (`ToolStatusNone`) is delta-driven by construction — every
  `Af` append invalidates — so polling it would be wasted work.
- **Lock discipline.** `InvalidateRunningToolSpinners` takes `wb.mu`
  alone, never nested with `outputWriter.mu` (see the ordering documented
  in `output.go`). Tool status lives on the renderer inside each window,
  so the model exposes this invalidation rather than handing windows out
  for the presentation layer to mutate under the buffer lock — this
  coupling is deliberate.
- **Why not a session-side heartbeat (periodic `Uf`)?** It would only
  cover the streaming tools (`execute_command`, `search_content`) that own
  a `streamingWriter`; MCP calls and any future non-streaming tool would
  still freeze. It also needs a per-command ticker goroutine and a
  `lastSent`-dedup bypass. The TUI-side fix covers **every** tool with a
  pending window.
- **Why not a render-time glyph patch** (like the cursor-arrow
  replacement in `windowFragment`)? The `updateContent` early-exit still
  requires per-tick dirtying, so the hook is not avoided; it would only
  skip one border rebuild (~100ns). It would extend a documented aliasing
  pitfall (`lines` aliases `w.border.lines`) into the most fragile,
  highest-gocyclo rendering code for negligible gain.
- **Why `ToolStatusPending` only — not a general animation framework?**
  Two animations (loading screen, tool spinner) with different lifecycles
  do not justify an abstraction; it would add indirection, not clarity.

### Future direction

If the idle scan (76ns/tick) ever matters, replace it with an
`atomic.Int32` running-tool counter maintained at the ~5 status
transition sites — not before profile data demands it.
