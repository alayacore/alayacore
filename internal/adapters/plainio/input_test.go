package plainio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestReadPrompts_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\n")

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
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
	var buf bytes.Buffer

	// :quit should return errQuitPrompt immediately without any output
	input := strings.NewReader("some text\n:quit\nmore text\n")
	err := readPrompts(&buf, input, nil)
	if !errors.Is(err, errQuitPrompt) {
		t.Fatalf("expected errQuitPrompt, got %v", err)
	}

	// Only "some text" should be emitted
	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "some text" {
		t.Errorf("expected 'some text', got %q", value)
	}

	// UE after first prompt
	tag, _, err = tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read UE TLV: %v", err)
	}
	if tag != tlv.TagUserEnd {
		t.Errorf("expected UE tag, got %s", tag)
	}

	// Should be no more data
	if buf.Len() > 0 {
		t.Errorf("expected no more data after :quit, got %d bytes", buf.Len())
	}
}

func TestReadPrompts_BackslashThenEOF(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\\\n")

	err := readPrompts(&buf, input, nil)
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

	err := readPrompts(&buf, input, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// errorReader returns an error on every read
type errorReader struct{}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// frameCapture collects TLV frames written by readPrompts from a
// goroutine; a channel is used because the test reads while the reader
// goroutine writes.
type frameCapture struct{ ch chan string }

func (c *frameCapture) Write(p []byte) (int, error) {
	c.ch <- string(p)
	return len(p), nil
}

// TestReadPrompts_GateBlocksPromptButNotCommand pins the contract that
// makes a piped prompt safe: a prompt waits for the ready gate, while a
// command is emitted immediately so a user can still steer an
// initializing session (:mcp_cancel) or leave (:quit).
func TestReadPrompts_GateBlocksPromptButNotCommand(t *testing.T) {
	cap := &frameCapture{ch: make(chan string, 8)}
	released := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- readPrompts(cap, strings.NewReader(":cancel\nhello\n"), func() error {
			<-released
			return nil
		})
	}()

	// The command frame is written while the gate is still blocking —
	// proof that commands bypass it.
	select {
	case frame := <-cap.ch:
		if tag := frame[:2]; tag != tlv.TagCommandIn {
			t.Fatalf("first frame tag = %q, want CI (command bypasses the gate)", tag)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command frame was not emitted before the gate released")
	}

	// The prompt is blocked at the gate: nothing else is written.
	select {
	case frame := <-cap.ch:
		t.Fatalf("prompt written before the gate released: %q", frame)
	case <-time.After(50 * time.Millisecond):
	}

	close(released)

	// Now UT then UE.
	for _, want := range []string{tlv.TagUserT, tlv.TagUserEnd} {
		select {
		case frame := <-cap.ch:
			if tag := frame[:2]; tag != want {
				t.Fatalf("frame tag = %q, want %q", tag, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %q not written after the gate released", want)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("readPrompts() error = %v", err)
	}
}

// A gate error stops the feed without writing the prompt.
func TestReadPrompts_GateErrorStopsFeed(t *testing.T) {
	var buf bytes.Buffer
	gateErr := errors.New("session closed")

	err := readPrompts(&buf, strings.NewReader("hello\n"), func() error { return gateErr })
	if !errors.Is(err, gateErr) {
		t.Fatalf("readPrompts() error = %v, want %v", err, gateErr)
	}
	if buf.Len() != 0 {
		t.Errorf("prompt written despite gate error: %q", buf.String())
	}
}
