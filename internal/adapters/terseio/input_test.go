package terseio

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestReadAllPrompt_MultiLine(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader("line one\nline two\n\n"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2 (UT + UE), got %q", len(frames), buf.String())
	}
	if frames[0].tag != tlv.TagUserT {
		t.Errorf("frame[0] tag = %q, want %q", frames[0].tag, tlv.TagUserT)
	}
	// Trailing newlines trimmed, inner newlines preserved.
	if frames[0].value != "line one\nline two" {
		t.Errorf("prompt = %q, want %q", frames[0].value, "line one\nline two")
	}
	if frames[1].tag != tlv.TagUserEnd {
		t.Errorf("frame[1] tag = %q, want %q", frames[1].tag, tlv.TagUserEnd)
	}
}

func TestReadAllPrompt_SingleLineNoNewline(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader("single line"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}
	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 2 || frames[0].value != "single line" {
		t.Errorf("frames = %+v, want UT with %q + UE", frames, "single line")
	}
}

func TestReadAllPrompt_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	if err := readAllPrompt(&buf, strings.NewReader("\n\n")); err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty input produced %d bytes, want 0", buf.Len())
	}
}

func TestReadAllPrompt_Command(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader(":save /tmp/x.alaya\n"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1 (CI only, no UE)", len(frames))
	}
	if frames[0].tag != tlv.TagCommandIn {
		t.Errorf("frame[0] tag = %q, want %q", frames[0].tag, tlv.TagCommandIn)
	}

	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(frames[0].value), &cmd); err != nil {
		t.Fatalf("CI payload not JSON: %v", err)
	}
	if cmd.Name != commands.CommandNameSave {
		t.Errorf("cmd.Name = %q, want %q", cmd.Name, commands.CommandNameSave)
	}
	if cmd.Input != "/tmp/x.alaya" {
		t.Errorf("cmd.Input = %q, want %q", cmd.Input, "/tmp/x.alaya")
	}
	if !strings.HasPrefix(cmd.ID, "terse-") {
		t.Errorf("cmd.ID = %q, want terse- prefix", cmd.ID)
	}
	// The adapter must track the id → name mapping for CO correlation.
	if name, ok := commandNames.Load(cmd.ID); !ok || name != commands.CommandNameSave {
		t.Errorf("commandNames[%q] = %v, %v; want %q, true", cmd.ID, name, ok, commands.CommandNameSave)
	}
}

func TestReadAllPrompt_CommandMultiLine(t *testing.T) {
	// The WHOLE input is the command; a newline is just another separator
	// between the name and the argument text.
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader(":save\n/tmp/x.alaya\n"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 1 || frames[0].tag != tlv.TagCommandIn {
		t.Fatalf("frames = %+v, want 1 CI frame", frames)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(frames[0].value), &cmd); err != nil {
		t.Fatalf("CI payload not JSON: %v", err)
	}
	if cmd.Name != commands.CommandNameSave {
		t.Errorf("cmd.Name = %q, want %q", cmd.Name, commands.CommandNameSave)
	}
	if cmd.Input != "/tmp/x.alaya" {
		t.Errorf("cmd.Input = %q, want %q", cmd.Input, "/tmp/x.alaya")
	}
}

func TestReadAllPrompt_CommandNoArgs(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader(":continue\n"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 1 || frames[0].tag != tlv.TagCommandIn {
		t.Fatalf("frames = %+v, want 1 CI frame", frames)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(frames[0].value), &cmd); err != nil {
		t.Fatalf("CI payload not JSON: %v", err)
	}
	if cmd.Name != commands.CommandNameContinue || cmd.Input != "" {
		t.Errorf("cmd = %+v, want name %q with empty input", cmd, commands.CommandNameContinue)
	}
}

func TestReadAllPrompt_Quit(t *testing.T) {
	// :quit / :q stop reading and ask the session to end: one CI frame
	// naming the quit command, then errQuitPrompt (clean exit, code 0).
	for _, input := range []string{":quit", ":q", ":quit\n", ":q\n"} {
		var buf bytes.Buffer
		err := readAllPrompt(&buf, strings.NewReader(input))
		if !errors.Is(err, errQuitPrompt) {
			t.Errorf("readAllPrompt(%q) error = %v, want errQuitPrompt", input, err)
		}
		frames := parseTLVFrames(t, buf.Bytes())
		if len(frames) != 1 {
			t.Fatalf("readAllPrompt(%q) frames = %d, want 1 (CI)", input, len(frames))
		}
		if frames[0].tag != tlv.TagCommandIn {
			t.Errorf("readAllPrompt(%q) frame tag = %q, want %q", input, frames[0].tag, tlv.TagCommandIn)
		}
		var cmd protocol.CmdMsg
		if err := json.Unmarshal([]byte(frames[0].value), &cmd); err != nil {
			t.Fatalf("readAllPrompt(%q): CI payload not JSON: %v", input, err)
		}
		if cmd.Name != commands.CommandNameQuit || cmd.Input != "" {
			t.Errorf("readAllPrompt(%q) cmd = %+v, want name %q with empty input", input, cmd, commands.CommandNameQuit)
		}
	}
}

// An argument makes the line the session's command, not the local quit:
// ":quit now" is sent as a "quit" command carrying "now" (the session
// answers INVALID_ARGS), and it is not a clean exit.
func TestReadAllPrompt_QuitWithArgsIsSent(t *testing.T) {
	var buf bytes.Buffer
	if err := readAllPrompt(&buf, strings.NewReader(":quit now\n")); err != nil {
		t.Fatalf("readAllPrompt() error = %v, want nil", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 1 || frames[0].tag != tlv.TagCommandIn {
		t.Fatalf("frames = %+v, want 1 CI frame", frames)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(frames[0].value), &cmd); err != nil {
		t.Fatalf("CI payload not JSON: %v", err)
	}
	if cmd.Name != commands.CommandNameQuit || cmd.Input != "now" {
		t.Errorf("cmd = %+v, want name %q input %q", cmd, commands.CommandNameQuit, "now")
	}
}

// The feed never waits for the session: a prompt that arrives before MCP init
// has settled is held by the session, which is the only side that can tell
// whether refusing it would leave the client with no way to retry. Pinned
// behaviourally: nothing else has to happen for the frames to be written.
func TestReadAllPrompt_WritesWithoutWaiting(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- readAllPrompt(&buf, strings.NewReader("hello\n"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("readAllPrompt() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readAllPrompt() blocked: the feed must not wait for the session")
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 2 || frames[0].tag != tlv.TagUserT || frames[0].value != "hello" {
		t.Errorf("frames = %+v, want UT %q + UE", frames, "hello")
	}
}
