package plainio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestReadPrompts_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "hello") followed by UE
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected value 'hello', got %q", value)
	}
	// UE
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}
}

func TestReadPrompts_MultiLineBackslash(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("first line\\\nsecond line\\\nthird line\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "first line\nsecond line\nthird line") followed by UE
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "first line\nsecond line\nthird line" {
		t.Errorf("expected multi-line value, got %q", value)
	}
	// UE
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}
}

func TestReadPrompts_TrailingBackslash(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\\\n")
	// Trailing backslash at EOF with no continuation — the backslash
	// is consumed, leaving "hello" as the accumulated text.

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "hello") followed by UE
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected 'hello', got %q", value)
	}
	// UE
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}
}

func TestReadPrompts_MultipleLines(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("first\nsecond\nthird\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each prompt is followed by UE.
	for i, expected := range []string{"first", "second", "third"} {
		tag, value, err := tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read TLV: %v", i, err)
		}
		if tag != tlv.TagUserT {
			t.Errorf("prompt %d: expected tag UT, got %s", i, tag)
		}
		if value != expected {
			t.Errorf("prompt %d: expected %q, got %q", i, expected, value)
		}
		// UE
		tag, _, err = tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read UE TLV: %v", i, err)
		}
		if tag != tlv.TagUserEnd {
			t.Errorf("prompt %d: expected UE tag, got %s", i, tag)
		}
	}
}

func TestReadPrompts_EmptyLines(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\n\n\nworld\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each prompt is followed by UE.
	for i, expected := range []string{"hello", "world"} {
		tag, value, err := tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read TLV: %v", i, err)
		}
		if tag != tlv.TagUserT {
			t.Errorf("prompt %d: expected tag UT, got %s", i, tag)
		}
		if value != expected {
			t.Errorf("prompt %d: expected %q, got %q", i, expected, value)
		}
		// UE
		tag, _, err = tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read UE TLV: %v", i, err)
		}
		if tag != tlv.TagUserEnd {
			t.Errorf("prompt %d: expected UE tag, got %s", i, tag)
		}
	}

	// Should be no more data
	if buf.Len() > 0 {
		t.Errorf("expected no more data after last prompt, got %d bytes", buf.Len())
	}
}

func TestReadPrompts_EOFPartialLine(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("partial prompt")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Partial prompt on EOF should also flush with UE.
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "partial prompt" {
		t.Errorf("expected 'partial prompt', got %q", value)
	}
	// UE
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}
}

func TestReadPrompts_Command(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader(":cancel\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Commands should emit a CI frame (not UT), with no UE after it.
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Errorf("expected tag CI, got %s", tag)
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

	// Should be no UE after command
	tag, _, err = tlv.ReadTLV(&buf)
	if err == nil {
		t.Errorf("expected EOF after command, got tag %s", tag)
	}
}

func TestReadPrompts_CommandWithArgs(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader(":save /tmp/x.alaya\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Errorf("expected tag CI, got %s", tag)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &cmd); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if cmd.Name != "save" || cmd.Input != "/tmp/x.alaya" {
		t.Errorf("unexpected cmd: name=%q input=%q", cmd.Name, cmd.Input)
	}
}

func TestReadPrompts_QuitCommand(t *testing.T) {
	for _, word := range []string{"quit", "q"} {
		var buf bytes.Buffer

		// :quit stops reading and asks the session to end: the reader
		// sees errQuitPrompt (a clean exit, code 0) and the session
		// receives a quit command.
		input := strings.NewReader("some text\n:" + word + "\nmore text\n")
		err := readPrompts(&buf, input)
		if !errors.Is(err, errQuitPrompt) {
			t.Fatalf(":%s: expected errQuitPrompt, got %v", word, err)
		}

		// Only "some text" should be emitted as a prompt
		tag, value, err := tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf(":%s: failed to read TLV: %v", word, err)
		}
		if tag != tlv.TagUserT {
			t.Errorf(":%s: expected tag UT, got %s", word, tag)
		}
		if value != "some text" {
			t.Errorf(":%s: expected 'some text', got %q", word, value)
		}

		// UE after first prompt
		tag, _, err = tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf(":%s: failed to read UE TLV: %v", word, err)
		}
		if tag != tlv.TagUserEnd {
			t.Errorf(":%s: expected UE tag, got %s", word, tag)
		}

		assertQuitCommand(t, &buf, word)

		// Should be no more data
		if buf.Len() > 0 {
			t.Errorf(":%s: expected no more data after :quit, got %d bytes", word, buf.Len())
		}
	}
}

// An argument makes the line the session's command, not the local quit:
// ":quit foo" is sent as a "quit" command carrying "foo" (the session
// answers INVALID_ARGS) and reading continues to EOF.
func TestReadPrompts_QuitWithArgsIsNotLocalQuit(t *testing.T) {
	var buf bytes.Buffer
	if err := readPrompts(&buf, strings.NewReader(":quit foo\n")); err != nil {
		t.Fatalf("readPrompts() error = %v, want nil (EOF)", err)
	}

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("tag = %s, want CI", tag)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &cmd); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if cmd.Name != commands.CommandNameQuit || cmd.Input != "foo" {
		t.Errorf("cmd = %+v, want name %q input %q", cmd, commands.CommandNameQuit, "foo")
	}
	if tag, _, err := tlv.ReadTLV(&buf); err == nil {
		t.Errorf("expected EOF after the command, got tag %s", tag)
	}
}

// assertQuitCommand reads one frame and asserts it is a CI naming the
// session's quit command of the user's typed word, with no arguments.
func assertQuitCommand(t *testing.T, buf *bytes.Buffer, typed string) {
	t.Helper()
	tag, value, err := tlv.ReadTLV(buf)
	if err != nil {
		t.Fatalf(":%s: failed to read quit CI frame: %v", typed, err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf(":%s: quit frame tag = %s, want CI", typed, tag)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &cmd); err != nil {
		t.Fatalf(":%s: CI payload is not CmdMsg JSON: %v", typed, err)
	}
	if cmd.Name != commands.CommandNameQuit {
		t.Errorf(":%s: quit command name = %q, want %q", typed, cmd.Name, commands.CommandNameQuit)
	}
	if cmd.Input != "" {
		t.Errorf(":%s: quit command input = %q, want empty", typed, cmd.Input)
	}
	if cmd.ID == "" {
		t.Errorf(":%s: quit CI frame should carry a generated call ID", typed)
	}
}

func TestReadPrompts_BackslashThenEOF(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\\\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The backslash consumed the newline, "hello" is the accumulated text.
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected 'hello', got %q", value)
	}
	// UE
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}
}

func TestReadPrompts_ReturnsEOFError(t *testing.T) {
	var buf bytes.Buffer
	input := &errorReader{}

	err := readPrompts(&buf, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// errorReader returns an error on every read
type errorReader struct{}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// The feed never waits for the session: a prompt that arrives before MCP init
// has settled is held by the session, which is the only side that can tell
// whether refusing it would leave the client with no way to retry. Pinned
// behaviourally: nothing else has to happen for the frames to be written.
func TestReadPrompts_WritesWithoutWaiting(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- readPrompts(&buf, strings.NewReader("hello\n"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("readPrompts() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readPrompts() blocked: the feed must not wait for the session")
	}

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("read the prompt frame: %v", err)
	}
	if tag != tlv.TagUserT || value != "hello" {
		t.Errorf("first frame = %s %q, want UT %q", tag, value, "hello")
	}
}
