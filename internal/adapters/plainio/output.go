package plainio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// stdoutOutput implements io.Writer.
// It parses TLV messages and prints human-readable text to stdout.
//
// Concurrency: the session writes from two goroutines (task and run),
// so a mutex protects the buffer, tag/history-ID state, and the close-once
// guard for errorCh. See stream/doc.go for the full contract.
type stdoutOutput struct {
	writer        io.Writer
	mu            sync.Mutex // protects buf, lastTag, lastHistoryID, errorClosed, seenDelta
	buf           []byte
	inProgress    atomic.Bool
	hasError      atomic.Bool
	errorClosed   atomic.Bool // true once errorCh has been closed
	errorCh       chan struct{}
	lastTag       string
	lastHistoryID string
	seenDelta     map[string]bool // history IDs already printed via At/Ar deltas
}

func newStdoutOutput() *stdoutOutput {
	return &stdoutOutput{
		writer:    os.Stdout,
		errorCh:   make(chan struct{}),
		seenDelta: make(map[string]bool),
	}
}

func (o *stdoutOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.processBuffer()
	o.mu.Unlock()
	return len(p), nil
}

// ErrorChannel returns a channel that is closed when an SE (system error)
// tag is received. It can be used in a select to react to errors without
// a dedicated goroutine.
func (o *stdoutOutput) ErrorChannel() <-chan struct{} {
	return o.errorCh
}

// HasError returns true if any TagSystemMsg with type "error" was ever received.
func (o *stdoutOutput) HasError() bool {
	return o.hasError.Load()
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
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "> %s\n", content)

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
// render the structured result as human-readable text (the command name
// is correlated from the CI the adapter sent).
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
	case "save":
		return "Session saved to " + data.Path
	case "fork":
		return fmt.Sprintf("Session forked to %s (up to content ID %d)", data.Path, data.HistoryID)
	case "mcp_confirm":
		return fmt.Sprintf("MCP auth code received for %q.", data.Server)
	case "mcp_decline":
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

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag and the previous frame was streamed (had a non-empty history ID).
// It updates lastTag to the new tag.
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
			o.hasError.Store(true)
			if o.errorClosed.CompareAndSwap(false, true) {
				close(o.errorCh)
			}
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
	}
}
