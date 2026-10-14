package lines

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

// maxTestLine is the bound the tests use where the bound itself is not the point.
const maxTestLine = 1024

// feed feeds each chunk in turn, the way a stream that arrives in pieces would.
func feed(sp *Splitter, chunks ...string) {
	for _, c := range chunks {
		sp.Feed([]byte(c))
	}
}

// next returns the next line as a string, failing the test on an unexpected error.
func next(t *testing.T, sp *Splitter) string {
	t.Helper()
	line, err := sp.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	return string(line)
}

func TestSplitter_Next(t *testing.T) {
	tests := []struct {
		name string
		feed []string
		want []string
	}{
		{name: "one line", feed: []string{"a\n"}, want: []string{"a"}},
		{name: "CRLF", feed: []string{"a\r\n"}, want: []string{"a"}},
		{name: "CR before the end is payload", feed: []string{"a\rb\n"}, want: []string{"a\rb"}},
		{name: "two lines in one feed", feed: []string{"a\nb\n"}, want: []string{"a", "b"}},
		{name: "line split across feeds", feed: []string{"ab", "c\nd\n"}, want: []string{"abc", "d"}},
		{name: "blank line", feed: []string{"\n"}, want: []string{""}},
		{name: "blank line between", feed: []string{"a\n\nb\n"}, want: []string{"a", "", "b"}},
		{name: "empty JSON object", feed: []string{"{}\n"}, want: []string{"{}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := NewSplitter(maxTestLine)
			feed(sp, tt.feed...)
			for i, want := range tt.want {
				if got := next(t, sp); got != want {
					t.Fatalf("line %d = %q, want %q", i, got, want)
				}
			}
			if _, err := sp.Next(); !errors.Is(err, ErrIncomplete) {
				t.Errorf("Next() past the last line = %v, want ErrIncomplete", err)
			}
			if got := sp.Pending(); got != 0 {
				t.Errorf("Pending() = %d after draining, want 0", got)
			}
		})
	}
}

// TestSplitter_Next_ByteAtATime checks the boundary is found regardless of how
// the bytes are cut up, which is the whole job of a splitter.
func TestSplitter_Next_ByteAtATime(t *testing.T) {
	const stream = "one\ntwo\r\n\nthree\n"
	want := []string{"one", "two", "", "three"}

	var got []string
	sp := NewSplitter(maxTestLine)
	for i := 0; i < len(stream); i++ {
		sp.Feed([]byte{stream[i]})
		for {
			line, err := sp.Next()
			if errors.Is(err, ErrIncomplete) {
				break
			}
			if err != nil {
				t.Fatalf("byte %d: Next() error = %v", i, err)
			}
			got = append(got, string(line))
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func TestSplitter_Next_LineAtMaxIsAccepted(t *testing.T) {
	// The bound counts the terminator, so "abc\n" is exactly 4 bytes.
	sp := NewSplitter(4)
	feed(sp, "abc\n")
	if got := next(t, sp); got != "abc" {
		t.Errorf("Next() = %q, want %q", got, "abc")
	}
}

func TestSplitter_Next_LinePastMaxIsRejected(t *testing.T) {
	sp := NewSplitter(4)
	feed(sp, "abcd") // four bytes and no terminator: no line can fit any more

	_, err := sp.Next()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("Next() error = %v, want ErrLineTooLong", err)
	}
	// The size it reports is the line's, not the buffer's: four bytes are buffered
	// and the line is already at least five, since it still needs a terminator.
	// Reporting four would print a size equal to the limit.
	if !strings.Contains(err.Error(), "5 bytes (limit 4)") {
		t.Errorf("error = %q, want it to report the line's length, not the buffer's", err)
	}
	// The bytes are left alone: a caller that asks again is told the same thing,
	// rather than being stepped past the line silently.
	if got := sp.Pending(); got != 4 {
		t.Errorf("Pending() = %d, want the 4 buffered bytes untouched", got)
	}
	if _, err := sp.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Errorf("second Next() error = %v, want the same ErrLineTooLong", err)
	}
}

// TestSplitter_Next_TerminatedLinePastMaxIsRejected covers the bound from the
// other side: a line that arrives already terminated, in one read. Checking only
// an unterminated prefix would let it through, and the buffer would have grown to
// hold the whole line before anyone looked.
func TestSplitter_Next_TerminatedLinePastMaxIsRejected(t *testing.T) {
	sp := NewSplitter(4)
	feed(sp, "abcde\n")

	_, err := sp.Next()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("Next() error = %v, want ErrLineTooLong", err)
	}
	if got := sp.Pending(); got != 6 {
		t.Errorf("Pending() = %d, want the 6 buffered bytes untouched", got)
	}
}

func TestSplitter_TakePending(t *testing.T) {
	sp := NewSplitter(maxTestLine)
	feed(sp, "tail")
	if got := string(sp.TakePending()); got != "tail" {
		t.Errorf("TakePending() = %q, want %q", got, "tail")
	}
	if got := sp.Pending(); got != 0 {
		t.Errorf("Pending() = %d after TakePending, want 0", got)
	}
	if got := sp.TakePending(); got != nil {
		t.Errorf("TakePending() on an empty buffer = %q, want nil", got)
	}
}

func TestSplitter_TakePending_StripsTrailingCR(t *testing.T) {
	sp := NewSplitter(maxTestLine)
	feed(sp, "tail\r")
	if got := string(sp.TakePending()); got != "tail" {
		t.Errorf("TakePending() = %q, want %q", got, "tail")
	}
}

func TestNewSplitter_PanicsOnNonPositiveMax(t *testing.T) {
	for _, max := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewSplitter(%d) did not panic", max)
				}
			}()
			NewSplitter(max)
		}()
	}
}

// TestSplitter_Next_DropsOversizedArray checks that one huge line does not leave
// its array pinned for the life of the stream: the remainder moves to a buffer
// of its own size.
func TestSplitter_Next_DropsOversizedArray(t *testing.T) {
	const bigLen = 256 * 1024

	sp := NewSplitter(1 << 20)
	feed(sp, strings.Repeat("x", bigLen)+"\n", "b\n")

	if got := len(next(t, sp)); got != bigLen {
		t.Fatalf("length of the big line = %d, want %d", got, bigLen)
	}
	if got := cap(sp.buf); got > maxTestLine {
		t.Errorf("cap(buf) = %d after consuming a %d-byte line: its array outlived it", got, bigLen)
	}
	if got := next(t, sp); got != "b" {
		t.Errorf("line after the big one = %q, want %q", got, "b")
	}
}

func TestReader_ReadLine(t *testing.T) {
	r := NewReader(strings.NewReader("a\nb\r\nc\n"), maxTestLine)
	for i, want := range []string{"a", "b", "c"} {
		line, err := r.ReadLine()
		if err != nil {
			t.Fatalf("line %d: ReadLine() error = %v", i, err)
		}
		if string(line) != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
		}
	}
	if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
		t.Errorf("after the last line ReadLine() error = %v, want io.EOF", err)
	}
}

// TestReader_LineCompletesAtEOF covers a read that hands back data and io.EOF
// together — common on a stream — where the line is still a line.
func TestReader_LineCompletesAtEOF(t *testing.T) {
	r := NewReader(&dataAndEOFReader{data: []byte("a\nb\n")}, maxTestLine)
	for i, want := range []string{"a", "b"} {
		line, err := r.ReadLine()
		if err != nil {
			t.Fatalf("line %d: ReadLine() error = %v", i, err)
		}
		if string(line) != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
		}
	}
	if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
		t.Errorf("at the end ReadLine() error = %v, want io.EOF", err)
	}
}

// TestReader_UnterminatedTail covers both answers to what a missing final
// terminator means: a final line (what bufio.Scanner does, and what MCP and SSE
// relied on) or an error (what the frame protocol needs).
func TestReader_UnterminatedTail(t *testing.T) {
	t.Run("lenient by default", func(t *testing.T) {
		r := NewReader(strings.NewReader("a\nb"), maxTestLine)
		if line, err := r.ReadLine(); err != nil || string(line) != "a" {
			t.Fatalf("first ReadLine() = %q, %v", line, err)
		}
		line, err := r.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine() of the tail error = %v, want the tail as a line", err)
		}
		if string(line) != "b" {
			t.Errorf("tail = %q, want %q", line, "b")
		}
		if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
			t.Errorf("after the tail ReadLine() error = %v, want io.EOF", err)
		}
	})

	t.Run("terminal when required", func(t *testing.T) {
		r := NewReader(strings.NewReader("a\nb"), maxTestLine)
		r.RequireTerminator = true
		if line, err := r.ReadLine(); err != nil || string(line) != "a" {
			t.Fatalf("first ReadLine() = %q, %v", line, err)
		}
		_, err := r.ReadLine()
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("ReadLine() of the unterminated tail = %v, want an error that is not io.EOF", err)
		}
		// Stable: the bytes are not consumed, so asking again says the same thing.
		if _, again := r.ReadLine(); again == nil || again.Error() != err.Error() {
			t.Errorf("second ReadLine() = %v, want the same error as %v", again, err)
		}
	})
}

func TestReader_LineTooLong(t *testing.T) {
	r := NewReader(strings.NewReader("abcd"), 4)
	if _, err := r.ReadLine(); !errors.Is(err, ErrLineTooLong) {
		t.Errorf("ReadLine() error = %v, want ErrLineTooLong", err)
	}
}

func TestReader_PropagatesReadError(t *testing.T) {
	sentinel := errors.New("read failed")
	r := NewReader(failingReader{err: sentinel}, maxTestLine)
	if _, err := r.ReadLine(); !errors.Is(err, sentinel) {
		t.Errorf("ReadLine() error = %v, want %v", err, sentinel)
	}
}

// failingReader fails every read, standing in for a broken pipe.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// dataAndEOFReader hands back its whole payload together with io.EOF.
type dataAndEOFReader struct{ data []byte }

func (d *dataAndEOFReader) Read(p []byte) (int, error) {
	if len(d.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, d.data)
	d.data = d.data[n:]
	return n, io.EOF
}
