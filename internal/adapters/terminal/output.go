package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text (with the
// self-built style layer or any ANSI styling), each segment must be
// rendered individually before concatenation. You cannot render a string
// that already contains ANSI codes with a new style and expect it to work
// - the outer styling will not wrap the inner styled segments. Always
// render segments separately, then join them.

// LOCK ORDERING:
//
// Three mutexes exist, but they are NEVER nested across goroutines:
//
//   outputWriter.mu  — protects the frame decoder and processBuffer() call chain
//   WindowBuffer.mu  — protects window data (windows slice, line heights)
//   sessionState.mu  — protects status/model/queue snapshot fields
//
// Session goroutine call paths (inside outputWriter.mu):
//   outputWriter.mu → WindowBuffer.mu      (content tags, exclusive with sessionState)
//   outputWriter.mu → sessionState.mu      (TagSystemMsg, exclusive with WindowBuffer)
//
// UI (event loop) goroutine call paths:
//   outputWriter.mu → WindowBuffer.mu      (FlushPendingDeltas, exclusive with sessionState)
//   WindowBuffer.mu  (via display update, tick — never nested with sessionState)
//   sessionState.mu  (via snapshot methods — never nested with WindowBuffer)
//
// WindowBuffer.mu and sessionState.mu are NEVER held simultaneously — they
// are acquired and released in separate, sequential calls. No deadlock is
// possible since each goroutine only nests one additional lock under its
// "root" lock (or no nesting at all for the UI goroutine).

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/wire"
)

// outputWriter parses frame from the session and writes styled content to the WindowBuffer.
// It implements io.Writer for the agent/session output stream.
type outputWriter struct {
	windowBuffer *WindowBuffer
	dec          wire.Decoder
	mu           sync.Mutex // protects dec and processBuffer
	dirty        atomic.Bool
	styles       atomic.Pointer[Styles]
	nextWindowID atomic.Int64
	status       sessionState // cached session state (status, models, queue)

	// Active user window — set on first user frame (UT/UI/UV/UA/UD),
	// cleared on next non-user tag. Each new frame updates the
	// window incrementally and marks dirty for immediate render.
	activeUserWindowID string // window ID; "" = none
	pendingUserMaxID   uint64 // max history ID across all parts

	// Pending delta coalescing — accumulates "At"/"Ar" and "Af" frames
	// and flushes them to WindowBuffer in batches, reducing lock contention
	// and WindowBuffer churn during high-speed streaming.
	pendingTextDeltas map[string]*pendingTextDelta // key: windowID (historyID)
	pendingToolDeltas map[string]*pendingToolDelta // key: toolCallID
}

// pendingTextDelta accumulates "At" / "Ar" frames for the same window.
// Flushed as a single AppendOrUpdate call.
type pendingTextDelta struct {
	content    strings.Builder
	historyID  uint64
	displayTag string // "AT" or "AR" — used only if window doesn't exist yet
}

// pendingToolDelta accumulates "Af" frames for the same tool call.
// Flushed as a single HandleToolInputDelta call.
type pendingToolDelta struct {
	content   strings.Builder
	historyID uint64
	toolName  string
}

func NewTerminalOutput(styles *Styles) *outputWriter { //nolint:revive // tests need access to internal methods
	to := &outputWriter{
		windowBuffer:      NewWindowBuffer(DefaultWidth, styles),
		status:            sessionState{mu: &sync.Mutex{}},
		pendingTextDeltas: make(map[string]*pendingTextDelta),
		pendingToolDeltas: make(map[string]*pendingToolDelta),
	}
	to.styles.Store(styles)
	return to
}

// SetStyles updates the styles for the output writer
func (to *outputWriter) WithStyles(styles *Styles) {
	to.styles.Store(styles)
	to.windowBuffer.WithStyles(styles)
}

func (to *outputWriter) Write(p []byte) (n int, err error) {
	to.mu.Lock()
	to.dec.Feed(p)
	fatal := to.processBuffer()
	if fatal != nil {
		// The stream is finished, and the display still works: say so in the UI
		// before handing the error back, because a session canceled by a write
		// error reports nothing else. WriteError is called under to.mu, which is
		// the documented order (outputWriter.mu → WindowBuffer.mu).
		to.WriteError(fmt.Sprintf("frame error: %v", fatal))
	}
	to.mu.Unlock()
	if fatal != nil {
		return 0, fatal
	}
	return len(p), nil
}

// WriteError adds an error message to the display buffer with error styling
// (applied during render by the window renderer).
func (to *outputWriter) WriteError(msg string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSE, id, msg)
	to.dirty.Store(true)
}

// WriteNotify writes a notification message to the display.
// Styling is stored raw — it's applied during render by the window renderer.
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, msg)
	to.dirty.Store(true)
}

// processBuffer writes out every complete frame in the decoder and returns the
// error that finished the stream, if any. Must be called with to.mu held.
func (to *outputWriter) processBuffer() error {
	for {
		tag, value, err := to.dec.Next()
		if err != nil {
			if errors.Is(err, wire.ErrIncomplete) {
				return nil
			}
			// A malformed frame ends the stream: the decoder never recovers, so
			// every later frame would report the same error. The caller stops the
			// session with it rather than pretending the output continued.
			return err
		}
		to.writeColored(tag, value)
	}
}

// writeColored writes styled content based on the frame tag
//
//nolint:gocyclo // dispatch over many tag types; each case is simple
func (to *outputWriter) writeColored(tag string, value string) {
	to.triggerUpdateForTag(tag)

	// Flush any pending deltas before processing a non-delta frame.
	// This ensures the WindowBuffer reflects the latest accumulated content
	// before authoritative frames (AT, AR, AF, UF, etc.) arrive. Both
	// accumulating and write-through deltas skip the flush — flushing
	// mid-stream would corrupt pending state.
	if !isAccumulatingDelta(tag) && !isWriteThroughDelta(tag) {
		to.flushPendingDeltas()
	}

	// Flush pending user content before any non-user tag.
	if to.activeUserWindowID != "" && !userTag(tag) {
		to.flushUserContent()
	}

	switch tag {
	// Streaming delta frames — incremental content appended to existing window.
	// Instead of writing directly to WindowBuffer, accumulate for coalescing.
	// Streaming content delta — incremental content appended to an existing
	// window. Instead of writing directly to WindowBuffer, accumulate for
	// coalescing. The content kind (text vs reasoning) rides in the JSON.
	case wire.TagAssistantXDelta:
		d, ok := protocol.DecodeContent(value)
		if !ok {
			return
		}
		displayTag, ok := contentDisplayTag(wire.TagAssistantX, d.Type)
		if !ok {
			return
		}
		to.pendTextDelta(d.ID, displayTag, d.Text)

	// Complete authoritative content — already delivered via deltas during
	// live streaming, but carries the authoritative content during replay.
	// Only display if no window exists yet (replay case).
	case wire.TagAssistantX:
		d, ok := protocol.DecodeContent(value)
		if !ok {
			return
		}
		displayTag, ok := contentDisplayTag(wire.TagAssistantX, d.Type)
		if !ok {
			return
		}
		if !to.windowBuffer.HasWindow(d.ID) {
			to.windowBuffer.AppendOrUpdate(displayTag, d.ID, d.Text)
		}
		to.applyWireTime(d.ID, d.Time)

	// User content — accumulated until the next non-user tag triggers flush.
	case wire.TagUserX:
		d, ok := protocol.DecodeContent(value)
		if !ok {
			return
		}
		displayTag, ok := contentDisplayTag(wire.TagUserX, d.Type)
		if !ok {
			return
		}
		if d.Type == protocol.TypeText {
			to.bufferUserContent(d.ID, d.Text, displayTag)
		} else {
			to.bufferUserContent(d.ID, mediaLabel(displayTag), displayTag)
		}
		to.applyWireTime(to.activeUserWindowID, d.Time)

	case wire.TagUserEnd:
		// Flush any pending user content. TagUserEnd should only appear
		// on the input stream, but handle it here as a safety measure.
		to.flushUserContent()

	// Function lifecycle (JSON: call_id, name, input). The history id rides in
	// the body's "id"; call_id pairs the call with its result.
	case wire.TagAssistantF:
		var fd protocol.ToolInputData
		if err := json.Unmarshal([]byte(value), &fd); err != nil {
			return
		}

		// Resolve the tool name — prefer the existing window (input frame)
		// over the frame itself (start/combined).
		name := fd.Name
		if info := to.windowBuffer.GetFunctionInfo(fd.CallID); info != nil {
			name = info.Name
		}

		// Format the input for display.
		if len(fd.Input) == 0 {
			fd.Input = json.RawMessage(name + ": \n")
		} else {
			handler := GetHandler(name)
			fd.Input = json.RawMessage(handler.FormatCall(fd.Input))
		}

		to.windowBuffer.HandleToolInputEvent(fd, parseHistoryID(fd.ID))
		to.applyWireTime(fd.CallID, fd.Time)

	// Tool input delta — partial JSON arguments during streaming.
	case wire.TagAssistantFDelta:
		var fd protocol.ToolInputDeltaData
		if err := json.Unmarshal([]byte(value), &fd); err != nil {
			return
		}
		// Resolve tool name from existing window if known.
		name := ""
		if info := to.windowBuffer.GetFunctionInfo(fd.CallID); info != nil {
			name = info.Name
		}
		to.pendToolDelta(fd.CallID, name, fd.Delta, parseHistoryID(fd.ID))

	// Tool result preview snapshot (JSON: call_id, text)
	// Ephemeral display-only frame; authoritative result arrives via UF.
	case wire.TagUserFDelta:
		var od protocol.ToolOutputDeltaData
		if err := json.Unmarshal([]byte(value), &od); err != nil {
			return
		}
		to.windowBuffer.HandleToolOutputDelta(od.CallID, od.Text, parseHistoryID(od.ID))

	// Function result (JSON: call_id, output, is_error)
	case wire.TagUserF:
		var tr protocol.ToolOutputData
		if err := json.Unmarshal([]byte(value), &tr); err != nil {
			return
		}
		// Extract display text from the content item list.
		displayText := extractToolOutputDisplayText(tr.Output)
		to.windowBuffer.HandleToolOutput(tr.CallID, displayText, tr.IsError, parseHistoryID(tr.ID))
		to.applyWireTime(tr.CallID, tr.Time)

	// System tags
	case wire.TagSystemMsg:
		to.handleSystemFrame(value)
		return

	// Command output (CO) — command result frames
	case wire.TagCommandOut:
		to.handleCommandOut(value)
		return

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(tag, id, value)
	}
}

// triggerUpdateForTag marks the display as dirty for tags that modify the display.
//
// All tags that mutate the WindowBuffer (authoritative + accumulating +
// write-through deltas) set dirty. The split between isAccumulatingDelta
// and isWriteThroughDelta captures the *flush* semantics (whether flush-
// PendingDeltas should fire on arrival); triggerUpdateForTag captures the
// *redraw* semantics independently.
func (to *outputWriter) triggerUpdateForTag(tag string) {
	if tagsThatSetDirty[tag] {
		to.dirty.Store(true)
	}
}

// isAccumulatingDelta reports whether a frame is an accumulating delta
// (Ax/Af). These tags accumulate content in pending maps instead of
// touching the window directly, so they do not trigger flushPendingDeltas
// on arrival — flushing mid-stream would interleave authoritative frames
// incorrectly.
func isAccumulatingDelta(tag string) bool {
	switch tag {
	case wire.TagAssistantXDelta, wire.TagAssistantFDelta:
		return true
	}
	return false
}

// isWriteThroughDelta reports whether a frame is a write-through delta
// (Uf). It updates the window directly (snapshot semantics, no coalescing)
// and also does not trigger flushPendingDeltas on arrival.
func isWriteThroughDelta(tag string) bool {
	return tag == wire.TagUserFDelta
}

// tagsThatSetDirty marks tags whose arrival requires a redraw. Ax/Af
// are NOT in this set: they accumulate pending data without modifying
// WindowBuffer, and the dirty flag is set by flushPendingDeltas() when
// those pending deltas are eventually written out. Uf IS in the set
// because it updates the window directly (snapshot semantics).
var tagsThatSetDirty = map[string]bool{
	wire.TagAssistantX: true,
	wire.TagUserX:      true,
	wire.TagAssistantF: true,
	wire.TagUserF:      true,
	wire.TagUserFDelta: true,
}

// pendTextDelta accumulates a text delta ("Ax") for coalescing.
// Multiple deltas for the same window are merged into a single
// AppendOrUpdate call when flushPendingDeltas is called.
// Must be called under outputWriter.mu.
func (to *outputWriter) pendTextDelta(id, displayTag, content string) {
	pd, ok := to.pendingTextDeltas[id]
	if !ok {
		pd = &pendingTextDelta{displayTag: displayTag}
		to.pendingTextDeltas[id] = pd
	}
	pd.content.WriteString(content)
	// Track max history ID from the window ID string.
	if hid, err := strconv.ParseUint(id, 10, 64); err == nil && hid > pd.historyID {
		pd.historyID = hid
	}
}

// pendToolDelta accumulates a tool input delta ("Af") for coalescing.
// Multiple JSON argument fragments for the same tool call are merged
// into a single HandleToolInputDelta call.
// Must be called under outputWriter.mu.
func (to *outputWriter) pendToolDelta(toolID, toolName, delta string, historyID uint64) {
	pd, ok := to.pendingToolDeltas[toolID]
	if !ok {
		pd = &pendingToolDelta{toolName: toolName}
		to.pendingToolDeltas[toolID] = pd
	}
	pd.content.WriteString(delta)
	if historyID > pd.historyID {
		pd.historyID = historyID
	}
}

// flushPendingDeltas writes all accumulated delta frames to the WindowBuffer
// as single batched calls. This reduces WindowBuffer.mu lock acquisitions,
// markDirty calls, and incremental render updates during high-speed streaming.
// Must be called under outputWriter.mu.
func (to *outputWriter) flushPendingDeltas() {
	if len(to.pendingTextDeltas) == 0 && len(to.pendingToolDeltas) == 0 {
		return
	}

	// Flush in historyID order, not map order.
	//
	// A window's display position is fixed at creation: AppendOrUpdate only
	// ever appends, and HistoryID is stored for navigation, never consulted
	// for ordering. Text/reasoning deltas accumulate in pendingTextDeltas
	// keyed by historyID and are written out together, so when a reasoning
	// window and an assistant text window are pending in the same flush their
	// relative order used to be decided by Go's randomized map iteration —
	// REASONING could land below the ASSISTANT window it preceded, for the
	// rest of the session. Flushing sorted by historyID removes the coin flip.
	//
	// Note what this does and does not decide. historyIDs are handed out on
	// first touch of each block key, and the first touch is always a delta:
	// --no-delta only suppresses the frames sent to *this* adapter, the
	// response still streams, so numbering follows delta arrival in every mode
	// and this sort merely emits, deterministically, the order the stream
	// already established. It is not an independent source of ordering, and it
	// must not be read as "reasoning always renders above text": a provider
	// that really streams its answer before its reasoning numbers the answer
	// lower and renders it first.
	//
	// Providers must still emit their complete events in content-array order,
	// but for what those events are here for — display, not numbering. In
	// --no-delta no Ax frame ever reaches this writer, so nothing is pending and
	// flushPendingDeltas does not run: the authoritative AX frames create the
	// AT/AR windows themselves, in the order they arrive. Out of
	// array order, that put ASSISTANT above its own REASONING. Pinned by
	// openai_event_order_test.go, openai_history_id_order_test.go, and
	// TestStreamNumbersBlocksAtDeltasEvenWithNoDeltaCallbacks for the
	// numbering half (docs/providers.md → "Complete-event order").
	type pendingFlush struct {
		id        string
		historyID uint64
		isTool    bool
	}

	flushed := make([]pendingFlush, 0, len(to.pendingTextDeltas)+len(to.pendingToolDeltas))
	for id, pd := range to.pendingTextDeltas {
		flushed = append(flushed, pendingFlush{id: id, historyID: pd.historyID})
	}
	for id, pd := range to.pendingToolDeltas {
		flushed = append(flushed, pendingFlush{id: id, historyID: pd.historyID, isTool: true})
	}
	sort.SliceStable(flushed, func(i, j int) bool {
		if flushed[i].historyID != flushed[j].historyID {
			return flushed[i].historyID < flushed[j].historyID
		}
		// Same historyID: tool windows carry a call ID rather than a
		// numeric history ID, so break ties by kind, then by ID, so the
		// order is deterministic even in that case.
		if flushed[i].isTool != flushed[j].isTool {
			return !flushed[i].isTool
		}
		return flushed[i].id < flushed[j].id
	})

	for _, f := range flushed {
		if f.isTool {
			pd := to.pendingToolDeltas[f.id]
			to.windowBuffer.HandleToolInputDelta(f.id, pd.toolName, pd.content.String(), pd.historyID)
			continue
		}
		pd := to.pendingTextDeltas[f.id]
		to.windowBuffer.AppendOrUpdate(pd.displayTag, f.id, pd.content.String())
	}

	// Allocate fresh maps to avoid retaining references and
	// to provide clear GC boundaries for the builder strings.
	to.pendingTextDeltas = make(map[string]*pendingTextDelta)
	to.pendingToolDeltas = make(map[string]*pendingToolDelta)

	to.dirty.Store(true)
}

// FlushPendingDeltas is the public, lock-safe version of flushPendingDeltas.
// It flushes all accumulated delta frames to the WindowBuffer so the TUI
// can render the latest content. Called from the TUI tick loop.
//
// writeColored calls the internal flushPendingDeltas directly since it
// already holds outputWriter.mu.
func (to *outputWriter) FlushPendingDeltas() {
	to.mu.Lock()
	to.flushPendingDeltas()
	to.mu.Unlock()
}

// handleSystemFrame processes a TagSystemMsg frame.
// Called from processBuffer which holds outputWriter.mu.
func (to *outputWriter) handleSystemFrame(value string) {
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		return
	}
	// The two theme payloads carry theme.Theme, which protocol does not depend on
	// (protocol/sm.go says why), so they are the only ones this adapter reads
	// itself. Everything else is decoded by protocol as the type its envelope
	// names, so a payload's shape lives in one place and a renamed field is a
	// compile error in the handler below rather than a zero value drawn as empty.
	switch protocol.SystemMsgType(env.Type) {
	case protocol.MsgTypeTheme:
		to.handleSystemTheme(env.Data)
	case protocol.MsgTypeThemeList:
		to.handleSystemThemeList(env.Data)
	default:
		msg, derr := protocol.DecodeSystemMsg(env)
		if derr != nil {
			// A known type whose payload is not that type. An adapter draws what
			// it is handed and degrades on what it cannot use — the asymmetry
			// protocol.KindAllowed documents — so the frame is dropped, and the
			// stream is not.
			return
		}
		to.applySystemMsg(msg)
	}
	to.dirty.Store(true)
}

// applySystemMsg folds one decoded system message into the display state.
//
// A nil msg is a type this build does not know, which is not a failure: the SM
// vocabulary grows without breaking readers, and an adapter that answers every
// type it can name keeps working when the core learns a new one.
func (to *outputWriter) applySystemMsg(msg protocol.SystemMsg) {
	switch m := msg.(type) {
	case protocol.ErrorMsg:
		to.handleSystemError(m.Text)
	case protocol.NotifyMsg:
		to.handleSystemNotify(m.Text)
	case protocol.TaskMsg:
		to.handleSystemTask(m)
	case protocol.ModelMsg:
		to.handleSystemModel(m)
	case protocol.ModelListMsg:
		to.handleSystemModelList(m)
	case protocol.ReasoningLevelMsg:
		to.status.updateReasoningLevel(m.Level)
	case protocol.VideoConfigMsg:
		to.status.updateVideoConfig(m.FPS, m.Res)
	case protocol.ToolConfirmMsg:
		to.handleSystemToolConfirm(m.CallID)
	case protocol.MCPMsg:
		to.handleSystemMCP(m)
	case protocol.SessionMsg:
		to.handleSystemSession(m.State)
	case protocol.SteeringMsg:
		to.handleSystemSteering(m)
	}
}

// handleSystemSteering folds a steering event into the queue count, and renders
// the human line it carries only where that line adds something the count does
// not — a drop or a refusal. The refused phase keeps the error register the BUSY
// text used to have.
func (to *outputWriter) handleSystemSteering(m protocol.SteeringMsg) {
	// The count is driven by the phase. The human line is shown only where it
	// says something the count does not — a drop (the words are gone) or a
	// refusal (this prompt was not queued). A "queued" line would only repeat
	// the count.
	to.status.updateSteering(m.Phase)
	if m.Text == "" || m.Phase == protocol.SteeringQueued {
		return
	}
	id := to.generateWindowID()
	tag := TagWindowSN
	if m.Phase == protocol.SteeringRefused {
		tag = TagWindowSE
	}
	to.windowBuffer.AppendOrUpdate(tag, id, m.Text)
}

func (to *outputWriter) handleSystemError(text string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSE, id, text)
}

func (to *outputWriter) handleSystemNotify(text string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, text)
}

// handleCommandOut processes a CO (Command Output) frame.
// Errors render in the error window using the uniform error message;
// successes render the structured result as human-readable text for
// commands that carry informative data (save, fork, ...), and stay
// silent for commands whose UI effect is the feedback itself (cancel,
// theme_set, ...). The command name is correlated from the CI the
// adapter sent; state changes arrive separately via SM broadcasts.
func (to *outputWriter) handleCommandOut(value string) {
	var msg protocol.CmdResultMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		return
	}
	if msg.IsError {
		var errObj protocol.CmdError
		if json.Unmarshal(msg.Result, &errObj) == nil && errObj.Message != "" {
			id := to.generateWindowID()
			to.windowBuffer.AppendOrUpdate(TagWindowSE, id, errObj.Message)
		}
		to.dirty.Store(true)
		return
	}
	name, _ := tuiCommands.Name(msg.CommandID)
	text := commands.RenderResult(name, msg.Result)
	if text == "" {
		// No text feedback needed — the command's UI effect (task stop,
		// theme switch, status change, overlay close) is itself the
		// confirmation. Staying silent keeps the log free of noise for
		// commands like :cancel, :theme_set, :reasoning_level_set.
		return
	}
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, text)
	to.dirty.Store(true)
}

func (to *outputWriter) handleSystemTask(m protocol.TaskMsg) {
	if to.status.updateTask(m.InProgress, m.CurrentStep, m.MaxSteps, m.ContextTokens, m.StepTPS, m.TTFTMS) {
		// Task completion edge: settle tool windows left pending by
		// abnormal paths (canceled confirmation, malformed/dropped frames).
		// Normal tools already settled via their UF frames before this
		// frame arrived (toolWg.Wait precedes handleTaskDone), so only
		// genuine stragglers are affected.
		to.windowBuffer.SettleUnfinishedTools()
	}
}

func (to *outputWriter) handleSystemModel(m protocol.ModelMsg) {
	to.status.updateModel(m.ActiveModelID, m.ActiveModelName, m.ContextLimit)
}

func (to *outputWriter) handleSystemModelList(m protocol.ModelListMsg) {
	to.status.updateModelList(m.Models)
}

// themePayload and themeListPayload are this adapter's copy of the two theme SM
// payloads. They are the only pair an adapter decodes itself: they carry
// theme.Theme, and protocol does not depend on that package (protocol/sm.go says
// why). The agent's side is internal/agent/session_types.go.
//
// A struct here cannot make the two ends agree, so two tests do. The agent's
// adapter_guide_tables_test.go checks its structs against the table
// adapter-guide/README.md documents, and this package's sm_payload_drift_test.go
// feeds that same documented shape through this decoder — so a rename on either
// side meets a test, and the doc is the shape both are measured against.
type themePayload struct {
	Name string `json:"name"`
}

type themeListPayload struct {
	Themes []struct {
		Name  string       `json:"name"`
		Theme *theme.Theme `json:"theme"`
	} `json:"themes"`
}

// handleSystemTheme activates a theme by name. The palette comes from the cache
// theme_list filled; the frame carries no colors of its own.
func (to *outputWriter) handleSystemTheme(data json.RawMessage) {
	var m themePayload
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTheme(m.Name, nil)
}

func (to *outputWriter) handleSystemThemeList(data json.RawMessage) {
	var m themeListPayload
	if json.Unmarshal(data, &m) != nil {
		return
	}
	infos := make([]ThemeEntry, len(m.Themes))
	for i, t := range m.Themes {
		infos[i] = ThemeEntry{Name: t.Name, Theme: t.Theme}
	}
	to.status.updateThemeList(infos)
}

// handleSystemMCP processes a single "mcp" system message.
// All MCP init progress (connecting, auth confirm, done) comes through here.
func (to *outputWriter) handleSystemMCP(msg protocol.MCPMsg) {
	switch msg.Status {
	case protocol.MCPStatusConnecting, protocol.MCPStatusConnected, protocol.MCPStatusFailed:
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case protocol.MCPStatusAuthRequired:
		to.status.updateMCPProgress(protocol.MCPStatusAuthRequired, msg.Server)
		to.status.addMCPAuthPending(msg.Server, msg.URL)
	case protocol.MCPStatusAuthRunning:
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case protocol.MCPStatusDone:
		to.status.updateMCPProgress(protocol.MCPStatusDone, "")
		// Display-only terminal state: the init overlay is closed by the
		// authoritative SM "session" ready frame (ConsumeSessionReady).
	}
}

// handleSystemSession processes a "session" system message — the
// authoritative lifecycle signal. state "ready" means initialization
// completed (replay + MCP settle, including canceled/aborted MCP init
// and the no-MCP case), which the Terminal consumes to close the init
// overlay; state "closed" is the terminal frame, which it consumes to
// leave the event loop. See outputWriter.ConsumeSessionReady() and
// ConsumeSessionClosed().
func (to *outputWriter) handleSystemSession(state string) {
	switch state {
	case protocol.SessionStateReady:
		to.status.markSessionReady()
	case protocol.SessionStateClosed:
		to.status.markSessionClosed()
	}
}

// handleSystemToolConfirm processes a tool_confirm system message.
// Stores the pending state so the Terminal can open its confirm overlay.
func (to *outputWriter) handleSystemToolConfirm(callID string) {
	if callID == "" {
		return
	}
	// Look up the tool info from the function data window for a richer message.
	toolName := ""
	toolInput := ""
	if info := to.windowBuffer.GetFunctionInfo(callID); info != nil {
		toolName = info.Name
		toolInput = info.Input
	}
	// Store pending state for the Terminal's confirm overlay.
	to.status.addToolConfirmPending(callID, toolName, toolInput)
}

// GetPendingToolConfirm returns any pending tool confirmation and clears it.
func (to *outputWriter) GetPendingToolConfirm() (id, toolName, toolInput string, ok bool) {
	return to.status.takeToolConfirmPending()
}

// GetPendingMCPAuth returns a pending MCP auth confirmation, if any.
func (to *outputWriter) GetPendingMCPAuth() (server, url string, ok bool) {
	return to.status.takeMCPAuthPending()
}

// ClearMCPAuths discards any pending MCP auth confirmations.
// Used when the user cancels MCP initialization mid-auth.
func (to *outputWriter) ClearMCPAuths() {
	to.status.clearMCPAuths()
}

// ConsumeSessionReady returns true if initialization just completed
// (SM "session" ready frame received), and resets the flag. The Terminal
// uses this to close the init overlay.
// Also clears any stale MCP auth confirmations that may have been queued
// before the cancellation took effect.
func (to *outputWriter) ConsumeSessionReady() bool {
	done := to.status.takeSessionReady()
	if done {
		to.status.clearMCPAuths()
	}
	return done
}

// ConsumeSessionClosed returns true if the session's terminal frame has
// arrived (SM "session" closed), and resets the flag. The Terminal uses this
// to leave the event loop once the session is over: the frame is the
// session's own announcement, so nothing has to be inferred from a stream
// ending.
func (to *outputWriter) ConsumeSessionClosed() bool {
	return to.status.takeSessionClosed()
}

// ConsumeTaskStarted returns true if a task began since the last call, and
// resets the flag. The Terminal uses it to return the view to the live edge:
// a task that starts from a command — :continue, :summarize — appends what the
// user asked to watch, exactly as a submitted prompt does, and a scrolled-back
// viewport would otherwise hide all of it. The edge is the session's own, so
// the adapter needs no list of which commands start a task.
func (to *outputWriter) ConsumeTaskStarted() bool {
	return to.status.takeTaskStarted()
}

// SnapshotStatus returns a consistent point-in-time view of session status.
func (to *outputWriter) SnapshotStatus() StatusSnapshot {
	return to.status.snapshotStatus()
}

// SnapshotModels returns a consistent point-in-time view of model state.
func (to *outputWriter) SnapshotModels() ModelSnapshot {
	return to.status.snapshotModels()
}

// generateWindowID returns a unique window ID for non-delta messages.
func (to *outputWriter) generateWindowID() string {
	return fmt.Sprintf("win%d", to.nextWindowID.Add(1))
}

// userTag returns true if tag is a user content tag (on the wire).
func userTag(tag string) bool {
	return tag == wire.TagUserX || tag == wire.TagUserEnd
}

// applyWireTime stamps a window's CreatedAt from a frame's "time" field, when
// one is present and parses. v14 puts the message time on authoritative
// content/function frames, so a replayed message renders when it was actually
// sent; frames without one (deltas; a part migrated from a v13 file) keep the
// receipt clock. id is the window's id — the history id for content, the
// call_id for a tool window.
func (to *outputWriter) applyWireTime(id, wireTime string) {
	if wireTime == "" {
		return
	}
	t, err := time.Parse(time.RFC3339, wireTime)
	if err != nil {
		return
	}
	to.windowBuffer.SetCreatedAt(id, t)
}

// bufferUserContent processes one user content frame.
// On the first frame, creates a new window. On subsequent frames,
// updates the existing window incrementally via AppendFromFrame.
// Always marks dirty so the next tick renders the latest state.
func (to *outputWriter) bufferUserContent(id, content string, tag string) {
	if to.activeUserWindowID == "" {
		// First frame — create the window
		to.activeUserWindowID = id
		to.windowBuffer.AppendOrUpdate(tagUserText, id, "")
		to.windowBuffer.SetWindowVisible(id)
	}

	// Track max history ID
	if hid, err := strconv.ParseUint(id, 10, 64); err == nil && hid > to.pendingUserMaxID {
		to.pendingUserMaxID = hid
	}

	// Feed the frame to the window's userRenderer via safe method
	to.windowBuffer.AppendUserContent(to.activeUserWindowID, tag, content)
	to.dirty.Store(true)
}

// flushUserContent finalizes the active user window (sets HistoryID)
// and resets the active window tracking.
func (to *outputWriter) flushUserContent() {
	if to.activeUserWindowID == "" {
		return
	}
	to.windowBuffer.SetHistoryID(to.activeUserWindowID, to.pendingUserMaxID)
	to.activeUserWindowID = ""
	to.pendingUserMaxID = 0
}

// SetScreenWidth updates the window buffer width.
func (to *outputWriter) SetScreenWidth(width int) {
	to.windowBuffer.WithWidth(width)
}

// extractToolOutputDisplayText extracts display text from a tool result's
// content items — the first text item, or a placeholder when there is none.
func extractToolOutputDisplayText(items []protocol.ContentItem) string {
	for _, item := range items {
		if item.Type == protocol.TypeText {
			return item.Text
		}
	}
	if len(items) == 0 {
		return ""
	}
	return "(non-text result)"
}
