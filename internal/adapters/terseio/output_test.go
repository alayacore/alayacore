package terseio

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// encodeTestTLV encodes a TLV frame for test input. Test payloads are
// tiny and never exceed maxMessageSize, so the encode error is ignored.
func encodeTestTLV(tag, value string) []byte {
	msg, _ := tlv.EncodeTLV(tag, value)
	return msg
}

// taskMsg builds an SM task frame with the given in_progress state.
func taskMsg(inProgress bool) []byte {
	payload, _ := json.Marshal(protocol.SystemMsgEnvelope{
		Type: string(protocol.MsgTypeTask),
		Data: json.RawMessage(fmt.Sprintf(`{"in_progress":%v}`, inProgress)),
	})
	return encodeTestTLV(tlv.TagSystemMsg, string(payload))
}

// cmdResultMsg builds a CO frame. output is the structured result (or the
// CmdError object when isError is true).
func cmdResultMsg(id string, output any, isError bool) []byte {
	raw, _ := json.Marshal(output)
	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:      id,
		Output:  raw,
		IsError: isError,
	})
	return encodeTestTLV(tlv.TagCommandOut, string(payload))
}

// errorMsg builds an SM error frame.
func errorMsg(text string) []byte {
	payload, _ := json.Marshal(protocol.SystemMsgEnvelope{
		Type: string(protocol.MsgTypeError),
		Data: json.RawMessage(fmt.Sprintf(`{"text":%q}`, text)),
	})
	return encodeTestTLV(tlv.TagSystemMsg, string(payload))
}

func newTestOutput() (*answerOutput, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return newAnswerOutput(&out, &errBuf), &out, &errBuf
}

func TestTerseOutput_OnlyFinalText(t *testing.T) {
	o, out, _ := newTestOutput()

	// First assistant message (intermediate — must be dropped).
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "I'll check that.\n")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", ""))) // delta-mode complete
	// Tool call + result (must be suppressed).
	o.Write(encodeTestTLV(tlv.TagAssistantF, `{"id":"c1","type":"call","name":"search_content","input":"{}"}`))
	o.Write(encodeTestTLV(tlv.TagUserF, `{"id":"c1","output":[],"is_error":false}`))
	// Final assistant message.
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("2", "final ")))
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("2", "answer")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "")))

	// Nothing printed before the task completes.
	if out.String() != "" {
		t.Fatalf("output before task completion = %q, want empty", out.String())
	}

	o.Write(taskMsg(true))
	o.Write(taskMsg(false))

	if got := out.String(); got != "final answer\n" {
		t.Errorf("output = %q, want %q", got, "final answer\n")
	}
}

func TestTerseOutput_NoDeltaCompleteFrames(t *testing.T) {
	o, out, _ := newTestOutput()

	// --no-delta mode: AT carries the full text; reasoning is separate.
	o.Write(encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("1", "thinking...")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "intermediate text")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("3", "final answer")))
	o.Write(taskMsg(true))
	o.Write(taskMsg(false))

	if got := out.String(); got != "final answer\n" {
		t.Errorf("output = %q, want %q", got, "final answer\n")
	}
}

func TestTerseOutput_ReasoningAndNoiseSuppressed(t *testing.T) {
	o, out, errBuf := newTestOutput()

	o.Write(encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID("1", "secret thinking")))
	o.Write(encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("1", "")))
	o.Write(encodeTestTLV(tlv.TagUserT, "user prompt"))
	o.Write(encodeTestTLV(tlv.TagUserI, "data:image/png;base64,xxx"))
	// Notification goes to stderr, not stdout.
	payload, _ := json.Marshal(protocol.SystemMsgEnvelope{
		Type: string(protocol.MsgTypeNotify),
		Data: json.RawMessage(`{"text":"MCP servers initialized: 2 servers"}`),
	})
	o.Write(encodeTestTLV(tlv.TagSystemMsg, string(payload)))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errBuf.String(), "MCP servers initialized") {
		t.Errorf("stderr = %q, want notification", errBuf.String())
	}
}

func TestTerseOutput_ErrorGoesToStderrAndDiscardsAnswer(t *testing.T) {
	o, out, errBuf := newTestOutput()

	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "partial answer")))
	o.Write(taskMsg(true))
	o.Write(errorMsg("boom"))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (partial answer must not print)", out.String())
	}
	if !strings.Contains(errBuf.String(), "[error: boom]") {
		t.Errorf("stderr = %q, want error", errBuf.String())
	}
	if !o.HasError() {
		t.Error("HasError = false, want true")
	}
	select {
	case <-o.ErrorChannel():
	default:
		t.Error("ErrorChannel not closed")
	}

	// Task completion after the error must not flush anything.
	o.Write(taskMsg(false))
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty after error + task done", out.String())
	}
}

func TestTerseOutput_CommandError_GoesToStderrAndSetsExitCode(t *testing.T) {
	o, out, errBuf := newTestOutput()

	o.Write(cmdResultMsg("terse-1", protocol.CmdError{Code: "UNKNOWN_COMMAND", Message: "unknown command: foo"}, true))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errBuf.String(), "[error: unknown command: foo]") {
		t.Errorf("stderr = %q, want command error", errBuf.String())
	}
	if !o.HasError() {
		t.Error("HasError = false, want true (command error drives exit code 1)")
	}
	select {
	case <-o.ErrorChannel():
	default:
		t.Error("ErrorChannel not closed")
	}
}

func TestTerseOutput_CommandSuccess_SaveRendered(t *testing.T) {
	o, out, errBuf := newTestOutput()

	// Correlate the CI the adapter sent: id → commandSave.
	commandNames.Store("terse-7", commandSave)
	o.Write(cmdResultMsg("terse-7", map[string]any{"path": "/tmp/x.alaya"}, false))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errBuf.String(), "[Session saved to /tmp/x.alaya]") {
		t.Errorf("stderr = %q, want save result", errBuf.String())
	}
	if o.HasError() {
		t.Error("HasError = true, want false")
	}
}

func TestTerseOutput_CommandSuccess_SelfEvidentSilent(t *testing.T) {
	o, out, errBuf := newTestOutput()

	// :continue — the final answer on stdout is the feedback, not the CO.
	commandNames.Store("terse-8", "continue")
	o.Write(cmdResultMsg("terse-8", map[string]any{"status": "started"}, false))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if errBuf.String() != "" {
		t.Errorf("stderr = %q, want empty", errBuf.String())
	}
}

func TestTerseOutput_CommandSuccess_UnknownNameSilent(t *testing.T) {
	o, out, errBuf := newTestOutput()

	// No CI correlation (or unknown command name) — stay silent.
	o.Write(cmdResultMsg("terse-9", map[string]any{"ok": true}, false))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if errBuf.String() != "" {
		t.Errorf("stderr = %q, want empty", errBuf.String())
	}
}

func TestTerseOutput_FlushIdempotent(t *testing.T) {
	o, out, _ := newTestOutput()

	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "the answer")))
	o.Write(taskMsg(true))
	o.Write(taskMsg(false)) // flush #1 (task completion)

	// Safety-net flush from the adapter must be a no-op.
	o.FlushFinal()

	if got := out.String(); got != "the answer\n" {
		t.Errorf("output = %q, want %q", got, "the answer\n")
	}
}

func TestTerseOutput_FlushWithoutTaskMsg(t *testing.T) {
	o, out, _ := newTestOutput()

	// Task-completion SM never arrived (edge case); the adapter's
	// safety-net flush prints the buffered answer.
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "answer")))
	o.FlushFinal()

	if got := out.String(); got != "answer\n" {
		t.Errorf("output = %q, want %q", got, "answer\n")
	}
}

func TestTerseOutput_ToolConfirmIgnored(t *testing.T) {
	// Defensive: --tool-confirm is rejected at startup, so tool_confirm
	// frames should never arrive. If one does anyway, it must be silently
	// ignored (no hang, no output).
	o, out, errBuf := newTestOutput()

	payload, _ := json.Marshal(protocol.SystemMsgEnvelope{
		Type: string(protocol.MsgTypeToolConfirm),
		Data: json.RawMessage(`{"id":"call_1"}`),
	})
	o.Write(encodeTestTLV(tlv.TagSystemMsg, string(payload)))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if errBuf.String() != "" {
		t.Errorf("stderr = %q, want empty", errBuf.String())
	}
}

func TestTerseOutput_FinalMessageReasoningOnly_PrintsNothing(t *testing.T) {
	o, out, _ := newTestOutput()

	// Intermediate message with text (must not leak into stdout).
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "I'll check that.")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "")))
	// Tool call + result: UF closes the message group.
	o.Write(encodeTestTLV(tlv.TagAssistantF, `{"id":"c1","type":"call","name":"search_content","input":"{}"}`))
	o.Write(encodeTestTLV(tlv.TagUserF, `{"id":"c1","output":[],"is_error":false}`))
	// Final message: reasoning only, no text.
	o.Write(encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID("2", "deep thinking")))
	o.Write(encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("2", "")))

	o.Write(taskMsg(true))
	o.Write(taskMsg(false))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (final message has no text)", out.String())
	}
}

func TestTerseOutput_FinalMessageToolCallOnly_PrintsNothing(t *testing.T) {
	o, out, _ := newTestOutput()

	// Intermediate message with text.
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "old intermediate text")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "")))
	// Tool call + result closes the group.
	o.Write(encodeTestTLV(tlv.TagAssistantF, `{"id":"c1","type":"call","name":"read_file","input":"{}"}`))
	o.Write(encodeTestTLV(tlv.TagUserF, `{"id":"c1","output":[],"is_error":false}`))
	// Final message: tool call only, no text.
	o.Write(encodeTestTLV(tlv.TagAssistantF, `{"id":"c2","type":"call","name":"execute_command","input":"{}"}`))

	o.Write(taskMsg(true))
	o.Write(taskMsg(false))

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (final message has no text)", out.String())
	}
}

func TestTerseOutput_TextThenToolCallInFinalMessage_PrintsText(t *testing.T) {
	o, out, _ := newTestOutput()

	// Final message: text + tool call, no UF afterwards (defensive: the
	// text came before the tool call in the same message, so it IS text).
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "answer text")))
	o.Write(encodeTestTLV(tlv.TagAssistantF, `{"id":"c1","type":"call","name":"save","input":"{}"}`))

	o.Write(taskMsg(true))
	o.Write(taskMsg(false))

	if got := out.String(); got != "answer text\n" {
		t.Errorf("output = %q, want %q", got, "answer text\n")
	}
}

// tlvFrame is a parsed test frame.
type tlvFrame struct {
	tag   string
	value string
}

// parseTLVFrames parses a raw TLV byte stream into frames.
func parseTLVFrames(t *testing.T, data []byte) []tlvFrame {
	t.Helper()
	var frames []tlvFrame
	for len(data) >= 6 {
		tag := string(data[0:2])
		length := int(binary.BigEndian.Uint32(data[2:6]))
		if len(data) < 6+length {
			t.Fatalf("truncated TLV frame: tag=%q length=%d remaining=%d", tag, length, len(data)-6)
		}
		frames = append(frames, tlvFrame{tag: tag, value: string(data[6 : 6+length])})
		data = data[6+length:]
	}
	if len(data) != 0 {
		t.Fatalf("trailing %d bytes after TLV frames", len(data))
	}
	return frames
}
