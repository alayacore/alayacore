package commands

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// sentCommand decodes the one CI frame a Write produced.
func sentCommand(t *testing.T, tag, value string) protocol.CmdMsg {
	t.Helper()
	if tag != wire.TagCommandIn {
		t.Fatalf("frame tag = %q, want %q", tag, wire.TagCommandIn)
	}
	var msg protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &msg); err != nil {
		t.Fatalf("CI body is not CmdMsg JSON: %v", err)
	}
	return msg
}

// TestWriterSendsTheDeclaredCommand pins the translation an adapter may not do
// for itself: the human-facing text — with or without its colon, in long or short
// form — reaches the wire as the name Defs declares.
func TestWriterSendsTheDeclaredCommand(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		wantName string
		wantArgs string
	}{
		{"colon and args", ":save /tmp/x.alaya", CommandNameSave, "/tmp/x.alaya"},
		{"no colon", "save /tmp/x.alaya", CommandNameSave, "/tmp/x.alaya"},
		{"no args", ":cancel", CommandNameCancel, ""},
		{"short form resolves", ":q", CommandNameQuit, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWriter("t")
			var dst strings.Builder
			id, err := w.Write(&dst, tc.cmd)
			if err != nil {
				t.Fatalf("Write(%q): %v", tc.cmd, err)
			}

			tag, value, err := wiretest.NextFrame(strings.NewReader(dst.String()))
			if err != nil {
				t.Fatalf("read the frame Write produced: %v", err)
			}
			msg := sentCommand(t, tag, value)
			if msg.Name != tc.wantName || msg.Args != tc.wantArgs {
				t.Errorf("cmd = %q, want name %q args %q", tc.cmd, tc.wantName, tc.wantArgs)
			}
			if msg.CommandID != id {
				t.Errorf("frame id = %q, Write returned %q", msg.CommandID, id)
			}
			if id == "" || id[0] != 't' {
				t.Errorf("id = %q, want the writer's prefix", id)
			}
		})
	}
}

// TestWriterCorrelatesTheIdOnce pins the table's contract: a CO names only the id,
// so the writer must still know the name when it arrives — and forget it
// afterwards, because a second CO for the same request answers nothing.
func TestWriterCorrelatesTheIdOnce(t *testing.T) {
	w := NewWriter("t")
	var dst strings.Builder
	id, err := w.Write(&dst, ":save /tmp/x")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if name, ok := w.Name(id); !ok || name != CommandNameSave {
		t.Fatalf("Name(%q) = %q, %v; want %q, true", id, name, ok, CommandNameSave)
	}
	if name, ok := w.Name(id); ok {
		t.Errorf("Name(%q) = %q on the second read, want it forgotten", id, name)
	}
	if _, ok := w.Name("someone-else-1"); ok {
		t.Error("Name of an id this writer never issued reported true")
	}
}

// errWriter is a stream that refuses every frame.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("stream is gone") }

// TestWriterForgetsAFrameItCouldNotSend pins the other side of the ordering: the
// name is recorded before the write (a CO cannot beat its CI), so a write that
// fails has to take it back — nothing will ever answer that frame.
func TestWriterForgetsAFrameItCouldNotSend(t *testing.T) {
	w := NewWriter("t")
	id, err := w.Write(errWriter{}, ":save /tmp/x")
	if err == nil {
		t.Fatal("Write to a failing stream returned no error")
	}
	if _, ok := w.Name(id); ok {
		t.Error("name kept for a frame that never reached the stream")
	}
}
