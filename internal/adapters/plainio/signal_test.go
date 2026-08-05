package plainio

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestHandleInterrupt(t *testing.T) {
	// Ctrl-C always sends a cancel frame (matching the terminal adapter's
	// Ctrl-G/:cancel) — the session decides whether there is something to
	// cancel.
	var buf bytes.Buffer
	if !handleInterrupt(&buf) {
		t.Fatal("expected a cancel frame to be sent")
	}

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("expected CI frame, got %s", tag)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &cmd); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if cmd.Name != "cancel" {
		t.Errorf("expected command 'cancel', got %q", cmd.Name)
	}
	if cmd.Input != "" {
		t.Errorf("expected empty input, got %q", cmd.Input)
	}
	if cmd.ID == "" {
		t.Error("CI frame should carry a generated call ID")
	}
}

func TestHandleInterrupt_WriteError(t *testing.T) {
	// A failing writer must not panic — the signal handler ignores write
	// errors (e.g. the input pipe is already closed).
	if handleInterrupt(&errorWriter{}) {
		t.Fatal("expected handleInterrupt to report failure on write error")
	}
}

// errorWriter fails every write.
type errorWriter struct{}

func (w *errorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("fake write error")
}
