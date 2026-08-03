package terseio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// answerOutput implements io.Writer. It parses TLV messages, buffers the
// text of the CURRENT assistant message (dropping earlier messages when a
// new history ID starts), and prints it to stdout exactly once when the
// task completes — but only if the final message actually contains text
// (reasoning-only or tool-call-only final messages produce empty stdout).
// Everything else — reasoning, tool calls/results, prompts, media — is
// suppressed; errors and notifications go to stderr so stdout stays a pure
// answer channel.
//
// Concurrency: the session writes from two goroutines (task and run), so a
// mutex protects the buffer and final-text state.
type answerOutput struct {
	stdout io.Writer
	stderr io.Writer

	mu  sync.Mutex // protects buf, finalText, finalTextID, lastMsgHasText
	buf []byte

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
	// message contains text. It is set on every AT/At frame and reset on
	// every UF (tool result) — tool results always close the message
	// group, so the next AT/AR/AF frame starts a new message. On flush,
	// an empty value means the final message had no text (e.g. reasoning-
	// only) and the stale buffer from an earlier message must NOT print.
	// Protected by mu (read lock-free in FlushFinal like finalText).
	lastMsgHasText bool
}

func newAnswerOutput(stdout, stderr io.Writer) *answerOutput {
	return &answerOutput{
		stdout:  stdout,
		stderr:  stderr,
		errorCh: make(chan struct{}),
	}
}

// Write parses and buffers complete TLV frames from p.
func (o *answerOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.processBuffer()
	o.mu.Unlock()
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
//
// Callable from processBuffer (under the mutex, on task completion) or from
// the adapter after session.Done() (no concurrent writers by then), so it
// deliberately does not take the mutex — the atomic guard makes it safe.
// No-op after an error (the buffer is discarded), if already flushed, or if
// the final message contained no text (the buffer then holds stale text
// from an earlier intermediate message — printing it would be wrong).
func (o *answerOutput) FlushFinal() {
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

// processBuffer parses complete TLV frames from the buffer.
func (o *answerOutput) processBuffer() {
	for len(o.buf) >= 6 {
		tag := string(o.buf[0:2])
		length := int(binary.BigEndian.Uint32(o.buf[2:6]))
		if len(o.buf) < 6+length {
			break
		}
		value := string(o.buf[6 : 6+length])
		o.buf = o.buf[6+length:]
		o.handleTag(tag, value)
	}
}

func (o *answerOutput) handleTag(tag, value string) {
	switch tag {
	case tlv.TagAssistantTDelta:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		o.lastMsgHasText = true
		o.bufferFinalText(id, content)

	case tlv.TagAssistantT:
		id, content, ok := tlv.UnwrapID(value)
		if !ok {
			return
		}
		// Delta mode: AT carries empty content (already streamed via At);
		// --no-delta: AT carries the full text. Buffering both is safe.
		o.lastMsgHasText = true
		o.bufferFinalText(id, content)

	case tlv.TagAssistantRDelta, tlv.TagAssistantR,
		tlv.TagAssistantFDelta, tlv.TagAssistantF,
		tlv.TagUserT, tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		// Reasoning, tool calls, and user content are never part of the
		// final answer. Suppressed.

	case tlv.TagUserF:
		// A tool result closes the current assistant message group: the
		// next AT/AR/AF frame belongs to a new message. Resetting here
		// ensures a final message without text (e.g. reasoning-only)
		// never flushes the previous message's intermediate text.
		o.lastMsgHasText = false

	case tlv.TagSystemMsg:
		o.handleSystemMsg(value)

	case tlv.TagCommandOut:
		o.handleCommandOut(value)

	default:
		// tool_confirm and anything else: suppressed. tool_confirm can
		// never arrive: --tool-confirm is rejected at startup (main.go).
	}
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
		if json.Unmarshal(msg.Output, &errObj) == nil && errObj.Message != "" {
			fmt.Fprintf(o.stderr, "[error: %s]\n", errObj.Message)
			o.hasError.Store(true)
			if o.errorClosed.CompareAndSwap(false, true) {
				close(o.errorCh)
			}
		}
		return
	}
	name, _ := commandNames.LoadAndDelete(msg.ID)
	nameStr, _ := name.(string)
	text := renderCommandResult(nameStr, msg.Output)
	if text != "" {
		fmt.Fprintf(o.stderr, "[%s]\n", text)
	}
}

// Command name constants for renderCommandResult. Kept local to the
// adapter — the session registry in internal/agent owns the canonical
// command names; these are only for CO result rendering.
const (
	commandSave       = "save"
	commandFork       = "fork"
	commandMCPConfirm = "mcp_confirm"
	commandMCPDecline = "mcp_decline"
)

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
	case commandSave:
		return "Session saved to " + data.Path
	case commandFork:
		return fmt.Sprintf("Session forked to %s (up to content ID %d)", data.Path, data.HistoryID)
	case commandMCPConfirm:
		return fmt.Sprintf("MCP auth code received for %q.", data.Server)
	case commandMCPDecline:
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

// handleSystemMsg processes a TagSystemMsg frame.
//   - error: rendered to stderr, buffered answer discarded (a partial
//     answer is never printed), exit-code machinery triggered.
//   - notify: rendered to stderr (diagnostics must not pollute stdout).
//   - task: on the in_progress true→false edge, the final answer is flushed.
func (o *answerOutput) handleSystemMsg(value string) {
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
	case protocol.MsgTypeTask:
		var m struct {
			InProgress bool `json:"in_progress"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			if o.inProgress.Load() && !m.InProgress {
				// Task finished: print the final answer.
				o.FlushFinal()
			}
			o.inProgress.Store(m.InProgress)
		}
	}
}
