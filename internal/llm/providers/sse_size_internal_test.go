package providers

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// A provider may send a whole tool call atomically, i.e. one large payload on
// a single SSE line. That is legal and must be read: the bound is on the
// decoded event, not on the line. (The old bufio.Scanner with a 1MB line
// buffer rejected exactly this.)
func TestSSEScannerLargeAtomicEventIsAccepted(t *testing.T) {
	payload := strings.Repeat("x", 2<<20) // 2MB, past any per-line cap we had

	t.Run("openai", func(t *testing.T) {
		body := `data: {"choices":[{"delta":{"content":"` + payload + `"}}]}` + "\n\n"
		s := newOpenAIScanner(strings.NewReader(body))
		if !s.Next() {
			t.Fatalf("2MB atomic event was rejected: %v", s.Err())
		}
		if !strings.Contains(s.Data(), payload) {
			t.Fatal("event data was not returned intact")
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		body := "event: content_block_delta\n" +
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"` + payload + `"}}` + "\n\n"
		s := newAnthropicScanner(strings.NewReader(body))
		if !s.Next() {
			t.Fatalf("2MB atomic event was rejected: %v", s.Err())
		}
		if _, data := s.Event(); !strings.Contains(data, payload) {
			t.Fatal("event data was not returned intact")
		}
	})
}

// The same total payload split across several data: lines is still one event.
// The bound must not depend on how the provider chose to break the lines.
func TestSSEScannerLargeEventAcrossLinesIsAccepted(t *testing.T) {
	chunk := strings.Repeat("y", 1<<20) // 3 × 1MB data lines = 3MB event
	body := "data: " + chunk + "\n" +
		"data: " + chunk + "\n" +
		"data: " + chunk + "\n\n"

	s := newOpenAIScanner(strings.NewReader(body))
	if !s.Next() {
		t.Fatalf("multi-line event was rejected: %v", s.Err())
	}
	if got := len(s.Data()); got < 3<<20 {
		t.Fatalf("event data = %d bytes, want >= %d", got, 3<<20)
	}
}

// Past the event bound the reader must say so and name the cause, rather than
// surfacing a raw bufio error or silently yielding partial data.
func TestSSEScannerOversizedEventIsRejected(t *testing.T) {
	huge := strings.Repeat("x", (maxEventBytes>>20+1)<<20) // just over the cap

	t.Run("openai", func(t *testing.T) {
		body := `data: {"choices":[{"delta":{"content":"` + huge + `"}}]}` + "\n\n"
		s := newOpenAIScanner(strings.NewReader(body))
		for s.Next() {
		}
		assertTooLarge(t, s.Err())
		if s.Next() {
			t.Fatal("scanner restarted after the size error")
		}
	})

	t.Run("openai across lines", func(t *testing.T) {
		chunk := strings.Repeat("x", 1<<20) // many small lines summing over the cap
		body := strings.Repeat("data: "+chunk+"\n", maxEventBytes>>20+2)
		s := newOpenAIScanner(strings.NewReader(body))
		for s.Next() {
		}
		assertTooLarge(t, s.Err())
		if s.Next() {
			t.Fatal("scanner restarted after the size error")
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		body := "data: {\"text\":\"" + huge + "\"}\n\n"
		s := newAnthropicScanner(strings.NewReader(body))
		for s.Next() {
		}
		assertTooLarge(t, s.Err())
		if s.Next() {
			t.Fatal("scanner restarted after the size error")
		}
	})
}

func assertTooLarge(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("oversized event was accepted")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error = %v, want it to name the size limit", err)
	}
}

// An event that is complete but merely missing its terminating blank line is
// still drained at EOF — a clean end, just truncated.
func TestSSEScannerDrainsPendingEventAtEOF(t *testing.T) {
	s := newOpenAIScanner(strings.NewReader(`data: {"a":1}`)) // no blank line
	if !s.Next() {
		t.Fatalf("pending event at EOF was dropped: %v", s.Err())
	}
	if got := s.Data(); got != `{"a":1}` {
		t.Fatalf("data = %q, want %q", got, `{"a":1}`)
	}
	if s.Next() {
		t.Fatal("expected end of stream")
	}
	if s.Err() != nil {
		t.Fatalf("EOF must not be an error: %v", s.Err())
	}
}

// failAfterReader yields data, then a permanent error on the next read.
type failAfterReader struct {
	data []byte
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

// A transport error is not a clean end: the pending event is refused rather
// than emitted as if complete, and the scanner then stays stopped.
func TestSSEScannerFatalErrorDropsPendingEvent(t *testing.T) {
	boom := errors.New("connection reset")
	s := newOpenAIScanner(&failAfterReader{
		data: []byte("data: {\"a\":1}\n"), // a complete line, no blank line
		err:  boom,
	})
	if s.Next() {
		t.Fatalf("pending event was emitted after a transport error: %q", s.Data())
	}
	if !errors.Is(s.Err(), boom) {
		t.Fatalf("Err() = %v, want %v", s.Err(), boom)
	}
	if s.Next() {
		t.Fatal("scanner restarted after an error")
	}
}

// countingReader records how many bytes the scanner pulled from the stream.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// After a size error the scanner must stay stopped. The reader is still
// healthy here (the error came from the event cap, not from I/O), so without
// a sticky error the next call would happily keep consuming the stream.
func TestSSEScannerStaysStoppedAfterSizeError(t *testing.T) {
	// An oversized event, then a perfectly ordinary one after it.
	body := "data: " + strings.Repeat("x", 9<<20) + "\n\n" +
		`data: {"ok":true}` + "\n\n"

	cr := &countingReader{r: strings.NewReader(body)}
	s := newOpenAIScanner(cr)
	for s.Next() {
	}
	if s.Err() == nil {
		t.Fatal("expected a size error")
	}

	readAtError := cr.n
	if s.Next() {
		t.Fatal("scanner yielded an event after erroring")
	}
	if cr.n != readAtError {
		t.Errorf("scanner read %d more bytes after the error; it must stay stopped", cr.n-readAtError)
	}
}
