package terminal

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// ============================================================================
// CI frame construction — commands.Writer, as this adapter uses it
// ============================================================================

func TestWriteCommand_CIFrame(t *testing.T) {
	var buf bytes.Buffer
	tuiCommands.Write(&buf, ":save /tmp/x.alaya")

	tag, value, err := wiretest.NextFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}
	if tag != wire.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}

	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "save" {
		t.Errorf("name = %q, want %q", msg.Name, "save")
	}
	if msg.Args != "/tmp/x.alaya" {
		t.Errorf("args = %q, want %q", msg.Args, "/tmp/x.alaya")
	}
	if msg.CommandID == "" {
		t.Error("CI frame should carry a generated command ID")
	}
	if !strings.HasPrefix(msg.CommandID, "tui-") {
		t.Errorf("unexpected id format: %q", msg.CommandID)
	}
}

func TestWriteCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	tuiCommands.Write(&buf, ":cancel")

	tag, value, err := wiretest.NextFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}
	if tag != wire.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}
	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "cancel" || msg.Args != "" {
		t.Errorf("unexpected cmd: name=%q args=%q", msg.Name, msg.Args)
	}
}

func TestWriteCommand_CommandWithoutColon(t *testing.T) {
	var buf bytes.Buffer
	tuiCommands.Write(&buf, "model_set 3")

	tag, value, err := wiretest.NextFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}
	if tag != wire.TagCommandIn {
		t.Errorf("expected CI tag, got %s", tag)
	}
	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if msg.Name != "model_set" || msg.Args != "3" {
		t.Errorf("unexpected cmd: name=%q args=%q", msg.Name, msg.Args)
	}
}

// ============================================================================
// handleCommandOut — CO frame rendering
// ============================================================================

func TestCommandOut_ErrorRendersMessage(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		CommandID: "x1",
		IsError:   true,
		Result:    mustRaw(`{"code":"REQUEST_MODEL_UNKNOWN","message":"model_set: model not found: 99"}`),
	})
	if err := wire.WriteFrame(out, wire.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
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

func TestCommandOut_NoNameMappingSilent(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		CommandID: "x2",
		Result:    mustRaw(`{"path":"/tmp/x.alaya"}`),
	})
	if err := wire.WriteFrame(out, wire.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 0 {
		t.Fatalf("expected no window for silent command, got %d", len(windows))
	}
	if out.DrainDirty() {
		t.Error("silent CO should not mark the display dirty")
	}
}

func TestCommandOut_SuccessRendersByName(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	// The id comes from the CI this adapter sent, which is the only id a CO can
	// name: a command's name is in the request, never in its result.
	var ci bytes.Buffer
	id, err := tuiCommands.Write(&ci, ":save /tmp/x.alaya")
	if err != nil {
		t.Fatalf("send the CI the CO answers: %v", err)
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		CommandID: id,
		Result:    mustRaw(`{"path":"/tmp/x.alaya"}`),
	})
	if err := wire.WriteFrame(out, wire.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if !strings.Contains(windows[0].RawContent(), "Session saved to /tmp/x.alaya") {
		t.Errorf("success window should render the save result, got %q", windows[0].RawContent())
	}
	if strings.Contains(windows[0].RawContent(), "Command completed") {
		t.Errorf("generic confirmation should not appear for a known command, got %q", windows[0].RawContent())
	}
	// The id → name mapping is consumed by the CO.
	if _, ok := tuiCommands.Name(id); ok {
		t.Error("command name mapping should be consumed after the CO arrives")
	}
}

func TestCommandOut_UnknownCommandSilent(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	// A command this build does not know renders nothing, whatever it returns.
	var ci bytes.Buffer
	id, err := tuiCommands.Write(&ci, ":some_new_command")
	if err != nil {
		t.Fatalf("send the CI the CO answers: %v", err)
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		CommandID: id,
		Result:    mustRaw(`{"foo":"bar"}`),
	})
	if err := wire.WriteFrame(out, wire.TagCommandOut, string(payload)); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 0 {
		t.Fatalf("expected no window for silent command, got %d", len(windows))
	}
	if out.DrainDirty() {
		t.Error("silent CO should not mark the display dirty")
	}
}

func TestCommandOut_MalformedIgnored(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	if err := wire.WriteFrame(out, wire.TagCommandOut, "{not json"); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	windows := out.windowBuffer.AllWindows()
	if len(windows) != 0 {
		t.Errorf("malformed CO should be ignored, got %d windows", len(windows))
	}
}

func mustRaw(s string) json.RawMessage {
	return json.RawMessage(s)
}
