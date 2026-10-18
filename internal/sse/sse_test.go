package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// collect reads every event from src and returns them with the reader's error.
func collect(t *testing.T, src string, limit int) ([]Event, error) {
	t.Helper()
	r := NewReader(strings.NewReader(src), limit)
	var got []Event
	for r.Next() {
		got = append(got, r.Event())
	}
	return got, r.Err()
}

func TestReader(t *testing.T) {
	const limit = 1 << 10

	cases := []struct {
		name string
		src  string
		want []Event
	}{
		{
			name: "typed event",
			src:  "event: message\ndata: {\"a\":1}\n\n",
			want: []Event{{Type: "message", Data: `{"a":1}`}},
		},
		{
			name: "untyped event (OpenAI's data-only format)",
			src:  "data: {\"a\":1}\n\n",
			want: []Event{{Data: `{"a":1}`}},
		},
		{
			// The spec lets one event's payload span lines; a reader that
			// bounded the line instead of the event failed a tool call that
			// arrived this way, which is why this is a case.
			name: "multi-line data joins with LF",
			src:  "data: {\"a\":1,\ndata: \"b\":2}\n\n",
			want: []Event{{Data: "{\"a\":1,\n\"b\":2}"}},
		},
		{
			name: "field name without the optional space",
			src:  "event:message\ndata:hello\n\n",
			want: []Event{{Type: "message", Data: "hello"}},
		},
		{
			name: "comments and unknown fields are ignored",
			src:  ":keep-alive\nid: 7\nretry: 100\nevent: message\ndata: x\n\n",
			want: []Event{{Type: "message", Data: "x"}},
		},
		{
			name: "crlf line endings",
			src:  "event: message\r\ndata: x\r\n\r\n",
			want: []Event{{Type: "message", Data: "x"}},
		},
		{
			name: "empty data value",
			src:  "data:\n\n",
			want: []Event{{Data: ""}},
		},
		{
			// A keep-alive carries no payload, and returning it as an event
			// would put an empty message in front of every caller.
			name: "event with no data is not an event",
			src:  "event: ping\n\nevent: message\ndata: x\n\n",
			want: []Event{{Type: "message", Data: "x"}},
		},
		{
			name: "consecutive blank lines",
			src:  "\n\n\ndata: x\n\n\n\n",
			want: []Event{{Data: "x"}},
		},
		{
			name: "repeated event field overwrites",
			src:  "event: a\nevent: b\ndata: x\n\n",
			want: []Event{{Type: "b", Data: "x"}},
		},
		{
			// The bytes arrived, so they are handed over: a caller that cannot
			// use a truncated payload says so itself (see the package doc).
			name: "EOF inside an event delivers it",
			src:  "event: message\ndata: x",
			want: []Event{{Type: "message", Data: "x"}},
		},
		{
			name: "an empty stream has no events",
			src:  "",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collect(t, tc.src, limit)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("events = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %#v, want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestReaderBoundOnTheEvent pins the bound's unit. A line limit alone lets a peer
// grow one event one small line at a time until the process runs out of memory;
// this is the case that used to pass every per-line check while doing exactly
// that.
func TestReaderBoundOnTheEvent(t *testing.T) {
	const limit = 4 << 10
	var src strings.Builder
	for range 4 * (limit / 16) {
		src.WriteString("data: " + strings.Repeat("x", 10) + "\n")
	}
	src.WriteString("\n")

	got, err := collect(t, src.String(), limit)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge (events read: %d)", err, len(got))
	}
}

// TestReaderBoundOnTheLine pins the other half: a stream that never sends a
// terminator cannot grow the buffer without bound either.
func TestReaderBoundOnTheLine(t *testing.T) {
	src := "data: " + strings.Repeat("x", 2<<10)

	_, err := collect(t, src, 1<<10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// TestReaderAcceptsOneLargeEvent pins what the bound must not reject. A provider
// may send a whole tool call atomically — one large payload on a single line —
// and that is legal and must be read: the bound is on the decoded event, not on
// the line. (The bufio.Scanner this reader replaced had a fixed line buffer and
// rejected exactly this.) The same total split across several data: lines is
// one event, so the answer cannot depend on how the sender broke its lines.
func TestReaderAcceptsOneLargeEvent(t *testing.T) {
	const limit = 8 << 20

	atomic := strings.Repeat("x", 2<<20) // past any per-line cap that was ever here
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"atomic", "data: " + atomic + "\n\n", 2 << 20},
		{"across lines", "data: " + atomic + "\ndata: " + atomic + "\n\n", 4<<20 + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collect(t, tc.src, limit)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(got) != 1 {
				t.Fatalf("events = %d, want 1", len(got))
			}
			if len(got[0].Data) != tc.want {
				t.Errorf("data = %d bytes, want %d", len(got[0].Data), tc.want)
			}
		})
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

// TestReaderFatalErrorDropsPendingEvent pins the other side of the EOF rule: a
// transport error is not a clean end, so a half-read event is refused rather
// than handed over as if the stream had finished it. The reader then stays
// stopped.
func TestReaderFatalErrorDropsPendingEvent(t *testing.T) {
	boom := errors.New("connection reset")
	r := NewReader(&failAfterReader{
		data: []byte("data: {\"a\":1}\n"), // a complete line, no blank line
		err:  boom,
	}, 1<<10)

	if r.Next() {
		t.Fatalf("pending event was emitted after a transport error: %q", r.Event().Data)
	}
	if !errors.Is(r.Err(), boom) {
		t.Fatalf("Err() = %v, want %v", r.Err(), boom)
	}
	if r.Next() {
		t.Fatal("reader restarted after an error")
	}
}

// countingReader records how many bytes the reader pulled from the stream.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// TestReaderStaysStoppedAfterSizeError pins that a size error is terminal. The
// stream is still healthy here — the error came from the event cap, not from
// I/O — so without a sticky error the next call would keep consuming it.
func TestReaderStaysStoppedAfterSizeError(t *testing.T) {
	body := "data: " + strings.Repeat("x", 2<<10) + "\n\n" + // over a 1KB cap
		`data: {"ok":true}` + "\n\n"

	cr := &countingReader{r: strings.NewReader(body)}
	r := NewReader(cr, 1<<10)
	for r.Next() {
	}
	if r.Err() == nil {
		t.Fatal("expected a size error")
	}

	readAtError := cr.n
	if r.Next() {
		t.Fatal("reader yielded an event after erroring")
	}
	if cr.n != readAtError {
		t.Errorf("reader consumed %d more bytes after the error; it must stay stopped", cr.n-readAtError)
	}
}
