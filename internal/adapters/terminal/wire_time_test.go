package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// TestWireTimeStampsTheWindow pins the adapter half of v14's message time: an
// authoritative frame's "time" becomes the window's CreatedAt, so a replayed
// message shows when it was actually sent rather than when this run read it.
func TestWireTimeStampsTheWindow(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.Write(encodeTestTLV(tlv.TagAssistantX, wiretest.AssistantText("7", "hello")))

	ws := out.windowBuffer.AllWindows()
	if len(ws) != 1 {
		t.Fatalf("windows = %d, want 1", len(ws))
	}
	if !ws[0].CreatedAt.Equal(wiretest.Stamp) {
		t.Errorf("CreatedAt = %v, want the wire time %v", ws[0].CreatedAt, wiretest.Stamp)
	}
}

// TestWireTimeStampsAUserEcho checks the same for a user echo, whose window is
// keyed by the first content item's id.
func TestWireTimeStampsAUserEcho(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.Write(encodeTestTLV(tlv.TagUserX, wiretest.UserText("3", "hi")))
	out.FlushPendingDeltas()

	ws := out.windowBuffer.AllWindows()
	if len(ws) != 1 {
		t.Fatalf("windows = %d, want 1", len(ws))
	}
	if !ws[0].CreatedAt.Equal(wiretest.Stamp) {
		t.Errorf("CreatedAt = %v, want the wire time %v", ws[0].CreatedAt, wiretest.Stamp)
	}
}

// TestDeltaLeavesTheReceiptClock pins the fallback: a delta carries no time, so
// the window keeps the adapter's receipt clock and does not fabricate one.
func TestDeltaLeavesTheReceiptClock(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.Write(encodeTestTLV(tlv.TagAssistantXDelta, wiretest.AssistantTextDelta("7", "he")))
	out.FlushPendingDeltas()

	ws := out.windowBuffer.AllWindows()
	if len(ws) != 1 {
		t.Fatalf("windows = %d, want 1", len(ws))
	}
	if ws[0].CreatedAt.IsZero() {
		t.Error("delta window has a zero CreatedAt; the receipt clock should have stamped it")
	}
	if ws[0].CreatedAt.Equal(wiretest.Stamp) {
		t.Error("delta window carries the wire time; deltas do not carry one")
	}
}
