package terseio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// answerOutput implements io.Writer. It parses frames, buffers the
// text of the CURRENT assistant message (dropping earlier messages when a
// new history ID starts), and prints it to stdout exactly once when the
// task completes — but only if the final message actually contains text
// (reasoning-only or tool-call-only final messages produce empty stdout).
// Everything else — reasoning, tool calls/results, prompts, media — is
// suppressed; errors, notifications, and informative command results go
// to stderr so stdout stays a pure answer channel.
//
// Concurrency: the session writes from two goroutines (task and run), so a
// mutex protects the buffer and final-text state.
type answerOutput struct {
	stdout io.Writer
	stderr io.Writer

	mu  sync.Mutex // protects dec, finalText, finalTextID, lastMsgHasText
	dec wire.Decoder

	// inProgress tracks the task system-message state. The final text is
	// flushed on the true→false edge.
	inProgress atomic.Bool
	// hasError is set on any SM error; drives the exit code.
	hasError atomic.Bool
	// errorClosed guards errorCh.
	errorClosed atomic.Bool
	errorCh     chan struct{}

	finalText    strings.Builder
	finalTextID  string
	finalFlushed atomic.Bool

	// lastMsgHasText reports whether the CURRENT (i.e. last) assistant
	// message contains text. It is set on every AX/Ax frame and reset on every
	// UF (tool result) — tool results always close the message group, so the
	// next AX/AF frame starts a new message. On flush,
	// an empty value means the final message had no text (e.g. reasoning-
	// only) and the stale buffer from an earlier message must NOT print.
	// Protected by mu.
	lastMsgHasText bool

	// closed is marked when the session's terminal frame arrives (SM
	// "session", state "closed"). The adapter's Start waits on it instead of
	// holding a session handle: the frame is the session's own announcement,
	// and it is written after everything else, so nothing has to be inferred
	// from a stream ending.
	closed *app.Latch

	// MCP hooks, injected by the adapter. mcpAuthRequired starts the
	// automatic OAuth flow for a server; onMCPConnected stops a flow whose
	// server connected by another path; onMCPDone stops every flow when MCP
	// init settles. They are invoked from inside handleSystemFrame (under
	// mu), so they must not block: mcpauth.Flow's methods only do
	// bookkeeping and hand off to their own goroutines.
	mcpAuthRequired func(server, url string)
	onMCPConnected  func(server string)
	onMCPDone       func()
}

func newAnswerOutput(stdout, stderr io.Writer) *answerOutput {
	return &answerOutput{
		stdout:  stdout,
		stderr:  stderr,
		errorCh: make(chan struct{}),
		closed:  app.NewLatch(),
	}
}

// Closed returns a channel closed once the session's terminal frame has been
// parsed.
func (o *answerOutput) Closed() <-chan struct{} { return o.closed.Done() }

// diagnostic writes a fully formatted progress line to stderr under the
// output lock, so the MCP auth goroutine and the frame parser cannot
// interleave a line.
func (o *answerOutput) diagnostic(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.stderr, format, args...)
}

// Write parses and buffers complete frames from p.
func (o *answerOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.dec.Feed(p)
	fatal := o.processBuffer()
	if fatal != nil {
		// The stream is finished. Say so on stderr under the lock (as diagnostic
		// does), then hand the error back so the session stops writing.
		fmt.Fprintf(o.stderr, "[error: %v]\n", fatal)
	}
	o.mu.Unlock()
	if fatal != nil {
		return 0, fatal
	}
	return len(p), nil
}

// ErrorChannel returns a channel that is closed when a TagSystemMsg of
// type "error" is received. It can be used in a select to react to errors
// without a dedicated goroutine.
func (o *answerOutput) ErrorChannel() <-chan struct{} {
	return o.errorCh
}

// HasError returns true if any TagSystemMsg with type "error" was ever received.
func (o *answerOutput) HasError() bool {
	return o.hasError.Load()
}

// FlushFinal prints the buffered final answer to stdout, at most once.
// Thread-safe: takes the mutex, so it is safe from the adapter goroutine
// after the session's terminal frame and from any other caller. No-op after an
// error (the buffer is discarded), if already flushed, or if the final message
// contained no text (the buffer then holds stale text from an earlier
// intermediate message — printing it would be wrong).
func (o *answerOutput) FlushFinal() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flushFinalLocked()
}

// flushFinalLocked is the mutex-free core of FlushFinal. Must be called
// with o.mu held — e.g. from handleSystemFrame, which runs inside
// processBuffer under the mutex (taking the mutex there would deadlock).
func (o *answerOutput) flushFinalLocked() {
	if !o.finalFlushed.CompareAndSwap(false, true) {
		return
	}
	if !o.lastMsgHasText {
		return
	}
	text := o.finalText.String()
	if text != "" {
		fmt.Fprintln(o.stdout, text)
	}
}

// discardFinal drops any buffered text so a partial answer is never
// printed after an error. Must be called under the mutex.
func (o *answerOutput) discardFinal() {
	o.finalTextID = ""
	o.finalText.Reset()
	o.finalFlushed.Store(true)
}

// processBuffer handles every complete frame in the decoder and returns the error
// that finished the stream, if any. Must be called with o.mu held.
func (o *answerOutput) processBuffer() error {
	for {
		tag, value, err := o.dec.Next()
		if err != nil {
			if errors.Is(err, wire.ErrIncomplete) {
				return nil
			}
			return err
		}
		o.handleTag(tag, value)
	}
}

func (o *answerOutput) handleTag(tag, value string) {
	switch tag {
	case wire.TagAssistantXDelta:
		if d, ok := decodeContent(value); ok && d.Type == protocol.TypeText {
			o.lastMsgHasText = true
			o.bufferFinalText(d.ID, d.Text)
		}

	case wire.TagAssistantX:
		// Delta mode: AX carries empty content (already streamed via Ax);
		// --no-delta: AX carries the full text. Buffering both is safe.
		if d, ok := decodeContent(value); ok && d.Type == protocol.TypeText {
			o.lastMsgHasText = true
			o.bufferFinalText(d.ID, d.Text)
		}

	case wire.TagAssistantFDelta, wire.TagAssistantF,
		wire.TagUserFDelta, wire.TagUserX:
		// Reasoning (AX with type "reasoning"), tool calls, and user
		// content are never part of the final answer. Suppressed.

	case wire.TagUserF:
		// A tool result closes the current assistant message group: the
		// next AX/AF frame belongs to a new message. Resetting here
		// ensures a final message without text (e.g. reasoning-only)
		// never flushes the previous message's intermediate text.
		o.lastMsgHasText = false

	case wire.TagSystemMsg:
		o.handleSystemFrame(value)

	case wire.TagCommandOut:
		o.handleCommandOut(value)

	default:
		// tool_confirm and anything else: suppressed. tool_confirm can
		// never arrive: --tool-confirm is rejected at startup (main.go).
	}
}

// decodeContent parses a content-frame body. ok is false on malformed JSON; the
// frame is then skipped rather than trusted.
func decodeContent(value string) (protocol.ContentData, bool) {
	var d protocol.ContentData
	if err := json.Unmarshal([]byte(value), &d); err != nil {
		return protocol.ContentData{}, false
	}
	return d, true
}

// handleCommandOut processes a CO (Command Output) frame — the reply to a
// CI the adapter sent (stdin starting with ":").
//
// Errors are rendered to stderr and drive the exit code: a failed command
// IS a failure signal in scripting mode (unlike plainio, where command
// errors are normal interaction and never affect the exit code).
//
// Successful results render human-readable feedback for informative
// commands (save, fork, ...) and stay silent for self-evident or async
// ones (cancel, continue, summarize, ...) — for async task commands the
// real feedback is the final answer flushed on the task SM. The command
// name is correlated from the CI the adapter sent (CO carries only the ID).
func (o *answerOutput) handleCommandOut(value string) {
	var msg protocol.CmdResultMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		return
	}
	if msg.IsError {
		var errObj protocol.CmdError
		if json.Unmarshal(msg.Result, &errObj) == nil && errObj.Message != "" {
			fmt.Fprintf(o.stderr, "[error: %s]\n", errObj.Message)
			o.hasError.Store(true)
			if o.errorClosed.CompareAndSwap(false, true) {
				close(o.errorCh)
			}
		}
		return
	}
	name, _ := commandNames.LoadAndDelete(msg.CommandID)
	nameStr, _ := name.(string)
	text := renderCommandResult(nameStr, msg.Result)
	if text != "" {
		fmt.Fprintf(o.stderr, "[%s]\n", text)
	}
}

// renderCommandResult formats a successful command result for stderr.
// Commands whose effect is self-evident or async (cancel, continue,
// summarize, model_set, ...) return "" so nothing is printed. The command
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

// bufferFinalText accumulates the current assistant text message. A new
// history ID means a new message — the previous one was intermediate
// (e.g. "I'll check that..."), so it is dropped: only the LAST text
// message survives. Must be called under the mutex.
func (o *answerOutput) bufferFinalText(id, content string) {
	if id == "" {
		return
	}
	if o.finalTextID != id {
		o.finalTextID = id
		o.finalText.Reset()
	}
	o.finalText.WriteString(content)
}

// handleSystemFrame processes a TagSystemMsg frame.
//   - error: rendered to stderr, buffered answer discarded (a partial
//     answer is never printed), exit-code machinery triggered.
//   - notify: rendered to stderr (diagnostics must not pollute stdout).
//   - task: on the in_progress true→false edge, the final answer is flushed.
//
//nolint:gocyclo // dispatch over system message types; each case is simple
func (o *answerOutput) handleSystemFrame(value string) {
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
			fmt.Fprintf(o.stderr, "[error: %s]\n", m.Text)
			o.hasError.Store(true)
			// Never print a partial answer after an error.
			o.discardFinal()
			if o.errorClosed.CompareAndSwap(false, true) {
				close(o.errorCh)
			}
		}
	case protocol.MsgTypeNotify:
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			fmt.Fprintf(o.stderr, "[%s]\n", m.Text)
		}
	case protocol.MsgTypeSteering:
		var m struct {
			Phase string `json:"phase"`
			Text  string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil && m.Text != "" {
			if m.Phase == protocol.SteeringRefused {
				fmt.Fprintf(o.stderr, "[error: %s]\n", m.Text)
			} else {
				fmt.Fprintf(o.stderr, "[%s]\n", m.Text)
			}
		}
	case protocol.MsgTypeTask:
		var m struct {
			InProgress bool `json:"in_progress"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			if o.inProgress.Load() && !m.InProgress {
				// Task finished: print the final answer. Called under o.mu
				// (inside processBuffer), so use the locked core directly.
				o.flushFinalLocked()
			}
			o.inProgress.Store(m.InProgress)
		}
	case protocol.MsgTypeMCP:
		o.handleSystemMCP(env.Data)
	case protocol.MsgTypeSession:
		// The session's terminal frame: everything the session had to say has
		// been written, so this is where the adapter stops waiting and leaves.
		// "ready" is deliberately not interpreted — nothing here waits for
		// readiness any more (a prompt sent too early is held by the session).
		var m struct {
			State string `json:"state"`
		}
		if json.Unmarshal(env.Data, &m) == nil && m.State == protocol.SessionStateClosed {
			o.closed.Mark()
		}
	}
}

// handleSystemMCP processes an "mcp" system message — MCP init progress.
// Progress goes to stderr (stdout stays a pure answer channel);
// "auth_required" starts the adapter-injected automatic OAuth flow, and
// "connected"/"done" stop flows that are no longer needed.
func (o *answerOutput) handleSystemMCP(data json.RawMessage) {
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
	case protocol.MCPStatusConnecting:
		o.mcpStatus("connecting %q", m.Server)
	case protocol.MCPStatusConnected:
		o.mcpStatus("connected %q", m.Server)
		if o.onMCPConnected != nil {
			o.onMCPConnected(m.Server)
		}
	case protocol.MCPStatusFailed:
		o.mcpStatus("failed %q: %s", m.Server, m.Error)
	case protocol.MCPStatusAuthRequired:
		if m.Server == "" {
			return
		}
		o.mcpStatus("server %q requires authorization", m.Server)
		if o.mcpAuthRequired != nil {
			o.mcpAuthRequired(m.Server, m.URL)
		}
	case protocol.MCPStatusAuthRunning:
		o.mcpStatus("waiting for authorization for %q…", m.Server)
	case protocol.MCPStatusDone:
		// Natural completion, or :mcp_skip — stop any running flow.
		if o.onMCPDone != nil {
			o.onMCPDone()
		}
	}
}

// mcpStatus writes one MCP progress line to stderr. Called under o.mu
// (from handleSystemFrame).
func (o *answerOutput) mcpStatus(format string, args ...any) {
	fmt.Fprintf(o.stderr, "\n[mcp: "+format+"]\n", args...)
}
