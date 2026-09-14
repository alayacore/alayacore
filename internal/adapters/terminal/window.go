package terminal

// Window is a single display unit: the row that names it and the content
// under it.
//
// Architecture
//
// Window holds fields accessed by WindowBuffer in hot paths (.Visible,
// .Folded, .ID, .HistoryID) and delegates type-specific rendering to
// a WindowRendering interface. This keeps ForEachVisible iteration
// fast (direct field access) while allowing each window type to have
// its own rendering and content management.
//
// Renderers (window_renderer.go):
//   - textRenderer:  assistant text (AT, At), reasoning (AR, Ar), sys msg (SN), sys err (SE)
//   - userRenderer:  user messages with optional media attachments (UT)
//   - toolRenderer:  tool calls and results (AF, Af, UF)
//
// Related files:
//   - window_renderer.go — WindowRendering interface and implementations
//   - window_buffer.go   — WindowBuffer, line tracking, virtual rendering
//   - wrap.go            — wrapContent, wrapVisualLines, appendDeltaToVisualLines

import (
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ToolInfo holds the identifying details of a tool call window.
type ToolInfo struct {
	ID    string
	Name  string
	Input string
}

// WindowRendering handles type-specific rendering and content management.
// Each Window has one renderer; implementations are not shared across windows.
type WindowRendering interface {
	// Tag returns the TLV tag for cursor navigation.
	Tag() string

	// ToolInfo returns tool call details, or nil if not a tool window.
	ToolInfo() *ToolInfo

	// AppendFromTLV processes one incoming TLV frame.
	AppendFromTLV(tag string, value string)

	// BuildInner returns the styled inner content lines and line count.
	// width is the full window width (content wraps at the full width —
	// windows have no side borders or padding).
	// The folded parameter is legacy (folded windows now render via
	// BuildCollapsed); it is always false.
	//
	// The returned lines are the VISUAL content lines (each element is one
	// terminal row, no '\n' inside) with continuation marks (visualLine
	// Cont): rows of the same original line join without '\n' (terminal
	// soft-wrap), rows starting a new original line are separated by hard
	// '\n' — the soft-wrap breakpoints that the viewport clips against
	// (see docs/internal/virtual-rendering-performance.md). lineCount is
	// the window's expanded height (len(lines) + 1): the content rows plus
	// the window's own line above them.
	BuildInner(width int, folded bool, styles *Styles) (lines []visualLine, lineCount int)

	// BuildCollapsed returns the single-line collapsed representation of
	// the window (label + content summary, truncated to fit width), WITHOUT
	// the leading fold marker — Window.Render composes row 0 (marker +
	// this). lineCount is always 1. Long-form text windows summarize the escaped HEAD + "…"
	// + TAIL of the content (see collapsedSummary / headAndTailParts);
	// short system messages use TAIL-only (see tailParts). Tool windows
	// summarize the first input line. Truncation markers are rendered dim.
	BuildCollapsed(width int, styles *Styles) (inner string, lineCount int)

	// Invalidate clears any cached rendering state.
	Invalidate()
}

// renderCache is a window's render cache: the rows it last rendered (row 0,
// the line that names it, then the content), the display widths measured for
// them, the joined string the viewport consumes, and the row variants derived
// on request (the cursor register, the pinned row's annotation).
// This is separate from any internal cache inside the renderer
// (e.g. textRenderer.wrappedLines for streaming optimization); this one is
// keyed on the window's own display inputs — width, fold state, blocked state
// and the receipt time row 0 prints.
//
// rendered is inner: row 0 is the whole of a window's chrome (marker, label,
// timestamp) and every state of it is built here, so nothing is prefixed to
// the cached output later and the two fold states differ only in what row 0
// says. See renderCursor / cursorLine0.
//
// The cursor highlight covers row 0 and nothing else, so a cursor render is
// the cached rows with that one row swapped for its highlighted form
// (line0Cursor).
//
// lines is the same content as inner, but as a VISUAL line array (one
// element per terminal row, no '\n' inside; visualLine.Cont marks rows
// that continue the same original line — soft-wrap — while rows starting
// a new original line are separated by hard '\n'). It is the structure
// the viewport clips against for soft-wrap fragment output (docs/internal/virtual-rendering-performance.md);
// inner/rendered are the '\n'-joined projections kept for the current
// line-based output path.
//
// widths caches display widths (cellWidth) computed once at render
// time, so renderVirtual can pad lines for soft-wrap fragment output
// without re-measuring every line on every view. The marker's width is not
// cached: the glyph is a layout constant one cell wide (arrowCellWidth).
type renderCache struct {
	valid     bool
	width     int
	folded    bool
	blocked   bool         // cached blocked state (different → cache miss)
	createdAt time.Time    // cached Window.CreatedAt (row 0 prints it)
	rendered  string       // full non-cursor output
	inner     string       // rendered, before any cursor row swap
	lines     []visualLine // visual rows, line 0 = the window's own line (marker included)
	widths    []int        // display width per line (parallel to lines)
	lineCount int

	// line0Cursor is row 0 rendered in the cursor's register: a folded
	// line's marker and label column recolored (its content summary keeps
	// the muted color), an expanded line's whole row — marker, label and
	// timestamp — in the selection color. Cursor rendering swaps row 0 for
	// it; the row is the same width as the cached one, so the rest of the
	// window (and all width accounting) is reused as-is.
	//
	// Built on first request, not by Render: the collapsed variant costs a
	// second BuildCollapsed (a full pass over the window's content, ~100µs
	// on a 2 KB message), Render runs for every window on every content
	// change, and exactly one window at a time is under the cursor.
	// line0CursorDone tells "not built yet" from "built and empty".
	line0Cursor     string
	line0CursorDone bool

	// pinnedRow is row 0 with the pinned annotation — "<n> lines above",
	// the count of THIS window's lines hidden above the viewport. It is the
	// one part of the row that depends on where the viewport is, so it
	// cannot live in `lines[0]` (keyed on content, width, theme and the
	// receipt time) and is memoized here instead, the way line0Cursor is
	// memoized on the register: pinnedDone tells "not built yet" from
	// "built", and pinnedAbove/pinnedCursor are the inputs it was built for.
	// Render clears it with the rest of the cache, so a rebuilt row is never
	// annotated with a stale count.
	//
	// The pin is drawn for one window at a time, and the count changes only
	// when the viewport crosses one of that window's rows — so a frame that
	// does not move the viewport (streaming, a status flip, a keystroke
	// elsewhere) re-reads this and pays nothing: see Window.pinnedLine0.
	pinnedRow    string
	pinnedAbove  int
	pinnedCursor bool
	pinnedDone   bool

	// frameStyles is what the cursor row is composed from: the styles the
	// window was rendered with — already dimmed when an overlay is up, so
	// the highlighted row dims with everything else. The styles are stored
	// as they were and the Selected() register is derived on first request,
	// in cursorLine0, so that Render does not pay for a window that is not
	// under the cursor.
	frameStyles *Styles
}

// Window represents a single display window.
//
// Hot-path fields (.Visible, .Folded, .ID, .HistoryID) are struct fields
// for direct access by WindowBuffer. Type-specific behavior is delegated
// to the renderer.
type Window struct {
	ID        string
	HistoryID uint64
	Visible   bool
	Folded    bool
	styles    *Styles

	// CreatedAt is when this window was created — the adapter's own
	// receipt clock, read ONCE at creation and then stored, never read
	// again while rendering (a clock read inside Render would make the
	// output a function of the moment, uncacheable and untestable).
	//
	// It is explicitly NOT a record field: the session file carries no
	// per-message time (only session-level created_at/updated_at), so a
	// replayed message is stamped with when this run received it. The
	// header renders that as "arrival in this view", which is the fact the
	// adapter actually has. Zero means unknown → no timestamp is rendered,
	// which is what a Window built outside the adapter (tests) gets unless
	// it sets one.
	CreatedAt time.Time

	renderer WindowRendering

	// cache holds this window's rendered rows: row 0 (the one that names
	// it), the visual content lines, and the row variants that are derived
	// on request (the cursor register, the pinned row's annotation).
	cache renderCache
}

// NewWindow creates a window with the appropriate renderer for the given tag.
func NewWindow(id string, tag string, styles *Styles) *Window {
	w := &Window{
		ID:        id,
		styles:    styles,
		CreatedAt: time.Now(), // the adapter's receipt clock — see Window.CreatedAt
	}
	w.setRenderer(tag)
	return w
}

// setRenderer sets the renderer based on the TLV tag.
func (w *Window) setRenderer(tag string) {
	switch tag {
	case tlv.TagUserT:
		w.renderer = &userRenderer{}
	case tlv.TagAssistantF, tlv.TagUserF:
		w.renderer = &toolRenderer{isUF: tag == tlv.TagUserF}
	default:
		w.renderer = &textRenderer{tag: tag}
	}
}

// ToolInfo returns tool call details, or nil if not a tool window.
func (w *Window) ToolInfo() *ToolInfo {
	if w.renderer == nil {
		return nil
	}
	return w.renderer.ToolInfo()
}

// Tag returns the TLV tag for cursor navigation.
func (w *Window) Tag() string {
	if w.renderer == nil {
		return ""
	}
	return w.renderer.Tag()
}

// AppendFromTLV processes one incoming TLV frame.
func (w *Window) AppendFromTLV(tag string, value string) {
	if w.renderer == nil {
		return
	}
	w.renderer.AppendFromTLV(tag, value)
	w.cache.valid = false
}

// AppendContent adds content from a non-TLV source (e.g. directly from output.go).
// Used for system messages (SE, SN) that don't go through TLV dispatch.
func (w *Window) AppendContent(content string) {
	if w.renderer == nil {
		return
	}
	w.renderer.AppendFromTLV(w.renderer.Tag(), content)
	w.cache.valid = false
}

// EnsureVisibleContent marks the window visible if it has non-whitespace content.
func (w *Window) EnsureVisibleContent(content string) {
	if !w.Visible && hasVisibleContent(content) {
		w.Visible = true
	}
}

// Invalidate marks the cache as stale.
func (w *Window) Invalidate() {
	w.cache.valid = false
	if w.renderer != nil {
		w.renderer.Invalidate()
	}
}

// SetRendererForTool switches the renderer to toolRenderer (for AF/UF frames).
func (w *Window) SetRendererForTool(name, input string) {
	w.renderer = &toolRenderer{
		name:   name,
		input:  input,
		status: ToolStatusPending,
	}
	w.cache.valid = false
}

// HandleToolInput updates the tool call data on an existing tool window
// or creates a tool renderer if none exists.
func (w *Window) HandleToolInput(data protocol.ToolInputData, historyID uint64) {
	if w.renderer == nil || w.renderer.Tag() != tlv.TagAssistantF {
		w.renderer = &toolRenderer{}
	}
	if tr, ok := w.renderer.(*toolRenderer); ok {
		if data.Name != "" && len(data.Input) == 0 {
			// Start frame — set name, keep existing input
			tr.name = data.Name
			if tr.input == "" {
				tr.input = string(data.Input)
			}
		} else {
			if data.Name != "" {
				tr.name = data.Name
			} else if tr.name == "" {
				// AF frame arrived with empty name. Without a fallback
				// the tool window's BuildCollapsed path treats name=="" as
				// a UF-only window and renders nothing useful.
				tr.name = "_"
			}
			tr.input = string(data.Input)
			// Complete input arrived, clear delta preview.
			tr.deltaBuffer = ""
		}
		if tr.status == ToolStatusNone {
			tr.status = ToolStatusPending
		}
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.cache.valid = false
}

// HandleToolOutput sets the output and status on a tool window.
func (w *Window) HandleToolOutput(output string, isError bool, historyID uint64) {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		tr.output = output
		if isError {
			tr.status = ToolStatusError
		} else {
			tr.status = ToolStatusSuccess
		}
	}
	if historyID > w.HistoryID {
		w.HistoryID = historyID
	}
	w.cache.valid = false
}

// SetHistoryID sets the history ID if the given value is larger.
func (w *Window) SetHistoryID(hid uint64) {
	if hid > w.HistoryID {
		w.HistoryID = hid
	}
}

// RawContent returns the accumulated text content for testing.
func (w *Window) RawContent() string {
	if w.renderer == nil {
		return ""
	}
	switch r := w.renderer.(type) {
	case *textRenderer:
		return r.rawContent()
	case *userRenderer:
		return strings.Join(r.textParts, "\n")
	case *toolRenderer:
		return r.input
	}
	return ""
}

// ToggleMarkdownMode toggles markdown rendering for plain-text
// windows (assistant text AT / reasoning AR). Returns false for windows
// that never render markdown (user prompts, tools, system messages).
func (w *Window) ToggleMarkdownMode() bool {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return false
	}
	tr.ToggleMarkdownMode()
	w.cache.valid = false
	return true
}

// MarkdownMode reports whether the window renders markdown.
func (w *Window) MarkdownMode() bool {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return false
	}
	return tr.mdMode
}

// SetMarkdownDefault sets the initial markdown rendering state for
// plain-text windows (assistant text AT / reasoning AR). No-op for other
// window types. Existing state is overwritten (used at window creation).
func (w *Window) SetMarkdownDefault(on bool) {
	tr, ok := w.renderer.(*textRenderer)
	if !ok || !tr.plainContent() {
		return
	}
	tr.mdMode = on
	w.cache.valid = false
}

// RawStatus returns the tool status for testing.
func (w *Window) RawStatus() ToolStatus {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.status
	}
	return ToolStatusNone
}

// RawToolName returns the tool name for testing.
func (w *Window) RawToolName() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.name
	}
	return ""
}

// RawTag returns the TLV tag for testing.
func (w *Window) RawTag() string {
	return w.Tag()
}

// RawDelta returns the current tool delta buffer for testing.
// Returns empty string for non-tool windows.
func (w *Window) RawDelta() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.deltaBuffer
	}
	return ""
}

// Render returns the window, using cache if valid.
// When blocked is true, the content is rendered with dimmed colors.
//
// Two visual states, one shape: both are a single "window line" that starts
// with the fold marker and the label column, and what follows the label is
// what tells them apart.
//   - Folded: marker "+", label, content summary — one line, no content.
//   - Expanded: marker "-", label, the arrival timestamp right-aligned —
//     then the content, one row per wrapped line. No rule above or below:
//     a window is opened by this line and closed by the next window's own
//     (the prompt box closes the last one).
//
// The cursor highlight recolors row 0 — marker and label (and, on an
// expanded line, the timestamp), never a folded line's content summary.
// The markers are layout constants, not theme values (constants.go).
func (w *Window) Render(width int, isCursor bool, styles *Styles, blocked bool) string {
	if w.renderer == nil {
		return ""
	}

	// Validate cache. createdAt is part of the key: row 0 prints it, and a
	// window whose receipt time is set after its first render (tests, and
	// anything that stamps a window it did not construct) must not keep a
	// stale header. Equal rather than == — time.Time carries a monotonic
	// reading and a location pointer, and only the instant matters here.
	if w.cache.valid && w.cache.width == width && w.cache.folded == w.Folded &&
		w.cache.blocked == blocked && w.cache.createdAt.Equal(w.CreatedAt) {
		if isCursor {
			return w.renderCursor()
		}
		return w.cache.rendered
	}

	// Invalidate renderer cache when blocked state changes, so BuildInner
	// does a full re-styled render with the new (dimmed or normal) styles.
	if w.cache.valid && w.cache.blocked != blocked && w.renderer != nil {
		w.renderer.Invalidate()
	}

	// Use dimmed styles when blocked (Dimmed() maps every color the window
	// line draws with — including the prompt and error colors — to the dim
	// color, so the line dims with the rest of the chrome).
	if blocked {
		styles = styles.Dimmed()
	}

	w.cache.line0Cursor = ""
	w.cache.line0CursorDone = false
	w.cache.pinnedRow = ""
	w.cache.pinnedDone = false
	w.cache.frameStyles = styles
	if w.Folded {
		// Collapsed: one line — marker + label + content summary. The
		// marker is part of the row (not prefixed later by the viewport
		// layer): both states' first rows are built here, so the cursor
		// render has exactly one row to swap and every width is accounted
		// for in one place.
		//
		// BuildCollapsed does no wrapping: only the summary (escaped tail
		// for text windows, first line for tool windows) is read and
		// truncated, so folding a large window is O(1).
		inner, _ := w.renderer.BuildCollapsed(width, styles)
		w.cache.lines = []visualLine{{Text: w.lineStyle(styles).Render(w.markerChar()) + " " + inner}}
		w.cache.widths = nil // computed lazily by renderVirtual (fragment output)
		w.cache.inner = w.cache.lines[0].Text
		w.cache.rendered = w.cache.inner
		w.cache.lineCount = 1
	} else {
		// Expanded: the window's own line — marker + label, the timestamp
		// on the right — then the content. There is no rule and no closing
		// line: a window is opened by its marker row, and the next window
		// opens with its own. BuildInner returns the visual content lines
		// (soft-wrap breakpoints); the whole window is one flat visual line
		// array, so cache.rendered == cache.inner.
		contentLines, _ := w.renderer.BuildInner(width, false, styles)
		lines := make([]visualLine, 0, len(contentLines)+1)
		lines = append(lines, visualLine{Text: w.buildExpandHeader(width, styles, "")})
		lines = append(lines, contentLines...)
		w.cache.lines = lines
		w.cache.widths = nil // computed lazily by renderVirtual (fragment output)
		w.cache.inner = joinVisualLines(lines)
		w.cache.rendered = w.cache.inner
		w.cache.lineCount = len(lines)
	}

	w.cache.width = width
	w.cache.folded = w.Folded
	w.cache.blocked = blocked
	w.cache.createdAt = w.CreatedAt
	w.cache.valid = true

	if isCursor {
		return w.renderCursor()
	}
	return w.cache.rendered
}

// markerChar returns the window's fold marker glyph: "+" while folded
// (there is more), "-" while expanded (its content follows). Both states
// draw one, at column 0, so the label column never moves when a window
// folds — and the marker is the row's only structural glyph, which is why
// it is ASCII (constants.go).
func (w *Window) markerChar() string {
	if w.Folded {
		return foldArrow
	}
	return unfoldArrow
}

// lineStyle returns the style every glyph of this window's own line is
// drawn in — see lineStyleForTag, which owns the per-type colors.
func (w *Window) lineStyle(styles *Styles) Style {
	return lineStyleForTag(w.Tag(), styles)
}

// cursorLine0 returns row 0 in the cursor's register — the marker and the
// label column recolored, the content summary behind them left alone —
// building it on first request and caching it in the render cache.
//
// It is not built by Render on purpose: the collapsed variant costs a
// second BuildCollapsed (a full pass over the window's content, ~100µs on
// a 2 KB message), Render runs for every window on every content change,
// and exactly one window at a time is under the cursor. Memoized, the cost
// is paid once per cache generation by that one window.
//
// The register comes from Styles.Selected() rather than from a flag pushed
// through the renderers: the label color is part of the styles a renderer
// already paints from, so recoloring it is a styles swap and nothing else.
func (w *Window) cursorLine0() string {
	if w.cache.line0CursorDone {
		return w.cache.line0Cursor
	}
	w.cache.line0CursorDone = true
	// Selected() swaps every color that names a window (label, prompt,
	// error) for the selection color, so the line — marker, label, tool
	// name, timestamp — comes out highlighted as one unit with no other
	// change.
	styles := w.cache.frameStyles.Selected()
	if w.Folded {
		// The summary is content and keeps its muted color; the marker and
		// the label column take the selection color, so the two read as one
		// highlighted unit.
		inner, _ := w.renderer.BuildCollapsed(w.cache.width, styles)
		w.cache.line0Cursor = w.lineStyle(styles).Render(w.markerChar()) + " " + inner
		return w.cache.line0Cursor
	}
	w.cache.line0Cursor = w.buildExpandHeader(w.cache.width, styles, "")
	return w.cache.line0Cursor
}

// pinnedLine0 returns row 0 for the pinned case: the row above, with
// "<linesAbove> lines above" added left of the timestamp — how much of THIS
// window the reader has scrolled past.
//
// linesAbove is winStart-relative: the pinned row is the window's own line,
// and the body rows between it and the fragment's first row are exactly the
// ones the pin did not draw (see renderVirtual). The count is the window's
// own, never the transcript's: it says "there are 40 more lines of this
// message above you", which is the question the pin raises — the pin says
// whose message this is, this says where in it you are.
//
// Memoized on (linesAbove, register) in the render cache, and cleared by
// Render with the rest of it. The annotation is viewport-dependent, which is
// exactly why it is not part of `lines[0]`: that row is cached across
// scrolls, and a count baked into it would be stale the moment the viewport
// moved. Here the one window under the pin rebuilds its row when the count
// changes (a scroll step that crosses one of its rows) and the frame reuses
// it otherwise (streaming, a keystroke elsewhere), where the cost is one
// comparison — no render, no measure, no allocation.
func (w *Window) pinnedLine0(linesAbove int, isCursor bool) string {
	if w.cache.pinnedDone && w.cache.pinnedAbove == linesAbove && w.cache.pinnedCursor == isCursor {
		return w.cache.pinnedRow
	}
	styles := w.cache.frameStyles
	if isCursor {
		styles = styles.Selected()
	}
	w.cache.pinnedRow = w.buildExpandHeader(w.cache.width, styles, lineCountText(linesAbove, "above"))
	w.cache.pinnedAbove = linesAbove
	w.cache.pinnedCursor = isCursor
	w.cache.pinnedDone = true
	return w.cache.pinnedRow
}

// renderCursor renders the cached window with the cursor's selection
// highlight. Row 0 is the whole of it, in both fold states, and it is the
// same width either way — so no width or line-count accounting changes and
// the rest of the cached output is reused as-is.
//
// Every input it needs — the highlighted row, and the color the marker is
// painted with (selection, or dim under an overlay) — was resolved when the
// cache was filled, and the cache is only valid for the blocked state it
// was filled with (Render's cache key includes it), so the overlay case
// needs no flag here.
func (w *Window) renderCursor() string {
	return replaceFirstLine(w.cache.inner, w.cursorLine0())
}

// replaceFirstLine returns s with its first line replaced by first. Used
// to swap the cursor's highlighted row into a cached window render.
func replaceFirstLine(s, first string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return first + s[i:]
	}
	return first
}

// windowLabel returns the header label for the window type, e.g.
// "TOOL CALL edit_file", "REASONING", "ASSISTANT", "USER PROMPT",
// "SYSTEM NOTIFY", "SYSTEM ERROR".
func (w *Window) windowLabel() string {
	switch w.Tag() {
	case tlv.TagAssistantF, tlv.TagUserF:
		if ti := w.ToolInfo(); ti != nil && ti.Name != "" {
			return toolHeaderLabel + " " + ti.Name
		}
		return toolHeaderLabel
	case tlv.TagAssistantR:
		return "REASONING"
	case tlv.TagAssistantT:
		return "ASSISTANT"
	case tlv.TagUserT:
		return "USER PROMPT"
	case TagWindowSN:
		return "SYSTEM NOTIFY"
	case TagWindowSE:
		return "SYSTEM ERROR"
	default:
		return ""
	}
}

// buildExpandHeader returns the expanded window's first row: the marker,
// the window's label, and the timestamp right-aligned to the window edge —
//
//   - REASONING                              2026/09/14 16:32:07 +08:00
//
// The label starts at the same cell as on a collapsed line (marker + one
// space), so folding a window never moves its label. What differs between
// the two states is the marker glyph and what follows the label: a content
// summary when folded, the timestamp when expanded.
//
// markerColor paints the marker and the timestamp — the row's own chrome,
// in one color (dim by default, the accent for a user prompt, the
// selection color under the cursor) — while styles paints the label. The
// two differ only in the cursor's register, where borderStyle carries the
// selection color and styles is Styles.Selected(), so the whole row is
// highlighted (see Window.cursorLine0).
//
// Widths, in priority order: the marker and the label always render (the
// label is what identifies the window); then the timestamp, then the
// annotation — both are metadata, and metadata yields to the name. Each is
// drawn only when it fits beside what has priority over it with at least one
// space between them, and neither is ever truncated or shortened to fit. A
// label too wide for the row is cut with "…" in the label's own style: the
// tool header's per-segment styling is not worth reassembling across a cut,
// and a truncated tool header only happens on a terminal narrower than the
// tool name.
//
// annotation is the pinned row's "<n> lines above" (see pinnedLine0) or ""
// for every other row; an empty annotation changes nothing.
func (w *Window) buildExpandHeader(width int, styles *Styles, annotation string) string {
	if width <= 0 {
		return ""
	}
	lineStyle := w.lineStyle(styles)
	marker := lineStyle.Render(w.markerChar())
	plain, styled := w.expandTitle(styles)
	if plain == "" {
		// A window type with no label (see expandTitle): the marker stands
		// alone, and there is nothing for a timestamp to be right-aligned
		// against.
		return takeCells(marker, width)
	}

	// The annotation is a separate field from the timestamp, and this UI
	// separates fields on a chrome row with " | " — the status bar
	// (renderStatusSegments) and the help bars do exactly that, and the
	// glyph policy lists that ASCII "|" with them. So the block costs its
	// own cells plus the bar and the two spaces around it: 3, not 1.
	//
	// The bar is painted dim, the way the status bar paints its segment
	// separators, so the count and the timestamp read as two fields rather
	// than one run. The two fields keep the row's own style — and so take
	// the selection color under the cursor — while the bar stays in the
	// dim register: Selected() leaves ColorDim alone, exactly as it leaves
	// the content colors alone.
	annBlock := ""
	if annotation != "" {
		annBlock = annotation + " | "
	}
	annCells := cellWidth(annBlock)
	sepStyle := lineStyle
	if styles != nil {
		sepStyle = lineStyle.Foreground(styles.ColorDim)
	}

	budget := width - collapsedPrefixWidth // cells left for label + gap + time
	if budget < 1 {
		return takeCells(marker, width)
	}
	// The metadata that can follow the label, in the order it yields: the
	// timestamp with the annotation, the timestamp alone, nothing. An
	// annotation that does not fit is dropped without taking the timestamp
	// with it — a narrow pinned row must still say when the message
	// arrived.
	fits := func(extra int) bool {
		return !w.CreatedAt.IsZero() && cellWidth(plain)+extra <= budget-1-timeStampWidth
	}
	switch {
	case annotation != "" && fits(annCells):
		gap := budget - cellWidth(plain) - annCells - timeStampWidth
		return marker + " " + styled + strings.Repeat(" ", gap) +
			lineStyle.Render(annotation) + " " + sepStyle.Render("|") + " " +
			lineStyle.Render(w.timeStamp())
	case fits(0):
		gap := budget - cellWidth(plain) - timeStampWidth
		return marker + " " + styled + strings.Repeat(" ", gap) + lineStyle.Render(w.timeStamp())
	}
	if cellWidth(plain) > budget {
		plain = takeCells(plain, budget-1) + "…"
		styled = lineStyle.Render(plain)
	}
	return marker + " " + styled
}

// timeStamp returns the window's arrival time — when the adapter created
// this view of the message — formatted for the header's fixed-width
// column. See timeStampLayout (constants.go) for the format, the width and
// why it is local time rather than the UTC the session records use.
func (w *Window) timeStamp() string {
	return w.CreatedAt.Local().Format(timeStampLayout)
}

// expandTitle returns an expanded window's label in two forms: the plain
// text (for the row's width accounting) and the styled rendering.
//
// Tool windows keep the collapsed line's layout — bold "TOOL CALL" + a space
// + the status indicator in the fixed label column, then the tool name:
// "- TOOL CALL ⠋    execute_command". The label and indicator share one
// color so they read as a unit, and the bold name is the semantic payload.
// Other windows use their plain label ("ASSISTANT", "SYSTEM NOTIFY",
// "USER PROMPT", …).
//
// The name takes toolNameStyle, the same style the collapsed row paints it
// with, so folding a window leaves it alone; everything else on the row
// takes lineStyleForTag's style.
//
// The colors come from styles, so a caller wanting the cursor's register
// passes Styles.Selected() and gets a highlighted label — and, for tool
// windows, a highlighted status indicator with it (statusDot inherits the
// label color). The name is not highlighted in either state.
func (w *Window) expandTitle(styles *Styles) (plain, styled string) {
	labelStyle := lineStyleForTag(w.Tag(), styles)
	if tr, ok := w.renderer.(*toolRenderer); ok && tr.name != "" {
		dot, dotStyle := tr.status.statusDot(labelStyle)
		label := padLabel(toolLabelWithIndicator(dot))
		var sb strings.Builder
		sb.WriteString(labelStyle.Render(label[:len(toolHeaderLabel)]))
		// Separator space between the label and the status indicator.
		sb.WriteString(label[len(toolHeaderLabel) : len(toolHeaderLabel)+len(toolLabelSep)])
		sb.WriteString(dotStyle.Render(dot))
		// Label-column padding — the indicator is multi-byte UTF-8, so skip
		// len(dot) bytes (not 1) after the label + separator.
		sb.WriteString(label[len(toolHeaderLabel)+len(toolLabelSep)+len(dot):])
		sb.WriteString(toolNameStyle(styles).Render(tr.name))
		return label + tr.name, sb.String()
	}
	label := w.windowLabel()
	if label == "" {
		return "", ""
	}
	return label, labelStyle.Render(label)
}

// lineStyleForTag returns the ONE style the chrome of a window's own line is
// drawn in: the fold marker, the label and the arrival timestamp all take it,
// so what names a window reads as a single unit instead of three
// differently-weighted pieces. Bold throughout — a line that names a window
// is chrome and is meant to be scannable.
//
// A tool window's name is the exception, and it is not this function's
// business: it takes toolNameStyle in the collapsed row and in the expanded
// one, so that folding a window repaints nothing (see toolNameStyle).
//
// The color is the label color for EVERY window type except the system
// errors. A user's turn, a reasoning step, an answer and a tool call are all
// the same kind of thing — conversation — and nothing about a user prompt
// earns its line an accent: the accent belongs to the prompt box at the
// bottom of the screen, which is the one live surface. SYSTEM ERROR keeps
// the error color, because an error has to be recognizable at a glance and
// from the far end of a scrollback.
//
// This is also the function the renderers call for their label segment
// (collapsed lines and the expanded line alike), so both states are
// guaranteed to agree.
//
// The cursor's register is simply Styles.Selected() — the same styles with
// these colors swapped for the selection color — so "who is the cursor"
// needs no parameter here.
func lineStyleForTag(tag string, styles *Styles) Style {
	if styles == nil {
		return NewStyle().Bold(true)
	}
	if tag == TagWindowSE {
		return styles.Error.Bold(true)
	}
	return styles.Label.Bold(true)
}

// toolNameStyle is the style a tool window's name is drawn in — in BOTH fold
// states, which is the reason it exists as a named function rather than as
// this expression written twice.
//
// The name is the payload of a tool row, not chrome, so it is deliberately
// NOT lineStyleForTag's style: the highlight recolors the row's chrome (the
// marker, the label, the timestamp) and leaves the name and the arguments in
// the content color. That is the same reading the window body gets — the
// cursor covers the line that names a window, never what the window says.
//
// Writing the expression out at both call sites is what let folded and
// expanded drift apart: the collapsed row painted the name with the line's
// style and the expanded row with this one, and because Label and
// ToolContent are both muted the two only differed under the cursor — where
// the name turned to the selection color when folded and stayed muted when
// expanded. Folding a window repainted the one token the reader was looking
// at. One function, called from both rows, is what keeps them equal.
//
// Bold, like the line: the name has to hold its own against the muted
// arguments that follow it.
func toolNameStyle(styles *Styles) Style {
	if styles == nil {
		return NewStyle().Bold(true)
	}
	return styles.ToolContent.Bold(true)
}

// LineCount returns the cached line count (valid after Render).
func (w *Window) LineCount() int {
	return w.cache.lineCount
}

// UpdateLineCountFast attempts to compute the line count without a full render.
// Returns (lineCount, ok). If ok is false, the caller must call Render().
//
// Folded windows are always a single line, so this returns immediately
// without touching the renderer — during streaming, deltas to folded
// windows no longer trigger any wrapping or rendering for line tracking.
// This is the main performance win of the collapsed-line design.
func (w *Window) UpdateLineCountFast(width int) (int, bool) {
	if w.renderer == nil {
		return 0, false
	}
	if w.Folded {
		return 1, true
	}
	// Unfolded: try the renderer's internal cache. This fast path
	// (~58μs) only applies when the renderer's internal cache is still
	// valid (e.g. after resize or theme change, not after content append).
	// During streaming, every append invalidates the cache, so this
	// returns false and ensureLineHeights falls through to the full
	// Render (~100-200μs).
	return w.renderLineCountFromCache(width)
}

// renderLineCountFromCache tries to get line count from the renderer's cache.
func (w *Window) renderLineCountFromCache(width int) (int, bool) {
	// Check if renderer supports fast line count
	type lineCounter interface {
		TryLineCount(width int) (int, bool)
	}
	if lc, ok := w.renderer.(lineCounter); ok {
		return lc.TryLineCount(width)
	}
	return 0, false
}

// hasVisibleContent returns true if content has at least one non-whitespace character.
func hasVisibleContent(content string) bool {
	for _, r := range content {
		if !isWhitespace(r) {
			return true
		}
	}
	return false
}

// isWhitespace returns true if the character is whitespace.
func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
