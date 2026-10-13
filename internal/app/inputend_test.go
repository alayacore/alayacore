package app

// SendInputEnd is the adapter's half-close: the frame it sends when its own
// input is exhausted. The tests here pin the two properties the session relies
// on — the exact frame, and that writing it does not close the stream.

import (
	"bytes"
	"testing"

	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

func TestSendInputEnd_WritesTheFrame(t *testing.T) {
	var buf bytes.Buffer

	SendInputEnd(&buf)

	tag, value, err := wiretest.NextFrame(&buf)
	if err != nil {
		t.Fatalf("read the frame: %v", err)
	}
	if tag != wire.TagInputEnd {
		t.Errorf("tag = %q, want %q", tag, wire.TagInputEnd)
	}
	if value != "" {
		t.Errorf("value = %q, want empty", value)
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes after the frame, want none", buf.Len())
	}
}

// The stream stays usable: the frame says "no more input", not "the stream is
// closed". A command may still follow it (an OAuth callback's :mcp_confirm
// arrives on this same writer, after the reader is done).
func TestSendInputEnd_LeavesTheStreamUsable(t *testing.T) {
	var buf bytes.Buffer

	SendInputEnd(&buf)
	if err := wire.WriteFrame(&buf, wire.TagCommandIn, `{"id":"c1","name":"mcp_confirm"}`); err != nil {
		t.Fatalf("write a command after CE: %v", err)
	}

	if tag, _, err := wiretest.NextFrame(&buf); err != nil || tag != wire.TagInputEnd {
		t.Fatalf("first frame = %q, %v; want %q", tag, err, wire.TagInputEnd)
	}
	tag, value, err := wiretest.NextFrame(&buf)
	if err != nil {
		t.Fatalf("read the command frame: %v", err)
	}
	if tag != wire.TagCommandIn {
		t.Errorf("second frame tag = %q, want %q", tag, wire.TagCommandIn)
	}
	if value == "" {
		t.Error("the command frame should have carried its JSON payload")
	}
}
