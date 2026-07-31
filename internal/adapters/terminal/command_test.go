package terminal

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// writeCommand — CI frame construction
// ============================================================================

func TestWriteCommand_CIFrame(t *testing.T) {
	var buf bytes.Buffer
	writeCommand(&buf, ":save /tmp/x.alaya")

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}

	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "save" {
		t.Errorf("name = %q, want %q", msg.Name, "save")
	}
	if msg.Input != "/tmp/x.alaya" {
		t.Errorf("input = %q, want %q", msg.Input, "/tmp/x.alaya")
	}
	if msg.ID == "" {
		t.Error("CI frame should carry a generated call ID")
	}
	if !strings.HasPrefix(msg.ID, "tui-") {
		t.Errorf("unexpected id format: %q", msg.ID)
	}
}

func TestWriteCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	writeCommand(&buf, ":cancel")

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}
	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "cancel" || msg.Input != "" {
		t.Errorf("unexpected cmd: name=%q input=%q", msg.Name, msg.Input)
	}
}

func TestWriteCommand_CommandWithoutColon(t *testing.T) {
	var buf bytes.Buffer
	writeCommand(&buf, "model_set 3")

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}
	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "model_set" || msg.Input != "3" {
		t.Errorf("unexpected cmd: name=%q input=%q", msg.Name, msg.Input)
	}
}

// ============================================================================
// handleCommandOut — CO frame rendering
// ============================================================================

func TestCommandOut_ErrorRendersMessage(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:      "x1",
		IsError: true,
		Output:  mustRaw(`{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}`),
	})
	if err := tlv.WriteTLV(out, tlv.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].RawTag() != TagWindowSE {
		t.Errorf("expected SE (error) window, got %s", windows[0].RawTag())
	}
	if !strings.Contains(windows[0].RawContent(), "model_set: model not found: 99") {
		t.Errorf("error window should contain the message, got %q", windows[0].RawContent())
	}
	// CO handling must mark the display dirty so the view refreshes promptly.
	if !out.DrainDirty() {
		t.Error("CO frame should set the dirty flag for immediate display refresh")
	}
}

func TestCommandOut_SuccessConfirmation(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "x2",
		Output: mustRaw(`{"path":"/tmp/x.alaya"}`),
	})
	if err := tlv.WriteTLV(out, tlv.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].RawTag() != TagWindowSN {
		t.Errorf("expected SN (notify) window, got %s", windows[0].RawTag())
	}
	if !strings.Contains(windows[0].RawContent(), "Command completed") {
		t.Errorf("success window should show confirmation, got %q", windows[0].RawContent())
	}
	if !out.DrainDirty() {
		t.Error("CO frame should set the dirty flag for immediate display refresh")
	}
}

func TestCommandOut_MalformedIgnored(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	if err := tlv.WriteTLV(out, tlv.TagCommandOut, "{not json"); err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 0 {
		t.Errorf("malformed CO should be ignored, got %d windows", len(windows))
	}
}

func mustRaw(s string) json.RawMessage {
	return json.RawMessage(s)
}
