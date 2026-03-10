# Terminal Display Performance Analysis

## Problem
When large amounts of delta data arrive or many message windows are stacked, the terminal becomes severely laggy and keyboard input is delayed.

## Root Causes (identified)

### 1. **Update() blocks key handling on display refresh** (Critical)
**Location**: `internal/adaptors/terminal/terminal.go`

The `Update()` method processes `updateChan` **before** handling the incoming message (e.g., `tea.KeyMsg`):

```go
select {
case <-m.out.updateChan:
    // Heavy work: GetAll(), updateDisplayHeight(), updateContent()
    ...
default:
}
switch msg := msg.(type) {
case tea.KeyMsg:
    return m.handleKeyMsg(msg)  // Key handling happens AFTER expensive update
```

**Impact**: Every keypress waits for a full display rebuild. Bubble Tea's event loop is single-threaded—blocking Update() blocks all input.

### 2. **Duplicate GetAll() per update cycle** (High)
**Location**: `display.go:UpdateHeightForTodos()` line 243, `display.go:updateContent()` line 130

Each update triggers:
1. `updateDisplayHeight()` → `GetAll(m.windowCursor)` to compute `totalLines`
2. `updateContent()` → `GetAll(cursorIndex)` again for viewport content

**Impact**: 2× full window buffer rebuild per update. `GetTotalLines()` exists but isn't used.

### 3. **Full re-render on every delta** (High)
**Location**: `internal/adaptors/terminal/window.go:rebuildCache()`

Every delta sets `dirty=true` on the whole buffer. `rebuildCache()` re-renders **all** windows, including:
- `lipgloss.Wrap()` on full content per window (O(content length))
- Border styling per window
- String concatenation

**Impact**: With 20+ windows and 50KB+ content, each rebuild processes megabytes of text. No incremental rendering—only the last window changes during streaming, but we re-wrap everything.

### 4. **renderWithCursor discards cache when cursor active** (Medium)
**Location**: `window.go:GetAll()` lines 187-194

When `cursorIndex >= 0`, we always call `renderWithCursor()` which re-renders all windows. The cursor only changes border style—content is identical. We could apply border swap without re-wrapping.

### 5. **Write() holds lock during full TLV processing** (Medium)
**Location**: `output.go:Write()` lines 80-86

Session goroutine holds `w.mu` while parsing all TLV messages, calling `renderMultiline`, `AppendOrUpdate`, etc. Long streams block the writer.

### 6. **lipgloss.Wrap cost** (Medium)
**Location**: `window.go:renderWindowContent()` lines 269, 301

`lipgloss.Wrap()` processes full content with rune-width calculation and ANSI handling. Called for every window on every rebuild.

## Implemented Fixes

1. **Handle KeyMsg before updateChan** ✅ - KeyMsg returns immediately; display updates only on tickMsg (every 500ms during streaming)
2. **Use GetTotalLines() in UpdateHeightForTodos** ✅ - Avoids GetAll() + strings.Count; GetTotalLines() now ensures cache and returns count without allocating full render string
3. **Increase update throttle** ✅ - 100ms → 150ms to reduce update frequency during heavy streaming

4. **Incremental window rendering** ✅ - Only re-render dirty window(s), reuse cached renders for others
   - dirtyIndex: -1=clean, >=0=single window dirty, fullRebuild(-2)=all
   - rebuildOneWindow re-renders one window, concatenates with others' cache

## Remaining (lower priority)

5. **Cursor-only border swap** - When cursor changes, reuse content and only swap border style
