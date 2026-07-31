package plainio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

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
	var buf bytes.Buffer

	// :quit should return errQuitPrompt immediately without any output
	input := strings.NewReader("some text\n:quit\nmore text\n")
	err := readPrompts(&buf, input)
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
