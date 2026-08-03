package terseio

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
	// :quit / :q are transport-level controls — clean exit, nothing sent.
	for _, input := range []string{":quit", ":q", ":quit\n", ":q\n"} {
		var buf bytes.Buffer
		err := readAllPrompt(&buf, strings.NewReader(input))
		if !errors.Is(err, errQuitPrompt) {
			t.Errorf("readAllPrompt(%q) error = %v, want errQuitPrompt", input, err)
		}
		if buf.Len() != 0 {
			t.Errorf("readAllPrompt(%q) wrote %d bytes, want 0", input, buf.Len())
		}
	}
}
