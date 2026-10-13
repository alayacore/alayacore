// Package lines splits a byte stream into lines.
//
// Three streams in AlayaCore are newline-delimited, and each had grown its own
// splitter: the frame protocol (a hand-rolled parse loop per adapter), the MCP
// transports (a bufio.Scanner with an explicit buffer) and the SSE reader (a
// bufio.Reader loop). They agree on what a line *is*, so they share one
// implementation here: one place decides where a boundary falls, which is what
// keeps a stream from being read two different ways.
//
// A line ends at the first LF. The line handed back does not include that LF,
// nor a CR immediately before it, so a writer that terminates with CRLF on a
// platform that translates line endings reads the same as one that writes LF. A
// CR anywhere else is left alone: it is the caller's payload, and stripping it
// here would corrupt a line this package has no business understanding.
//
// What a *missing* terminator means is the caller's decision, not this package's.
// At the end of a stream with bytes left over, MCP and SSE hand those bytes over
// as a final line — bufio.Scanner's behavior, which both relied on — while the
// frame protocol must report a truncated frame rather than guess at one. Set
// Reader.RequireTerminator for the latter. A blank line is likewise policy-free
// here: the splitter returns it, and the frame decoder is what rejects it,
// because only the frame format knows a blank line is never a frame.
//
// A Splitter and a Reader are not safe for concurrent use. A caller that feeds
// them from more than one goroutine owns the synchronization; the adapters take
// the lock that already covered the bytes they used to buffer themselves.
package lines

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// chunkSize is how much a Reader asks for at once.
const chunkSize = 64 * 1024

// ErrIncomplete reports that the buffered bytes hold no complete line yet. It is
// not a failure: the caller feeds more bytes and asks again.
var ErrIncomplete = errors.New("lines: incomplete line")

// ErrLineTooLong reports that the buffered bytes cannot be a line: they reached
// the maximum without a terminator, so the line would exceed it.
var ErrLineTooLong = errors.New("lines: line too long")

// Splitter turns bytes that arrive in pieces into lines (push style).
type Splitter struct {
	buf []byte
	// limit bounds one line *including* its terminator — the bytes this splitter
	// must hold at once, which is the quantity that must not grow without bound.
	limit int
}

// NewSplitter returns a Splitter that refuses lines longer than limit bytes,
// counting the terminator.
//
// limit is not a tuning knob: it is the bound that keeps a peer which never sends
// a terminator from growing this buffer without bound. It must be positive, and a
// caller passing something else has a bug — every caller passes a constant — so
// that panics rather than quietly reporting every line as too long.
func NewSplitter(limit int) *Splitter {
	if limit <= 0 {
		panic("lines: NewSplitter needs a positive limit")
	}
	return &Splitter{limit: limit}
}

// Feed appends bytes received from the stream.
func (s *Splitter) Feed(p []byte) {
	s.buf = append(s.buf, p...)
}

// Pending reports how many bytes are buffered: the length of a line that has not
// been terminated yet, or 0 when the buffer holds nothing.
func (s *Splitter) Pending() int {
	return len(s.buf)
}

// Next returns the next line and consumes it from the buffer, or ErrIncomplete
// when no terminator has arrived yet.
//
// The returned slice aliases the buffer and is only valid until the next call; a
// caller that keeps a line copies it. A blank line comes back as an empty slice
// and no error — deciding that it is not allowed belongs to the format being
// read.
func (s *Splitter) Next() ([]byte, error) {
	i := bytes.IndexByte(s.buf, '\n')
	if i < 0 {
		// Every buffered byte would be part of this line, so the line is already
		// at least len(buf)+1 bytes long — past the bound even if the terminator
		// arrives next.
		if len(s.buf) >= s.limit {
			return nil, tooLong(len(s.buf), s.limit)
		}
		return nil, ErrIncomplete
	}
	// The line is complete, and the bound counts its terminator. The check has to
	// happen here as well as above: a terminator that arrives in the same read as
	// an over-long line would otherwise walk straight past the bound, and the
	// buffer would have grown to hold it first.
	if i+1 > s.limit {
		return nil, tooLong(i+1, s.limit)
	}

	line := s.buf[:i]
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}

	rest := s.buf[i+1:]
	if len(rest)*2 < cap(rest) {
		// Most of the buffer was this one line (a large attachment, say). Copy the
		// remainder out rather than reslicing, so the oversized array is not kept
		// alive for the rest of the stream.
		s.buf = append([]byte(nil), rest...)
	} else {
		s.buf = rest
	}
	return line, nil
}

// tooLong reports a line over the bound. The bytes are left where they are: a
// caller that asks again is told the same thing, rather than being stepped past
// the line silently.
func tooLong(size, limit int) error {
	return fmt.Errorf("%w: %d bytes (limit %d)", ErrLineTooLong, size, limit)
}

// TakePending removes and returns the bytes buffered without a terminator: the
// tail of a stream that ended mid-line, which a caller that does not require a
// terminator reads as a final line. It returns nil when the buffer is empty.
//
// The returned slice aliases the buffer, and nothing else refers to it
// afterwards, so it stays valid.
func (s *Splitter) TakePending() []byte {
	tail := s.buf
	if len(tail) == 0 {
		return nil
	}
	if n := len(tail); tail[n-1] == '\r' {
		tail = tail[:n-1]
	}
	s.buf = nil
	return tail
}

// Reader reads lines from a stream (pull style).
type Reader struct {
	src   io.Reader
	sp    *Splitter
	chunk []byte
	eof   bool

	// RequireTerminator makes a stream that ends without a terminator an error
	// instead of a final line — what the frame protocol needs, since a frame cut
	// short is a failure to report rather than one to guess at. Left false, the
	// trailing bytes are returned as a line, which is what bufio.Scanner does and
	// therefore what the MCP and SSE streams were already relying on.
	RequireTerminator bool
}

// NewReader returns a Reader that pulls lines from src, refusing lines longer
// than limit bytes (counting the terminator).
func NewReader(src io.Reader, limit int) *Reader {
	return &Reader{src: src, sp: NewSplitter(limit), chunk: make([]byte, chunkSize)}
}

// Pending reports how many bytes of an unterminated line are buffered.
func (r *Reader) Pending() int {
	return r.sp.Pending()
}

// ReadLine returns the next line, blocking until one is available.
//
// At the end of the stream it returns io.EOF, but only at a line boundary unless
// RequireTerminator is false. The returned slice is only valid until the next
// call.
func (r *Reader) ReadLine() ([]byte, error) {
	for {
		line, err := r.sp.Next()
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, ErrIncomplete) {
			return nil, err
		}
		if r.eof {
			if n := r.sp.Pending(); n > 0 {
				if r.RequireTerminator {
					return nil, fmt.Errorf("lines: stream ended with %d bytes of an unterminated line", n)
				}
				return r.sp.TakePending(), nil
			}
			return nil, io.EOF
		}

		n, readErr := r.src.Read(r.chunk)
		if n > 0 {
			r.sp.Feed(r.chunk[:n])
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, readErr
			}
			// Loop once more: a line that completed exactly at EOF is still a
			// line, and io.EOF is reported only after handing it over.
			r.eof = true
		}
	}
}
