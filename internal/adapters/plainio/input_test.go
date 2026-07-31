package plainio

import (
	"bytes"
	"encoding/json"
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
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

	// :quit should return nil immediately without any output
	input := strings.NewReader("some text\n:quit\nmore text\n")
	err := readPrompts(nil, &buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	err := readPrompts(nil, &buf, input)
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

	err := readPrompts(nil, &buf, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestReadPrompts_DoneDiscardsBufferedLine(t *testing.T) {
	// When done is closed, readPrompts should discard any line that was
	// already buffered in bufio.Reader and return nil.
	t.Run("closed before any read", func(t *testing.T) {
		var buf bytes.Buffer
		input := strings.NewReader("should be discarded\n")
		done := make(chan struct{})
		close(done)

		err := readPrompts(done, &buf, input)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if buf.Len() > 0 {
			t.Errorf("expected no output when done is already closed, got %d bytes", buf.Len())
		}
	})

	t.Run("closed after partial read", func(t *testing.T) {
		var buf bytes.Buffer
		pr, pw := io.Pipe()

		done := make(chan struct{})

		// Start reading in a goroutine (simulates real usage).
		errCh := make(chan error, 1)
		go func() {
			errCh <- readPrompts(done, &buf, pr)
		}()

		// Write one full line and give it time to be consumed.
		_, err := pw.Write([]byte("first\n"))
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		// Close done while there's more data pending in the pipe.
		close(done)
		// Close the pipe writer to unblock ReadString (same role as
		// os.Stdin.Close() in the real adapter).
		_ = pw.Close()

		err = <-errCh
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}

		// "first" should have been emitted with UE.
		tag, value, err := tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("failed to read UT: %v", err)
		}
		if tag != tlv.TagUserT {
			t.Errorf("expected UT, got %s", tag)
		}
		if value != "first" {
			t.Errorf("expected 'first', got %q", value)
		}
		tag, _, err = tlv.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("failed to read UE: %v", err)
		}
		if tag != tlv.TagUserEnd {
			t.Errorf("expected UE, got %s", tag)
		}
		// No more data — subsequent lines are discarded.
		if buf.Len() > 0 {
			t.Errorf("expected no more data after done closed, got %d bytes", buf.Len())
		}
	})
}

// errorReader returns an error on every read
type errorReader struct{}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
