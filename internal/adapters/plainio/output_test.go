package plainio

import (
	"bytes"
	"encoding/json"
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

func TestNewlineBetweenDifferentStreamGroups(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Simulate: assistant text delta with NUL-delimited history IDs
	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello "))
	msg2 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "world"))
	// New step: different history ID
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "new step"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	want := "hello world\nnew step"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestCommandOut_ErrorRenders(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:      "x1",
		IsError: true,
		Output:  json.RawMessage(`{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if !strings.Contains(got, "model_set: model not found: 99") {
		t.Errorf("output = %q, want error message", got)
	}
	// A command failure must not poison the exit code.
	if o.HasError() {
		t.Error("command error should not set HasError (exit code)")
	}
	select {
	case <-o.ErrorChannel():
		t.Error("command error should not close ErrorChannel")
	default:
	}
}

func TestCommandOut_NoNameMappingSilent(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "x2",
		Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if got != "" {
		t.Errorf("output = %q, want empty (no generic confirmation)", got)
	}
	if o.HasError() {
		t.Error("success should not set HasError")
	}
}

func TestCommandOut_SuccessRendersByName(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}
	commandNames.Store("p1", "save")
	defer commandNames.Delete("p1")

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "p1",
		Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if !strings.Contains(got, "Session saved to /tmp/x.alaya") {
		t.Errorf("output = %q, want rendered save result", got)
	}
	if strings.Contains(got, "Command completed") {
		t.Errorf("generic confirmation should not appear for a known command, got %q", got)
	}
	if _, ok := commandNames.Load("p1"); ok {
		t.Error("command name mapping should be consumed after the CO arrives")
	}
}

func TestCommandOut_MalformedIgnored(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	o.Write(encodeTestTLV(tlv.TagCommandOut, "{not json"))

	if buf.Len() != 0 {
		t.Errorf("malformed CO should be ignored, got %q", buf.String())
	}
}

func TestNewlineBetweenTextAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "some text"))
	msg2 := encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("2", "some reasoning"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "some text\nsome reasoning"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenReasoningAndText(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("1", "thinking..."))
	msg2 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "answer"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "thinking...\nanswer"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoPrefixNoNewline(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Messages without stream prefixes are malformed for stdout AT frames
	// (history ID is always required). They should be silently ignored.
	msg1 := encodeTestTLV(tlv.TagAssistantT, "hello ")
	msg2 := encodeTestTLV(tlv.TagAssistantT, "world")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := ""
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestToolCallResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Stream some text
	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello"))
	// Then a tool call (resets prefix)
	msg2 := encodeTestTLV(tlv.TagAssistantF, `{"id":"1","type":"call","name":"read_file","input":"{}"}`)
	// Then more text with different prefix — should NOT get extra newline since tool call reset it
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "result"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	// After tool call, lastHistoryID is "" so the new ID doesn't trigger separator
	if !contains(got, "hello") || !contains(got, "result") {
		t.Errorf("output = %q", got)
	}
}

func TestUserPromptResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "response"))
	// Echoed user prompts carry a NUL-delimited history ID (the agent
	// assigns one before echoing back).
	msg2 := encodeTestTLV(tlv.TagUserT, tlv.WrapID("9", "next prompt"))
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "new response"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	// The User block is its own line with a blank line after it; the
	// prompt resets the stream prefix so the next assistant message gets
	// no extra separator.
	want := "response\nUser: next prompt\n\nnew response"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestUserPromptBlockFormat(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Tool result ends with a newline → the User block gets a blank line
	// before it.
	o.Write(encodeTestTLV(tlv.TagUserF, tlv.WrapID("3", `{"id":"c1","output":[],"is_error":false}`)))
	o.Write(encodeTestTLV(tlv.TagUserT, tlv.WrapID("1", "hello world")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "answer")))

	want := "{\"id\":\"c1\",\"output\":[],\"is_error\":false}\n\nUser: hello world\n\nanswer"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
