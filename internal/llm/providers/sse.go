package providers

// Shared SSE reader.
//
// The reader is bounded by the decoded *event*, not by a line. SSE lets one
// event's data span several "data:" lines, so a per-line cap makes the same
// payload pass or fail depending on how the provider chose to break it up —
// an artifact of the reader, not a property of the protocol. bufio.Scanner,
// which tokenizes on lines with a fixed maximum, has exactly that shape (and
// is why a provider sending a large tool call atomically used to fail here).
// The cap belongs on what the event decodes to.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// maxEventBytes bounds one decoded SSE event — the data accumulated between
// blank lines.
//
// It is fixed, not derived from the configured max_tokens: this is the memory
// ceiling, and a ceiling a config value can raise is not a ceiling.
//
// 8MB is far above any real single turn. Provider output limits are tens of
// thousands to ~128K tokens (around half a megabyte of text), and even a
// pathological, escape-heavy JSON encoding of one stays within a few MB, so no
// legitimate turn is rejected — yet a misbehaving server is still bounded.
//
// NOTE: model output limits grow. A single turn today tops out around half a
// megabyte of output (provider caps of ~128K tokens), but if a turn can ever
// approach 1MB or more, revisit this constant — it must stay comfortably above
// what a provider may emit atomically. It tracks what providers can emit, and
// is not to be tightened to whatever is typical today.
const maxEventBytes = 8 << 20 // 8MB

// errEventTooLarge reports an event past maxEventBytes. It names the model's
// oversized output because that is the realistic cause; a hostile or broken
// peer is the other.
func errEventTooLarge() error {
	return fmt.Errorf("SSE event exceeded %dMB — the model may have generated an oversized tool call", maxEventBytes>>20)
}

// sseLineReader reads newline-delimited lines with no fixed per-line cap. A
// line longer than maxEventBytes fails, since no acceptable event could
// contain it, which keeps a stream with no newline from growing without bound.
type sseLineReader struct {
	r   *bufio.Reader
	buf []byte
}

func newSSELineReader(r io.Reader) *sseLineReader {
	return &sseLineReader{r: bufio.NewReader(r)}
}

// next returns the next line without its trailing CR/LF. It returns io.EOF
// once the stream is exhausted.
func (l *sseLineReader) next() (string, error) {
	l.buf = l.buf[:0]
	for {
		frag, err := l.r.ReadSlice('\n')
		if len(l.buf)+len(frag) > maxEventBytes {
			return "", errEventTooLarge()
		}
		l.buf = append(l.buf, frag...)

		switch {
		case err == nil:
			return string(trimEOL(l.buf)), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue // the line continues; ReadSlice returned what fit
		case errors.Is(err, io.EOF):
			if len(l.buf) == 0 {
				return "", io.EOF
			}
			return string(trimEOL(l.buf)), nil
		default:
			return "", err
		}
	}
}

// trimEOL strips a trailing newline and any preceding carriage return.
func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}
