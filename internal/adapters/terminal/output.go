package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text with lipgloss (or any
// ANSI styling), each segment must be rendered individually before concatenation.
// You cannot render a string that already contains ANSI codes with a new style and
// expect it to work - the outer styling will not wrap the inner styled segments.
// Always render segments separately, then join them.

// LOCK ORDERING:
//
// Three mutexes exist, but they are NEVER nested across goroutines:
//
//   outputWriter.mu  — protects the raw TLV buffer and processBuffer() call chain
//   WindowBuffer.mu  — protects window data (windows slice, line heights)
//   sessionState.mu  — protects status/model/queue snapshot fields
//
// Session goroutine call paths (inside outputWriter.mu):
//   outputWriter.mu → WindowBuffer.mu      (content tags, exclusive with sessionState)
//   outputWriter.mu → sessionState.mu      (TagSystemMsg, exclusive with WindowBuffer)
//
// Bubble Tea goroutine call paths:
//   outputWriter.mu → WindowBuffer.mu      (FlushPendingDeltas, exclusive with sessionState)
//   WindowBuffer.mu  (via display update, tick — never nested with sessionState)
//   sessionState.mu  (via snapshot methods — never nested with WindowBuffer)
//
// WindowBuffer.mu and sessionState.mu are NEVER held simultaneously — they
// are acquired and released in separate, sequential calls. No deadlock is
// possible since each goroutine only nests one additional lock under its
// "root" lock (or no nesting at all for the Bubble Tea goroutine).

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// outputWriter parses TLV from the session and writes styled content to the WindowBuffer.
// It implements io.Writer for the agent/session output stream.
type outputWriter struct {
	windowBuffer *WindowBuffer
	buffer       []byte
	mu           sync.Mutex // protects buffer and processBuffer
	dirty        atomic.Bool
	styles       atomic.Pointer[Styles]
	nextWindowID atomic.Int64
	status       sessionState // cached session state (status, models, queue)

	// Active user window — set on first user frame (UT/UI/UV/UA/UD),
	// cleared on next non-user tag. Each new frame updates the
	// window incrementally and marks dirty for immediate render.
	activeUserWindowIdx int    // index into windowBuffer.windows, -1 = none
	activeUserWindowID  string // window ID (for LookupID fallback)
	pendingUserMaxID    uint64 // max history ID across all parts

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
		windowBuffer:        NewWindowBuffer(DefaultWidth, styles),
		status:              sessionState{mu: &sync.Mutex{}},
		activeUserWindowIdx: -1,
		pendingTextDeltas:   make(map[string]*pendingTextDelta),
		pendingToolDeltas:   make(map[string]*pendingToolDelta),
	}
	to.styles.Store(styles)
	return to
}

// SetStyles updates the styles for the output writer
func (to *outputWriter) WithStyles(styles *Styles) {
	to.styles.Store(styles)
	to.windowBuffer.WithStyles(styles)
}

// Close cleans up resources (no background goroutine to stop)
func (to *outputWriter) Close() error {
	return nil
}

func (to *outputWriter) Write(p []byte) (n int, err error) {
	to.mu.Lock()
	to.buffer = append(to.buffer, p...)
	to.processBuffer()
	to.mu.Unlock()
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

// processBuffer parses TLV-encoded data from the buffer
func (to *outputWriter) processBuffer() {
	for len(to.buffer) >= 6 {
		tag := string(to.buffer[0:2])
		length := int(binary.BigEndian.Uint32(to.buffer[2:6]))

		if len(to.buffer) < 6+length {
			break
		}

		value := string(to.buffer[6 : 6+length])
		to.writeColored(tag, value)
		to.buffer = to.buffer[6+length:]
	}
}

// writeColored writes styled content based on the TLV tag
//
//nolint:gocyclo // dispatch over many tag types; each case is simple
func (to *outputWriter) writeColored(tag string, value string) {
	to.triggerUpdateForTag(tag)

	// Flush any pending deltas before processing a non-delta frame.
	// This ensures the WindowBuffer reflects the latest accumulated content
	// before authoritative frames (AT, AR, AF, UF, etc.) arrive.
	if !isDeltaTag(tag) {
		to.flushPendingDeltas()
	}

	// Flush pending user content before any non-user tag.
	if to.activeUserWindowIdx >= 0 && !userTag(tag) {
		to.flushUserContent()
	}

	switch tag {
	// Streaming delta frames — incremental content appended to existing window.
	// Instead of writing directly to WindowBuffer, accumulate for coalescing.
	case tlv.TagAssistantTDelta, tlv.TagAssistantRDelta:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		// Map lowercase delta tag to uppercase for display styling.
		displayTag := tlv.TagAssistantT
		if tag == tlv.TagAssistantRDelta {
			displayTag = tlv.TagAssistantR
		}
		to.pendTextDelta(id, displayTag, content)

	// Complete authoritative frames — content already delivered via deltas
	// during live streaming, but carries the authoritative content during
	// replay. Only display if no window exists yet (replay case).
	case tlv.TagAssistantT, tlv.TagAssistantR:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		if !to.windowBuffer.HasWindow(id) {
			to.windowBuffer.AppendOrUpdate(tag, id, content)
		}

	// User text tag — carries NUL-delimited historyID
	// User content tags — accumulated until next non-user tag triggers flush.
	case tlv.TagUserT:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		to.bufferUserContent(id, content, tlv.TagUserT)

	case tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		id, _, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		to.bufferUserContent(id, tlv.MediaLabel(tag), tag)

	case tlv.TagUserEnd:
		// Flush any pending user content. TagUserEnd should only appear
		// on the input stream, but handle it here as a safety measure.
		to.flushUserContent()

	// Function lifecycle (JSON: id, type, name, input, status)
	// Carries NUL-delimited historyID prefix
	case tlv.TagAssistantF:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		var fd protocol.ToolInputData
		if err := json.Unmarshal([]byte(payload), &fd); err != nil {
			return
		}

		// Resolve the tool name — prefer the existing window (input frame)
		// over the frame itself (start/combined).
		name := fd.Name
		if info := to.windowBuffer.GetFunctionInfo(fd.ID); info != nil {
			name = info.Name
		}

		// Format the input for display.
		if len(fd.Input) == 0 {
			fd.Input = json.RawMessage(name + ": \n")
		} else {
			handler := GetHandler(name)
			fd.Input = json.RawMessage(handler.FormatCall(fd.Input))
		}

		to.windowBuffer.HandleToolInputEvent(fd, parseHistoryID(id))

	// Tool input delta — partial JSON arguments during streaming.
	case tlv.TagAssistantFDelta:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		var fd protocol.ToolInputDeltaData
		if err := json.Unmarshal([]byte(payload), &fd); err != nil {
			return
		}
		// Resolve tool name from existing window if known.
		name := ""
		if info := to.windowBuffer.GetFunctionInfo(fd.ID); info != nil {
			name = info.Name
		}
		to.pendToolDelta(fd.ID, name, fd.Delta, parseHistoryID(id))

	// Tool result preview snapshot (JSON: id, text)
	// Ephemeral display-only frame; authoritative result arrives via UF.
	// Carries NUL-delimited historyID prefix
	case tlv.TagUserFDelta:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		var od protocol.ToolOutputDeltaData
		if err := json.Unmarshal([]byte(payload), &od); err != nil {
			return
		}
		to.windowBuffer.HandleToolOutputDelta(od.ID, od.Text, parseHistoryID(id))

	// Function result (JSON: id, content, is_error)
	// Carries NUL-delimited historyID prefix
	case tlv.TagUserF:
		id, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		var tr protocol.ToolOutputData
		if err := json.Unmarshal([]byte(payload), &tr); err != nil {
			return
		}
		// Extract display text from the content JSON array
		displayText := extractToolOutputDisplayText(tr.Output)
		to.windowBuffer.HandleToolOutput(tr.ID, displayText, tr.IsError, parseHistoryID(id))

	// System tags
	case tlv.TagSystemMsg:
		to.handleSystemMsg(value)
		return

	// Command output (CO) — command result frames
	case tlv.TagCommandOut:
		to.handleCommandOut(value)
		return

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(tag, id, value)
	}
}

// triggerUpdateForTag marks the display as dirty for tags that modify the display.
//
// Most delta tags (At, Ar, Af) are NOT included — they only accumulate pending
// data without modifying WindowBuffer; the dirty flag is set by
// flushPendingDeltas() when accumulated deltas are written to WindowBuffer.
// Uf is the exception: it updates the window directly (snapshot semantics),
// so it must set dirty immediately.
//
// TagSystemMsg is NOT listed here because handleSystemMsg() calls dirty.Store(true)
// after processing, so it doesn't need the early dirty flag from this function.
func (to *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	case tlv.TagAssistantT, tlv.TagAssistantR, tlv.TagAssistantF,
		tlv.TagUserT, tlv.TagUserF, tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD,
		tlv.TagUserFDelta: // Uf updates the window directly (no pending coalescing)
		to.dirty.Store(true)
	}
}

// isDeltaTag reports whether a frame is a streaming delta tag.
// Delta tags do not trigger flushPendingDeltas() when they arrive (they
// either accumulate in the pending maps — At/Ar/Af — or, for Uf, update
// the window directly and must not interleave with a pending flush).
func isDeltaTag(tag string) bool {
	return tag == tlv.TagAssistantTDelta || tag == tlv.TagAssistantRDelta ||
		tag == tlv.TagAssistantFDelta || tag == tlv.TagUserFDelta
}

// pendTextDelta accumulates a text delta ("At" / "Ar") for coalescing.
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

	// Flush text deltas — one AppendOrUpdate per window.
	for id, pd := range to.pendingTextDeltas {
		to.windowBuffer.AppendOrUpdate(pd.displayTag, id, pd.content.String())
	}

	// Flush tool deltas — one HandleToolInputDelta per tool call.
	for toolID, pd := range to.pendingToolDeltas {
		to.windowBuffer.HandleToolInputDelta(toolID, pd.toolName, pd.content.String(), pd.historyID)
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

// handleSystemMsg processes a TagSystemMsg frame.
// Called from processBuffer which holds outputWriter.mu.
func (to *outputWriter) handleSystemMsg(value string) {
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		return
	}
	switch protocol.SystemMsgType(env.Type) {
	case protocol.MsgTypeError:
		to.handleSystemError(env.Data)
	case protocol.MsgTypeNotify:
		to.handleSystemNotify(env.Data)
	case protocol.MsgTypeTask:
		to.handleSystemTask(env.Data)
	case protocol.MsgTypeModel:
		to.handleSystemModel(env.Data)
	case protocol.MsgTypeModelList:
		to.handleSystemModelList(env.Data)
	case protocol.MsgTypeTheme:
		to.handleSystemTheme(env.Data)
	case protocol.MsgTypeThemeList:
		to.handleSystemThemeList(env.Data)
	case protocol.MsgTypeReasoning:
		to.handleSystemReasoning(env.Data)
	case protocol.MsgTypeVideoConfig:
		to.handleSystemVideoConfig(env.Data)
	case protocol.MsgTypeToolConfirm:
		to.handleSystemToolConfirm(env.Data)
	case protocol.MsgTypeMCP:
		to.handleSystemMCP(env.Data)
	case protocol.MsgTypeSession:
		to.handleSystemSession(env.Data)
	}
	to.dirty.Store(true)
}

func (to *outputWriter) handleSystemError(data json.RawMessage) {
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSE, id, m.Text)
}

func (to *outputWriter) handleSystemNotify(data json.RawMessage) {
	var m struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, m.Text)
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
		if json.Unmarshal(msg.Output, &errObj) == nil && errObj.Message != "" {
			id := to.generateWindowID()
			to.windowBuffer.AppendOrUpdate(TagWindowSE, id, errObj.Message)
		}
		to.dirty.Store(true)
		return
	}
	name, _ := commandNames.LoadAndDelete(msg.ID)
	nameStr, _ := name.(string)
	text := renderCommandResult(nameStr, msg.Output)
	if text == "" {
		// No text feedback needed — the command's UI effect (task stop,
		// theme switch, status change, overlay close) is itself the
		// confirmation. Staying silent keeps the log free of noise for
		// commands like :cancel, :theme_set, :reason.
		return
	}
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(TagWindowSN, id, text)
	to.dirty.Store(true)
}

// renderCommandResult formats a successful command result for display.
// Commands whose effect is self-evident in the UI (cancel, theme_set,
// reason, model_set, ...) return "" so nothing is rendered. The command
// name comes from the adapter's own CI tracking; structured fields come
// from the CO output (never display text — rendering is an adapter concern).
func renderCommandResult(name string, output json.RawMessage) string {
	var data struct {
		Path      string `json:"path"`
		HistoryID uint64 `json:"history_id"`
		Server    string `json:"server"`
	}
	_ = json.Unmarshal(output, &data) // best-effort; zero fields render generically
	switch name {
	case commands.CommandNameSave:
		return "Session saved to " + data.Path
	case commands.CommandNameFork:
		return fmt.Sprintf("Session forked to %s (up to content ID %d)", data.Path, data.HistoryID)
	case commands.CommandNameMCPConfirm:
		return fmt.Sprintf("MCP auth code received for %q.", data.Server)
	case commands.CommandNameMCPDecline:
		return fmt.Sprintf("MCP authorization for %q declined.", data.Server)
	}
	return "" // no generic confirmation
}

func (to *outputWriter) handleSystemTask(data json.RawMessage) {
	var m struct {
		InProgress  bool  `json:"in_progress"`
		CurrentStep int   `json:"current_step"`
		MaxSteps    int   `json:"max_steps"`
		Context     int64 `json:"context"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTask(m.InProgress, m.CurrentStep, m.MaxSteps, m.Context)
}

func (to *outputWriter) handleSystemModel(data json.RawMessage) {
	var m struct {
		ActiveID     int    `json:"active_id"`
		ActiveName   string `json:"active_name"`
		ContextLimit int64  `json:"context_limit"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateModel(m.ActiveID, m.ActiveName, m.ContextLimit)
}

func (to *outputWriter) handleSystemModelList(data json.RawMessage) {
	var m struct {
		Models []protocol.ModelInfo `json:"models"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateModelList(m.Models)
}

func (to *outputWriter) handleSystemTheme(data json.RawMessage) {
	var m struct {
		Name  string       `json:"name"`
		Theme *theme.Theme `json:"theme"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateTheme(m.Name, m.Theme)
}

func (to *outputWriter) handleSystemThemeList(data json.RawMessage) {
	var m struct {
		Themes []struct {
			Name  string       `json:"name"`
			Theme *theme.Theme `json:"theme"`
		} `json:"themes"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	infos := make([]ThemeEntry, len(m.Themes))
	for i, t := range m.Themes {
		infos[i] = ThemeEntry{Name: t.Name, Theme: t.Theme}
	}
	to.status.updateThemeList(infos)
}

func (to *outputWriter) handleSystemReasoning(data json.RawMessage) {
	var m struct {
		Level int `json:"level"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateReasoning(m.Level)
}

// handleSystemVideoConfig updates the video FPS and resolution in session status.
func (to *outputWriter) handleSystemVideoConfig(data json.RawMessage) {
	var m struct {
		FPS int `json:"fps"`
		Res int `json:"res"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	to.status.updateVideoConfig(m.FPS, m.Res)
}

// handleSystemMCP processes a single "mcp" system message.
// All MCP init progress (connecting, auth confirm, done) comes through here.
func (to *outputWriter) handleSystemMCP(data json.RawMessage) {
	var msg struct {
		Status string `json:"status"`
		Server string `json:"server,omitempty"`
		URL    string `json:"url,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}

	switch msg.Status {
	case "connecting", "connected", "failed":
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case "auth_required":
		if msg.Server != "" {
			to.status.updateMCPProgress("auth_required", msg.Server)
			to.status.addMCPAuthPending(msg.Server, msg.URL)
		}
	case "auth_running":
		to.status.updateMCPProgress(msg.Status, msg.Server)
	case "done":
		to.status.updateMCPProgress("done", "")
		// Display-only terminal state: the init overlay is closed by the
		// authoritative SM "session" ready frame (ConsumeSessionReady).
	}
}

// handleSystemSession processes a "session" system message — the
// authoritative lifecycle signal. state "ready" means initialization
// completed (replay + MCP settle, including canceled/aborted MCP init
// and the no-MCP case). The Terminal consumes it to close the init
// overlay; see outputWriter.ConsumeSessionReady().
func (to *outputWriter) handleSystemSession(data json.RawMessage) {
	var m struct {
		State string `json:"state"`
	}
	if json.Unmarshal(data, &m) != nil || m.State != "ready" {
		return
	}
	to.status.markSessionReady()
}

// handleSystemToolConfirm processes a tool_confirm system message.
// Stores the pending state so the Terminal can open its confirm overlay.
func (to *outputWriter) handleSystemToolConfirm(data json.RawMessage) {
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &m) != nil || m.ID == "" {
		return
	}
	// Look up the tool info from the function data window for a richer message.
	toolName := ""
	toolInput := ""
	if info := to.windowBuffer.GetFunctionInfo(m.ID); info != nil {
		toolName = info.Name
		toolInput = info.Input
	}
	// Store pending state for the Terminal's confirm overlay.
	to.status.addToolConfirmPending(m.ID, toolName, toolInput)
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

// userTag returns true if tag is a user content tag.
func userTag(tag string) bool {
	return tag == tlv.TagUserT || tag == tlv.TagUserI ||
		tag == tlv.TagUserV || tag == tlv.TagUserA || tag == tlv.TagUserD || tag == tlv.TagUserEnd
}

// bufferUserContent processes one user content frame.
// On the first frame, creates a new window. On subsequent frames,
// updates the existing window incrementally via AppendFromTLV.
// Always marks dirty so the next tick renders the latest state.
func (to *outputWriter) bufferUserContent(id, content string, tag string) {
	if to.activeUserWindowIdx < 0 {
		// First frame — create the window
		to.activeUserWindowID = id
		to.activeUserWindowIdx = to.windowBuffer.AppendOrUpdate(tlv.TagUserT, id, "")
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
	if to.activeUserWindowIdx < 0 {
		return
	}
	to.windowBuffer.SetHistoryID(to.activeUserWindowIdx, to.pendingUserMaxID)
	to.activeUserWindowIdx = -1
	to.activeUserWindowID = ""
	to.pendingUserMaxID = 0
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.WithWidth(width)
}

// extractToolOutputDisplayText extracts display text from a tool result content JSON array.
// The content is a JSON array of {"type":"text","text":"..."} items.
func extractToolOutputDisplayText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(content, &items); err != nil {
		return string(content)
	}
	for _, item := range items {
		if item.Type == "text" {
			return item.Text
		}
	}
	return "(non-text result)"
}
