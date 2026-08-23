package terminal

// WindowBuffer manages multiple Windows with virtual rendering support.
// It coordinates line height tracking for cursor navigation and provides
// virtual rendering (only visible windows are rendered) for performance.

import (
	"strconv"
	"strings"
	"sync"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// WindowBuffer - Manages Multiple Windows with Virtual Rendering
// ============================================================================

// WindowBuffer holds a sequence of windows with virtual rendering support.
// WindowBuffer.mu only protects window data and is never nested inside
// outputWriter.mu — SnapshotStatus et al. use atomic fields to avoid
// lock ordering inversions. See output.go for details.
type WindowBuffer struct {
	mu          sync.Mutex
	windows     []*Window
	idIndex     map[string]int
	width       int
	styles      *Styles
	borderStyle Style

	// markdownDefault is the initial markdown rendering state for new
	// assistant text windows (AT/AR). Defaults to on (markdown rendered);
	// --no-markdown sets it false. Per-window toggling with 'r' is
	// unaffected.
	markdownDefault bool

	// Line height tracking (for cursor navigation)
	lineHeights []int
	totalLines  int
	dirty       bool // true if lineHeights needs rebuild
	dirtyIndex  int  // index of single dirty window, -1 = clean, -2 = full rebuild

	// Virtual rendering state
	viewportYOffset int
	viewportHeight  int
}

// Sentinel values for dirtyIndex
const (
	dirtyClean       = -1 // no dirty windows
	dirtyFullRebuild = -2 // multiple windows dirty, need full rebuild
)

// NewWindowBuffer creates a new window buffer with given width and styles.
func NewWindowBuffer(width int, styles *Styles) *WindowBuffer {
	return &WindowBuffer{
		windows:         []*Window{},
		idIndex:         make(map[string]int),
		width:           width,
		styles:          styles,
		borderStyle:     NewStyle().Foreground(styles.ColorDim),
		lineHeights:     []int{},
		dirtyIndex:      dirtyClean,
		markdownDefault: true, // markdown rendering on by default
	}
}

// SetMarkdownDefault sets the initial markdown rendering state for new
// assistant text windows (AT/AR). Existing windows are unaffected.
func (wb *WindowBuffer) SetMarkdownDefault(on bool) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.markdownDefault = on
}

// SetWidth updates the window width (called on terminal resize).
func (wb *WindowBuffer) WithWidth(width int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.width != width {
		wb.width = width
		// Invalidate all windows
		for _, w := range wb.windows {
			w.Invalidate()
		}
		wb.dirty = true
		wb.dirtyIndex = dirtyFullRebuild // all windows affected
	}
}

// IsDirty returns true when the window buffer has unrendered changes
// (new content appended, windows invalidated by resize/theme) and the
// caller should re-run ensureLineHeights / GetAll before reading line
// counts or rendered content. Does NOT reset the flag — callers that
// want a "consume" semantics should use DrainDirty.
//
// Used by DisplayModel.updateContent to short-circuit the render path
// when the tick handler fires but no buffer-level changes occurred:
// the tick runs 4×/sec, but windows typically change a handful of
// times per task.
func (wb *WindowBuffer) IsDirty() bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.dirty
}

// DrainDirty returns true if the buffer is dirty and resets the flag.
// Distinct from IsDirty so the renderer can keep a stable view of
// dirty-ness while the rest of the system updates.
func (wb *WindowBuffer) DrainDirty() bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	dirty := wb.dirty
	wb.dirty = false
	wb.dirtyIndex = dirtyClean
	return dirty
}

func (wb *WindowBuffer) Width() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.width
}

// SetStyles updates the styles for the window buffer.
func (wb *WindowBuffer) WithStyles(styles *Styles) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.styles = styles
	wb.borderStyle = NewStyle().Foreground(styles.ColorDim)
	// Invalidate all windows to pick up new styles
	for _, w := range wb.windows {
		w.styles = styles // Update window's styles reference
		w.Invalidate()
	}
	wb.dirty = true
	wb.dirtyIndex = dirtyFullRebuild
}

// AppendOrUpdate adds content to an existing window or creates a new one.
// Used for text content (UT, AT, AR, SE, SN) and replayed UF sessions.
// Tool windows use HandleToolInputEvent and HandleToolOutput instead.
// Returns the index of the window in the buffer.
func (wb *WindowBuffer) AppendOrUpdate(tag string, id string, content string) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		w.AppendContent(content)
		w.EnsureVisibleContent(content)
		wb.markDirty(idx)
		return idx
	}

	// Create new window. Assistant text (AT) starts expanded; everything
	// else — user text (UT), tools (AF/UF), reasoning (AR), system
	// messages (SN/SE) — starts collapsed.
	folded := tag != tlv.TagAssistantT
	historyID := parseHistoryID(id)
	w := NewWindow(id, tag, wb.styles)
	w.HistoryID = historyID
	w.Folded = folded
	w.Visible = hasVisibleContent(content)
	w.SetMarkdownDefault(wb.markdownDefault) // AT/AR: default rendering mode
	w.AppendContent(content)                 // set initial content via renderer

	wb.windows = append(wb.windows, w)
	idx := len(wb.windows) - 1
	wb.idIndex[id] = idx
	wb.markDirty(idx)
	return idx
}

// HandleToolInputEvent processes a TagAssistantF (AF) frame.
// A frame with Name non-empty and Input empty is a "start" that sets
// the tool name. All other frames carry actual tool arguments.
// Status defaults to "pending" when a tool window is created —
// the final status arrives via HandleToolOutput (UF).
func (wb *WindowBuffer) HandleToolInputEvent(data protocol.ToolInputData, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[data.ID]; ok {
		w := wb.windows[idx]
		w.HandleToolInput(data, historyID)
		wb.markDirty(idx)
		return
	}

	// Create new window with tool renderer
	w := NewWindow(data.ID, tlv.TagAssistantF, wb.styles)
	w.HistoryID = historyID
	w.Folded = true
	w.Visible = true
	w.SetRendererForTool(data.Name, string(data.Input))
	if w.renderer != nil {
		if tr, ok := w.renderer.(*toolRenderer); ok && tr.status == ToolStatusNone {
			tr.status = ToolStatusPending
		}
	}

	wb.windows = append(wb.windows, w)
	wb.idIndex[data.ID] = len(wb.windows) - 1
	wb.markDirty(len(wb.windows) - 1)
}

// HandleToolInputDelta processes a TagAssistantFDelta (Af) frame.
// Carries a partial JSON chunk of tool arguments during streaming.
// For display, we show a truncated one-line preview alongside the tool name.
// If no window exists yet for this ID, we create a placeholder.
func (wb *WindowBuffer) HandleToolInputDelta(id, name, delta string, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	idx, ok := wb.idIndex[id]
	if !ok {
		// No window yet — create placeholder with just the tool name.
		w := NewWindow(id, tlv.TagAssistantF, wb.styles)
		w.HistoryID = historyID
		w.Folded = true
		w.Visible = true
		if name != "" {
			w.SetRendererForTool(name, "")
			if tr, ok := w.renderer.(*toolRenderer); ok && tr.status == ToolStatusNone {
				tr.status = ToolStatusPending
			}
		}
		wb.windows = append(wb.windows, w)
		wb.idIndex[id] = len(wb.windows) - 1
		idx = len(wb.windows) - 1
	}

	w := wb.windows[idx]
	// Apply the delta to the tool renderer.
	if tr, ok := w.renderer.(*toolRenderer); ok {
		tr.AppendDelta(delta)
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.Invalidate()
	wb.markDirty(idx)
}

// HandleToolOutputDelta processes a TagUserFDelta (Uf) frame.
// Updates the tool result preview with an ephemeral snapshot. The
// authoritative result arrives later via HandleToolOutput (UF), which
// overwrites the preview — Uf frames are display-only and may be dropped.
func (wb *WindowBuffer) HandleToolOutputDelta(id, text string, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	idx, ok := wb.idIndex[id]
	if !ok {
		// No window yet — create a placeholder (e.g. Uf arrived first).
		w := NewWindow(id, tlv.TagUserF, wb.styles)
		w.HistoryID = historyID
		w.Folded = true
		w.Visible = true
		w.SetRendererForTool("", "")
		if tr, ok := w.renderer.(*toolRenderer); ok {
			tr.status = ToolStatusPending
		}
		wb.windows = append(wb.windows, w)
		wb.idIndex[id] = len(wb.windows) - 1
		idx = len(wb.windows) - 1
	}

	w := wb.windows[idx]
	if tr, ok := w.renderer.(*toolRenderer); ok {
		tr.output = text
		if tr.status == ToolStatusNone {
			tr.status = ToolStatusPending
		}
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.Invalidate()
	wb.markDirty(idx)
}

// HandleToolOutput processes a TagUserF (UF) frame.
func (wb *WindowBuffer) HandleToolOutput(id, output string, isError bool, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		w.HandleToolOutput(output, isError, historyID)
		wb.markDirty(idx)
		return
	}

	// No prior AF window (e.g. replayed from session file) — create one.
	status := ToolStatusSuccess
	if isError {
		status = ToolStatusError
	}
	w := NewWindow(id, tlv.TagUserF, wb.styles)
	w.HistoryID = historyID
	w.Folded = true
	w.Visible = true
	w.SetRendererForTool("", "")
	if tr, ok := w.renderer.(*toolRenderer); ok {
		tr.output = output
		tr.status = status
	}

	wb.windows = append(wb.windows, w)
	wb.idIndex[id] = len(wb.windows) - 1
	wb.markDirty(len(wb.windows) - 1)
}

// markDirty marks that line heights need rebuilding.
// Uses sentinel values to track single vs multiple dirty windows:
//   - dirtyClean (-1): no dirty windows
//   - dirtyFullRebuild (-2): multiple windows dirty, need full rebuild
//   - >= 0: index of the single dirty window
//
// This enables incremental updates during streaming (same window repeatedly)
// while correctly triggering full rebuild for session loading (multiple windows rapidly).
func (wb *WindowBuffer) markDirty(idx int) {
	if wb.dirtyIndex == dirtyFullRebuild {
		// Already marked for full rebuild, keep it
		return
	}
	if wb.dirtyIndex >= 0 && wb.dirtyIndex != idx {
		// Different window already dirty - need full rebuild
		wb.dirtyIndex = dirtyFullRebuild
	} else {
		// Either clean or same window - mark just this one
		wb.dirtyIndex = idx
	}
	wb.dirty = true
}

// InvalidateRunningToolSpinners marks every executing tool window (status
// ToolStatusPending) for re-render so its header rebuilds with the current
// wall-clock spinner frame on the next GetAll. The tick handler calls this
// when the display is otherwise idle: the spinner glyph is baked into the
// window's border cache at the last render, so a long-running silent tool
// (no Uf/Af frames → no delta-driven invalidation) would freeze the
// spinner without it.
//
// Returns true when at least one window was invalidated, so callers can
// skip the render path entirely when no tool is executing (idle tick
// stays zero-cost).
//
// The invalidation sets wb.dirty (consumed by DisplayModel.updateContent
// via IsDirty), NOT the outputWriter's drain flag — a DrainDirty() ==
// false result does not reflect this invalidation, so the caller must
// merge the return value into its own render decision.
//
// Lock discipline: takes wb.mu alone, never nested with outputWriter.mu
// (see the ordering documented in output.go). Tool status lives on the
// renderer inside each window, so the model exposes this invalidation
// rather than handing windows out for the presentation layer to mutate
// under the buffer lock.
func (wb *WindowBuffer) InvalidateRunningToolSpinners() bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	refreshed := false
	for i, w := range wb.windows {
		tr, ok := w.renderer.(*toolRenderer)
		if !ok || tr.status != ToolStatusPending {
			continue
		}
		w.Invalidate() // border cache stale → rebuilt with current frame
		wb.markDirty(i)
		refreshed = true
	}
	return refreshed
}

// Clear removes all windows.
func (wb *WindowBuffer) Clear() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.windows = nil
	wb.idIndex = make(map[string]int)
	wb.lineHeights = nil
	wb.totalLines = 0
	wb.dirty = true
	wb.dirtyIndex = dirtyClean
}

// AppendUserContent appends a user content frame to the window identified by id.
// The window must already exist (created by a prior AppendOrUpdate call).
// This is safe for concurrent access (holds WindowBuffer.mu).
func (wb *WindowBuffer) AppendUserContent(id, tag, value string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if idx, ok := wb.idIndex[id]; ok {
		wb.windows[idx].AppendFromTLV(tag, value)
		wb.markDirty(idx)
	}
}

// SetWindowVisible marks the window with the given ID as visible.
// No-op if the window doesn't exist.
func (wb *WindowBuffer) SetWindowVisible(id string) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if idx, ok := wb.idIndex[id]; ok {
		wb.windows[idx].Visible = true
	}
}

func (wb *WindowBuffer) WindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return len(wb.windows)
}

// Returns nil if out of bounds.
func (wb *WindowBuffer) WindowAt(index int) *Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if index < 0 || index >= len(wb.windows) {
		return nil
	}
	return wb.windows[index]
}

// LookupID returns the window index by ID, or false if not found.
func (wb *WindowBuffer) LookupID(id string) (int, bool) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	idx, ok := wb.idIndex[id]
	return idx, ok
}

// SetHistoryID sets the HistoryID of the window with the given ID.
// No-op if the ID is not registered.
func (wb *WindowBuffer) SetHistoryID(id string, historyID uint64) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if idx, ok := wb.idIndex[id]; ok {
		wb.windows[idx].HistoryID = historyID
	}
}

// AllWindows returns a copy of the windows slice for snapshotting.
// The returned slice contains the same *Window pointers (no deep copy).
// Each window's Content is built from parts before returning.
func (wb *WindowBuffer) AllWindows() []*Window {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	result := make([]*Window, len(wb.windows))
	copy(result, wb.windows)
	return result
}

func (wb *WindowBuffer) GetVisibleWindowCount() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	count := 0
	for _, w := range wb.windows {
		if w.Visible {
			count++
		}
	}
	return count
}

func (wb *WindowBuffer) ToggleFold(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.windows) {
		return false
	}
	wb.windows[windowIndex].Folded = !wb.windows[windowIndex].Folded
	wb.markDirty(windowIndex)
	return true
}

// ToggleMarkdownMode toggles markdown rendering for the window at
// the given index. No-op for windows that don't render markdown
// (user prompts, tools, system messages). Returns true when toggled.
func (wb *WindowBuffer) ToggleMarkdownMode(windowIndex int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.windows) {
		return false
	}
	if !wb.windows[windowIndex].ToggleMarkdownMode() {
		return false
	}
	wb.markDirty(windowIndex)
	return true
}

// FunctionInfo holds details about a tool call window.
type FunctionInfo struct {
	ID    string // tool call ID
	Name  string // tool name (e.g. "read_file")
	Input string // tool call input/arguments (formatted for display)
}

// Returns nil if no window with that ID exists or if it's not a tool window.

// HasWindow returns true if a window with the given ID exists.
func (wb *WindowBuffer) HasWindow(id string) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	_, ok := wb.idIndex[id]
	return ok
}

func (wb *WindowBuffer) GetFunctionInfo(id string) *FunctionInfo {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if idx, ok := wb.idIndex[id]; ok {
		w := wb.windows[idx]
		if ti := w.ToolInfo(); ti != nil {
			return &FunctionInfo{
				ID:    w.ID,
				Name:  ti.Name,
				Input: ti.Input,
			}
		}
	}
	return nil
}

// For tool windows, returns tool input + tool output combined.
// Returns empty string if index is out of bounds.
func (wb *WindowBuffer) GetWindowContent(windowIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if windowIndex < 0 || windowIndex >= len(wb.windows) {
		return ""
	}

	w := wb.windows[windowIndex]
	if ti := w.ToolInfo(); ti != nil {
		if tr, ok := w.renderer.(*toolRenderer); ok && tr.output != "" {
			return ti.Input + "\n" + tr.output
		}
		return ti.Input
	}
	// For non-tool windows, return raw accumulated text
	return w.RawContent()
}

// ============================================================================
// Line Height Tracking
// ============================================================================

// ensureLineHeights rebuilds line heights if dirty.
// Supports incremental update when only one window changed.
// blocked is passed through to Window.Render for cache coherency (line counts
// are unaffected by dimming, but the cache entry must match what renderVirtual
// will request).
//
// During incremental updates, UpdateLineCountFast is tried first (fast path using
// len(wrappedLines) from TryLineCount). If the cache is stale, full Render is used
// instead. The rendered string is cached in Window.border and reused by
// GetAll → renderVirtual, which needs the content for the viewport.
// This avoids an O(n) render in ensureLineHeights that would be immediately
// overwritten by renderVirtual's own w.Render() call.
func (wb *WindowBuffer) ensureLineHeights(blocked bool) {
	if !wb.dirty && len(wb.lineHeights) == len(wb.windows) {
		return
	}

	// Extend lineHeights slice if needed
	for len(wb.lineHeights) < len(wb.windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Incremental update: only re-render the dirty window
	if wb.dirtyIndex >= 0 && wb.dirtyIndex < len(wb.windows) {
		w := wb.windows[wb.dirtyIndex]
		// Only render and count lines for visible windows
		if w.Visible {
			// Fast path: try UpdateLineCountFast first (~58μs when cache valid,
			// otherwise falls through to full Render ~100-200μs).
			if lc, ok := w.UpdateLineCountFast(wb.width); ok {
				oldHeight := wb.lineHeights[wb.dirtyIndex]
				wb.lineHeights[wb.dirtyIndex] = lc
				wb.totalLines += lc - oldHeight
			} else {
				w.Render(wb.width, false, wb.styles, wb.borderStyle, blocked)
				oldHeight := wb.lineHeights[wb.dirtyIndex]
				newHeight := w.LineCount()
				wb.lineHeights[wb.dirtyIndex] = newHeight
				wb.totalLines += newHeight - oldHeight
			}
		} else {
			// Non-visible windows contribute 0 lines
			oldHeight := wb.lineHeights[wb.dirtyIndex]
			wb.lineHeights[wb.dirtyIndex] = 0
			wb.totalLines -= oldHeight
		}
	} else {
		// Full rebuild (dirtyIndex == dirtyFullRebuild or first init)
		wb.totalLines = 0
		for i, w := range wb.windows {
			// Only render and count lines for visible windows
			if w.Visible {
				if w.Folded {
					// Folded windows are always a single line; no rendering
					// needed for line tracking (renderVirtual renders the
					// collapsed line on demand for viewport windows).
					wb.lineHeights[i] = 1
					wb.totalLines++
					continue
				}
				w.Render(wb.width, false, wb.styles, wb.borderStyle, blocked)
				wb.lineHeights[i] = w.LineCount()
				wb.totalLines += wb.lineHeights[i]
			} else {
				// Non-visible windows contribute 0 lines
				wb.lineHeights[i] = 0
			}
		}
	}
	wb.dirty = false
	wb.dirtyIndex = dirtyClean
}

// Returns (0, 0) if windowIndex is out of bounds.
// IMPORTANT: This calls ensureLineHeights to guarantee accurate positions,
// since line heights may be stale after content updates.
func (wb *WindowBuffer) GetWindowLineRange(windowIndex int) (start, end int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Ensure line heights are current before calculating
	wb.ensureLineHeights(false)

	if windowIndex < 0 || windowIndex >= len(wb.lineHeights) {
		return 0, 0
	}

	for i := range windowIndex {
		start += wb.lineHeights[i]
	}
	return start, start + wb.lineHeights[windowIndex]
}

func (wb *WindowBuffer) GetTotalLines() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights(false)
	return wb.totalLines
}

// ForEachVisible iterates forward over visible windows starting from the
// given index (inclusive), calling fn with the window index and pointer.
// If fn returns false, iteration stops. Returns true if all visible windows
// were visited (fn never returned false).
//
// This is one of four core iteration methods:
//   - ForEachVisible(index, fn(i, w))        — forward from index
//   - ForEachVisibleBackward(index, fn(i, w)) — backward from index
//   - ForEachVisibleRanged(fn(i, start, end)) — forward from 0 with line ranges
//   - ForEachVisibleBackwardRanged(fn(i, start, end)) — backward from end with line ranges
//
// Use the non-ranged variants for property-based searches (j, k, f, b).
// Use the ranged variants for position-based searches (H, L, M, center helpers).
func (wb *WindowBuffer) ForEachVisible(start int, fn func(i int, w *Window) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights(false)

	for i, w := range wb.windows {
		if i >= start && w.Visible && !fn(i, w) {
			return false
		}
	}
	return true
}

// ForEachVisibleBackward iterates backward over visible windows starting
// from the given index (inclusive). See ForEachVisible for callback semantics.
func (wb *WindowBuffer) ForEachVisibleBackward(start int, fn func(i int, w *Window) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights(false)

	if start >= len(wb.windows) {
		start = len(wb.windows) - 1
	}
	if start < 0 {
		return true
	}

	for i := start; i >= 0; i-- {
		if wb.windows[i].Visible && !fn(i, wb.windows[i]) {
			return false
		}
	}
	return true
}

// ForEachVisibleRanged iterates forward over all visible windows, calling fn
// with the window index and its line range [start, end). If fn returns false,
// iteration stops. Returns true if all visible windows were visited.
//
// Use this variant for viewport-aware positioning (H, M, L, center helpers).
// For property-based searches, use ForEachVisible instead.
func (wb *WindowBuffer) ForEachVisibleRanged(fn func(i int, startLine, endLine int) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights(false)

	pos := 0
	for i, w := range wb.windows {
		end := pos + wb.lineHeights[i]
		if w.Visible && !fn(i, pos, end) {
			return false
		}
		pos = end
	}
	return true
}

// ForEachVisibleBackwardRanged iterates backward over all visible windows,
// calling fn with the window index and its line range [start, end).
// If fn returns false, iteration stops. Returns true if all visible
// windows were visited.
func (wb *WindowBuffer) ForEachVisibleBackwardRanged(fn func(i int, startLine, endLine int) bool) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights(false)

	// Pass 1: compute total lines
	total := 0
	for _, h := range wb.lineHeights {
		total += h
	}

	// Pass 2: walk backward, deriving start/end from total
	pos := total
	for i := len(wb.windows) - 1; i >= 0; i-- {
		pos -= wb.lineHeights[i]
		if wb.windows[i].Visible && !fn(i, pos, pos+wb.lineHeights[i]) {
			return false
		}
	}
	return true
}

// Returns -1 if none are visible.
func (wb *WindowBuffer) FirstVisibleIndex() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for i, w := range wb.windows {
		if w.Visible {
			return i
		}
	}
	return -1
}

// Returns -1 if none are visible.
func (wb *WindowBuffer) LastVisibleIndex() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for i := len(wb.windows) - 1; i >= 0; i-- {
		if wb.windows[i].Visible {
			return i
		}
	}
	return -1
}

// NearestVisibleIndex returns the index of a visible window nearest to the
// given index, searching forward first then backward, or -1 if no visible
// windows exist.
func (wb *WindowBuffer) NearestVisibleIndex(index int) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	n := len(wb.windows)
	if n == 0 {
		return -1
	}
	// Clamp index to bounds
	if index < 0 {
		index = 0
	}
	if index >= n {
		index = n - 1
	}
	// Search forward first, then backward
	for i := index; i < n; i++ {
		if wb.windows[i].Visible {
			return i
		}
	}
	for i := index - 1; i >= 0; i-- {
		if wb.windows[i].Visible {
			return i
		}
	}
	return -1
}

// ============================================================================
// Virtual Rendering
// ============================================================================

// SetViewportPosition updates viewport state for virtual rendering.
func (wb *WindowBuffer) SetViewportPosition(yOffset, height int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.viewportYOffset = yOffset
	wb.viewportHeight = height
}

// GetAll returns rendered windows, using virtual rendering if viewport is set.
func (wb *WindowBuffer) GetAll(cursorIndex int, blocked bool) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	if len(wb.windows) == 0 {
		return ""
	}

	// Ensure line heights are current (pass blocked for cache coherency)
	wb.ensureLineHeights(blocked)

	// Use virtual rendering if viewport is set
	if wb.viewportHeight > 0 {
		return wb.renderVirtual(cursorIndex, blocked)
	}

	// Full render
	return wb.renderAll(cursorIndex, blocked)
}

// renderVirtual renders only the windows overlapping the viewport
// [viewportYOffset, viewportYOffset+viewportHeight), clipped to VISUAL
// lines, and returns the soft-wrap fragment output:
//
//   - within a window, the visual lines are joined WITHOUT '\n'
//     (continuous text the terminal soft-wraps — copying a selection
//     keeps the original line structure, no fake newlines);
//   - between windows a single '\n' separates (prevents windows from
//     merging into one soft-wrap run);
//   - every visual line except the last of a window's fragment is padded
//     with trailing spaces to the full window width, so the terminal's
//     soft-wrap falls exactly at the simulated breakpoints (docs/internal/virtual-rendering-performance.md).
//
// The viewport is the final clip — no buffer windows or blank
// placeholders are emitted; ScrollView pads to the viewport height.
//
//nolint:gocyclo // fragment assembly branches
func (wb *WindowBuffer) renderVirtual(cursorIndex int, blocked bool) string {
	if len(wb.windows) == 0 {
		return ""
	}

	startLine := wb.viewportYOffset
	endLine := wb.viewportYOffset + wb.viewportHeight

	startWindow := wb.findWindowAtLine(startLine)
	endWindow := wb.findWindowAtLine(max(0, endLine-1))

	var sb strings.Builder
	firstWritten := false
	emittedRows := 0
	pos := 0 // running line position across windows
	for i := range wb.windows {
		winStart := pos
		winEnd := pos + wb.lineHeights[i]
		pos = winEnd

		if i < startWindow || i > endWindow {
			continue
		}
		w := wb.windows[i]
		if !w.Visible {
			continue
		}

		// Clip the window's visual lines to the visible range.
		from := max(startLine, winStart) - winStart
		to := min(endLine, winEnd) - winStart
		if from >= to {
			continue
		}

		lines, widths := wb.windowFragment(w, from, to, cursorIndex == i, blocked)
		emittedRows += len(lines)

		if firstWritten {
			sb.WriteString("\n")
		}
		// Join the fragment according to continuation marks: rows of the
		// SAME original line (Cont) join without '\n' — the terminal
		// soft-wraps them, so an over-long single line stays one logical
		// line (copy restores it). Rows starting a NEW original line are
		// separated by a hard '\n' — multi-line content copies back as
		// multi-line. Rows followed by a continuation are padded to the
		// full width so the soft-wrap break lands exactly at the visual
		// row boundary; rows ending an original line are NOT padded (no
		// trailing spaces in selections).
		for j, vl := range lines {
			if j > 0 && !vl.Cont {
				sb.WriteString("\n")
			}
			sb.WriteString(vl.Text)
			if j < len(lines)-1 && lines[j+1].Cont {
				// Row followed by a continuation: pad to the full width so
				// the soft-wrap break lands exactly at the visual boundary.
				if wdt := widths[j]; wdt < wb.width {
					sb.WriteString(strings.Repeat(" ", wb.width-wdt))
				}
			} else if j < len(lines)-1 {
				// Row ending an original line: not padded (copy stays free
				// of trailing spaces), but erase the row tail — a shorter
				// row overwriting a longer one from the previous frame
				// would otherwise leave residue.
				sb.WriteString(ansi.EraseLine(0))
			}
		}
		// Clear the fragment's last row to the end of the line: it is not
		// padded (copy fidelity — no trailing spaces in selections), so
		// without this erase the overlay renderer would leave the previous
		// frame's characters on that row's tail (e.g. a short folded line
		// overwriting a longer row from the frame before).
		sb.WriteString(ansi.EraseLine(0))
		firstWritten = true
	}

	// Pad to the viewport height with blank rows. This must happen here —
	// ScrollView cannot count terminal rows (a fragment soft-wraps to
	// several rows), so the padding needs the visual row count. Each blank
	// row is entered with '\n' and then ERASED (EL clears the row we just
	// moved to — an EL before the '\n' would clear the PREVIOUS row and
	// leave the blank row carrying old frame content).
	if pad := wb.viewportHeight - emittedRows; pad > 0 {
		if emittedRows == 0 {
			sb.WriteString(ansi.EraseLine(0))
			sb.WriteString(strings.Repeat("\n"+ansi.EraseLine(0), max(0, pad-1)))
		} else {
			sb.WriteString(strings.Repeat("\n"+ansi.EraseLine(0), pad))
		}
	}
	return sb.String()
}

// windowFragment renders window w if needed and returns its clipped
// visual lines [from,to) plus their display widths. The fold arrow is
// prepended to the first line when the fragment starts at the window's
// first line; isCursor selects the selection color (mirroring
// renderCursorArrow), otherwise the cached dim arrow is reused.
func (wb *WindowBuffer) windowFragment(w *Window, from, to int, isCursor, blocked bool) ([]visualLine, []int) {
	// Ensure the border cache is populated (lineHeights alone don't
	// render folded windows — the fast path skips rendering).
	w.Render(wb.width, false, wb.styles, wb.borderStyle, blocked)
	lines := w.border.lines[from:to]

	// Display widths are computed lazily and cached: Render fills them
	// only when the fragment output needs padding, so the line-counting
	// paths (ensureLineHeights) never pay the per-line measurement cost.
	widths := w.border.widths
	if len(widths) != len(w.border.lines) {
		widths = make([]int, len(w.border.lines))
		for li, ln := range w.border.lines {
			widths[li] = ansi.StringWidth(ln.Text)
		}
		w.border.widths = widths
	}
	widths = widths[from:to]

	// Cursor highlight: recolor the arrow on the window's first visible
	// line (the header), mirroring renderCursorArrow. The dim arrow is
	// cached at render time; the cursor arrow (rare — one window) is
	// rendered on demand. The first row is REPLACED (not mutated in
	// place): lines aliases w.border.lines, so mutating it would prepend
	// another arrow on every render.
	if from == 0 {
		var arrowStr string
		if isCursor {
			color := wb.styles.BorderCursor
			if blocked {
				color = wb.styles.ColorDim
			}
			arrowStr = NewStyle().Foreground(color).Render(w.arrowChar())
		} else {
			arrowStr = w.border.arrow
		}
		first := lines[0]
		lines = append([]visualLine{{Text: arrowStr + first.Text, Cont: first.Cont}}, lines[1:]...)
		widths = append([]int{w.border.arrowWidth + widths[0]}, widths[1:]...)
	}
	return lines, widths
}

// renderAll renders all visible windows
func (wb *WindowBuffer) renderAll(cursorIndex int, blocked bool) string {
	var sb strings.Builder
	firstWritten := false
	for i, w := range wb.windows {
		// Skip non-visible windows entirely
		if !w.Visible {
			continue
		}

		if firstWritten {
			sb.WriteString("\n")
		}
		sb.WriteString(w.Render(wb.width, cursorIndex == i, wb.styles, wb.borderStyle, blocked))
		firstWritten = true
	}
	return sb.String()
}

// findWindowAtLine returns the window index containing the given line.
func (wb *WindowBuffer) findWindowAtLine(line int) int {
	current := 0
	for i, h := range wb.lineHeights {
		if current+h > line {
			return i
		}
		current += h
	}
	return len(wb.windows) - 1
}

// RenderWindowContent renders the content of a window (for testing).
func (wb *WindowBuffer) RenderWindowContent(w *Window, innerWidth int) string {
	if w.renderer == nil {
		return ""
	}
	// Use BuildInner to get the rendered content lines (without border)
	lines, _ := w.renderer.BuildInner(innerWidth, false, wb.styles)
	return joinVisualLines(lines)
}

// parseHistoryID parses a history ID string (from the wire format) to uint64.
// Returns 0 if the string is not a valid number.
func parseHistoryID(id string) uint64 {
	if id == "" {
		return 0
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
