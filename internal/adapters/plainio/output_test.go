package plainio

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestNewlineBetweenDifferentStreamGroups(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Simulate: assistant text delta with NUL-delimited history IDs
	msg1 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello "))
	msg2 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("1", "world"))
	// New step: different history ID
	msg3 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("2", "new step"))

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
	o.Write(tlv.EncodeTLV(tlv.TagCommandOut, string(payload)))

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

func TestCommandOut_SuccessRenders(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "x2",
		Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
	})
	o.Write(tlv.EncodeTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if !strings.Contains(got, "Command completed") {
		t.Errorf("output = %q, want confirmation", got)
	}
	if o.HasError() {
		t.Error("success should not set HasError")
	}
}

func TestCommandOut_MalformedIgnored(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	o.Write(tlv.EncodeTLV(tlv.TagCommandOut, "{not json"))

	if buf.Len() != 0 {
		t.Errorf("malformed CO should be ignored, got %q", buf.String())
	}
}

func TestNewlineBetweenTextAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("1", "some text"))
	msg2 := tlv.EncodeTLV(tlv.TagAssistantR, tlv.WrapID("2", "some reasoning"))

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

	msg1 := tlv.EncodeTLV(tlv.TagAssistantR, tlv.WrapID("1", "thinking..."))
	msg2 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("2", "answer"))

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
	msg1 := tlv.EncodeTLV(tlv.TagAssistantT, "hello ")
	msg2 := tlv.EncodeTLV(tlv.TagAssistantT, "world")

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
	msg1 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello"))
	// Then a tool call (resets prefix)
	msg2 := tlv.EncodeTLV(tlv.TagAssistantF, `{"id":"1","type":"call","name":"read_file","input":"{}"}`)
	// Then more text with different prefix — should NOT get extra newline since tool call reset it
	msg3 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("2", "result"))

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

	msg1 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("1", "response"))
	msg2 := tlv.EncodeTLV(tlv.TagUserT, "next prompt")
	msg3 := tlv.EncodeTLV(tlv.TagAssistantT, tlv.WrapID("2", "new response"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	if !contains(got, "response") || !contains(got, "new response") {
		t.Errorf("output = %q", got)
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
