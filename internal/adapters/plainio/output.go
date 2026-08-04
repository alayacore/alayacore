package plainio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// stdoutOutput implements io.Writer.
// It parses TLV messages and prints human-readable text to stdout.
//
// Concurrency: the session writes from two goroutines (task and run),
// so a mutex protects the buffer and the tag/history-ID state.
// See doc.go for the full contract.
//
// Hooks: MCP auth events (mcpAuthRequired/onMCPConnected/onMCPDone) are
// NOT executed while o.mu is held. handleSystemMCP registers them via
// deferHook and Write runs them after unlocking — the same
// register-then-consume pattern the terminal adapter implements with its
// bubbletea message loop. This keeps hooks free to call printLine (which
// takes o.mu) without deadlocking.
type stdoutOutput struct {
	writer        io.Writer
	mu            sync.Mutex // protects buf, lastTag, lastHistoryID, seenDelta, pendingHooks
	buf           []byte
	inProgress    atomic.Bool
	lastTag       string
	lastHistoryID string
	seenDelta     map[string]bool // history IDs already printed via At/Ar deltas
	pendingHooks  []func()        // hooks collected under mu, executed by Write after unlock

	// MCP hooks, injected by the adapter. mcpAuthRequired is invoked when
	// a server needs OAuth authorization; onMCPConnected fires when a
	// server connects (so its auth flow can stop waiting); onMCPDone fires
	// when MCP init completes (or is canceled) so any running auth flow
	// can clean up.
	mcpAuthRequired func(server, url string)
	onMCPConnected  func(server string)
	onMCPDone       func()
}

func newStdoutOutput() *stdoutOutput {
	return &stdoutOutput{
		writer:    os.Stdout,
		seenDelta: make(map[string]bool),
	}
}

func (o *stdoutOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.processBuffer()
	hooks := o.pendingHooks
	o.pendingHooks = nil
	o.mu.Unlock()
	for _, h := range hooks {
		h() // outside the lock, in FIFO order
	}
	return len(p), nil
}

// deferHook registers a callback to run after Write releases the output
// lock. Must be called with o.mu held (from the processBuffer call
// chain). Hooks run in registration order; each Write drains the queue.
// Ordering across Writes is preserved because MCP frames are written
// sequentially by the session's run goroutine (task goroutines never
// emit them), so hooks can never be registered concurrently out of
// order. Hooks must not block for long — they run inline in Write.
func (o *stdoutOutput) deferHook(hook func()) {
	o.pendingHooks = append(o.pendingHooks, hook)
}

// processBuffer parses and prints complete TLV frames from the buffer.
func (o *stdoutOutput) processBuffer() {
	for len(o.buf) >= 6 {
		tag := string(o.buf[0:2])
		length := int(binary.BigEndian.Uint32(o.buf[2:6]))
		if len(o.buf) < 6+length {
			break
		}
		value := string(o.buf[6 : 6+length])
		o.buf = o.buf[6+length:]
		o.printMessage(tag, value)
	}
}

func (o *stdoutOutput) printMessage(tag string, value string) {
	o.handleTag(tag, value)
}

//nolint:gocyclo // dispatch over many tag types; each case is simple
func (o *stdoutOutput) handleTag(tag, value string) {
	switch tag {
	case tlv.TagAssistantTDelta, tlv.TagAssistantRDelta:
		id, _, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		o.seenDelta[id] = true
		o.handleTextDelta(tag, value)

	case tlv.TagAssistantT, tlv.TagAssistantR:
		id, _, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		if o.seenDelta[id] {
			return
		}
		o.handleTextDelta(tag, value)

	case tlv.TagUserT:
		_, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		// Render the echoed user prompt as its own block so it stands out
		// from the assistant stream. The fixed format replaces the old
		// conditional emitSeparator logic: the leading newline separates
		// it from the previous message (a blank line when the previous
		// output already ended with a newline), the two trailing newlines
		// guarantee a blank line before the assistant response.
		fmt.Fprintf(o.writer, "\nUser: %s\n\n", content)
		o.lastTag = tag
		o.lastHistoryID = ""

	case tlv.TagSystemMsg:
		o.handleSystemMsg(value)

	case tlv.TagCommandOut:
		o.handleCommandOut(value)

	case tlv.TagAssistantF:
		_, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastHistoryID = ""
		// Show complete tool call JSON.
		fmt.Fprintf(o.writer, "%s\n", payload)

	// Tool argument deltas are ephemeral — printed via AF complete frame.
	case tlv.TagAssistantFDelta:
		// Ignore.

	// Tool result preview snapshots are ephemeral — printed via UF frame.
	case tlv.TagUserFDelta:
		// Ignore.

	case tlv.TagUserF:
		_, payload, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		// Show complete tool result JSON.
		if o.lastTag != "" && o.lastTag != tag {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastHistoryID = ""
		fmt.Fprintf(o.writer, "%s\n", payload)

	case tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		o.handleMediaTag(tag, value)

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// handleCommandOut processes a CO (Command Output) frame.
// Errors print like system errors but do NOT affect the exit code (a
// command failure is normal interaction, not a session error); successes
// render the structured result as human-readable text for commands that
// carry informative data (save, fork, ...), and stay silent for commands
// whose effect is self-evident (cancel, reason, ...). The command name
// is correlated from the CI the adapter sent.
func (o *stdoutOutput) handleCommandOut(value string) {
	var msg protocol.CmdResultMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		return
	}
	if msg.IsError {
		var errObj protocol.CmdError
		if json.Unmarshal(msg.Output, &errObj) == nil && errObj.Message != "" {
			fmt.Fprintf(o.writer, "\n[error: %s]\n", errObj.Message)
			o.lastTag = ""
			o.lastHistoryID = ""
		}
		return
	}
	name, _ := commandNames.LoadAndDelete(msg.ID)
	nameStr, _ := name.(string)
	o.lastTag = ""
	o.lastHistoryID = ""
	text := renderCommandResult(nameStr, msg.Output)
	if text == "" {
		// No text feedback needed — the command's effect (task stop,
		// status change) is itself the confirmation. Stay silent.
		return
	}
	fmt.Fprintf(o.writer, "\n[%s]\n", text)
}

// renderCommandResult formats a successful command result for display.
// Commands whose effect is self-evident (cancel, reason, model_set, ...)
// return "" so nothing is printed. The command name comes from the
// adapter's own CI tracking; structured fields come from the CO output
// (never display text — rendering is an adapter concern).
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

// handleTextDelta handles assistant text/reasoning tags (AT/AR/At/Ar).
// It prints a separator when transitioning between different tags or
// history IDs, then prints the content delta.
func (o *stdoutOutput) handleTextDelta(tag, value string) {
	id, content, _ := tlv.UnwrapID(value)
	if o.lastHistoryID != "" && o.lastTag != tag {
		// Transitioning from a different tag → separator
		fmt.Fprintln(o.writer)
	} else if o.lastHistoryID != "" && id != o.lastHistoryID {
		// Same tag but different history ID → separator
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastHistoryID = id
	fmt.Fprint(o.writer, content)
	if id == "" {
		fmt.Fprintln(o.writer)
	}
}

// handleMediaTag prints a media label (image/video/audio/document).
func (o *stdoutOutput) handleMediaTag(tag, value string) {
	tlv.UnwrapID(value)
	o.emitSeparator(tag)
	label := map[string]string{
		tlv.TagUserI: "image",
		tlv.TagUserV: "video",
		tlv.TagUserA: "audio",
		tlv.TagUserD: "document",
	}[tag]
	fmt.Fprintf(o.writer, "[%s]\n", label)
}

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag and the previous frame was streamed (had a non-empty history ID).
// It updates lastTag to the new tag.
func (o *stdoutOutput) emitSeparator(tag string) {
	if o.lastHistoryID != "" && o.lastTag != "" && o.lastTag != tag {
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastHistoryID = ""
}

// handleSystemMsg processes a TagSystemMsg frame.
// Handles error, notify, task, and tool_confirm system messages.
// Task completion transitions print a trailing blank line between tasks.
func (o *stdoutOutput) handleSystemMsg(value string) {
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		return
	}
	switch protocol.SystemMsgType(env.Type) {
	case protocol.MsgTypeError:
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			fmt.Fprintf(o.writer, "\n[error: %s]\n", m.Text)
			o.lastTag = ""
			o.lastHistoryID = ""
		}
	case protocol.MsgTypeNotify:
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			fmt.Fprintf(o.writer, "\n[%s]\n", m.Text)
			o.lastTag = ""
			o.lastHistoryID = ""
		}
	case protocol.MsgTypeTask:
		var m struct {
			InProgress bool `json:"in_progress"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			if o.inProgress.Load() && !m.InProgress {
				fmt.Fprintln(o.writer)
				o.lastTag = ""
				o.lastHistoryID = ""
			}
			o.inProgress.Store(m.InProgress)
		}
	case protocol.MsgTypeToolConfirm:
		var m struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(env.Data, &m) != nil || m.ID == "" {
			return
		}
		fmt.Fprintf(o.writer, "\n[tool_confirm: allow tool %q to run?]\n", m.ID)
		o.lastTag = ""
		o.lastHistoryID = ""

	case protocol.MsgTypeMCP:
		o.handleSystemMCP(env.Data)
	}
}

// handleSystemMCP processes an "mcp" system message — MCP init progress.
// Each status renders as a plain-text line; "auth_required" additionally
// hands off to the adapter-injected OAuth flow (async), and "done" signals
// the flow to clean up any running callback server.
func (o *stdoutOutput) handleSystemMCP(data json.RawMessage) {
	var m struct {
		Status string `json:"status"`
		Server string `json:"server,omitempty"`
		URL    string `json:"url,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	switch m.Status {
	case "connecting":
		o.printMCPStatus("connecting %q", m.Server)
	case "connected":
		o.printMCPStatus("connected %q", m.Server)
		if o.onMCPConnected != nil {
			o.deferHook(func() { o.onMCPConnected(m.Server) })
		}
	case "failed":
		o.printMCPStatus("failed %q: %s", m.Server, m.Error)
	case "auth_required":
		if m.Server == "" {
			return
		}
		o.printMCPStatus("server %q requires authorization", m.Server)
		if o.mcpAuthRequired != nil {
			o.deferHook(func() { o.mcpAuthRequired(m.Server, m.URL) })
		}
	case "auth_running":
		o.printMCPStatus("waiting for authorization for %q…", m.Server)
	case "done":
		// Completion is already announced by the session's notify
		// ("MCP servers initialized: ..."). Just release any running
		// auth flow — this covers both natural completion and :mcp_cancel.
		if o.onMCPDone != nil {
			o.deferHook(o.onMCPDone)
		}
	}
}

// printMCPStatus renders one MCP progress line and resets streaming state.
// Must be called with o.mu held (from handleSystemMsg).
func (o *stdoutOutput) printMCPStatus(format string, args ...any) {
	fmt.Fprintf(o.writer, "\n[mcp: "+format+"]\n", args...)
	o.lastTag = ""
	o.lastHistoryID = ""
}

// printLine writes a raw line to stdout under the output lock and resets
// streaming state. Safe for concurrent use (e.g. the MCP auth goroutine).
func (o *stdoutOutput) printLine(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.writer, format, args...)
	o.lastTag = ""
	o.lastHistoryID = ""
}
