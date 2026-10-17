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
	"errors"
	"fmt"
	"io"

	"github.com/alayacore/alayacore/internal/lines"
)

// maxEventBytes bounds one decoded SSE event — the data accumulated between
// blank lines.
//
// It is fixed, not derived from the configured max_tokens: this is the memory
// ceiling, and a ceiling a config value can raise is not a ceiling.
//
// 8MB is far above any real single task. Provider output limits are tens of
// thousands to ~128K tokens (around half a megabyte of text), and even a
// pathological, escape-heavy JSON encoding of one stays within a few MB, so no
// legitimate task is rejected — yet a misbehaving server is still bounded.
//
// NOTE: model output limits grow. A single task today tops out around half a
// megabyte of output (provider caps of ~128K tokens), but if a task can ever
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

// sseLineReader reads newline-delimited lines from an event stream, bounded by
// maxEventBytes: a longer line fails, since no acceptable event could contain
// it, which keeps a stream with no newline from growing without bound.
//
// It is lines.Reader with this package's words for that bound. The bound has a
// name here because of what exceeding it means — an oversized tool call, far more
// often than a hostile stream — and lines' own wording would hide that. A stream
// that ends without a terminator hands its last line over as a line, as it always
// has.
type sseLineReader struct {
	lines *lines.Reader
}

func newSSELineReader(r io.Reader) *sseLineReader {
	return &sseLineReader{lines: lines.NewReader(r, maxEventBytes)}
}

// next returns the next line without its terminator. It returns io.EOF once the
// stream is exhausted.
func (l *sseLineReader) next() (string, error) {
	line, err := l.lines.ReadLine()
	if errors.Is(err, lines.ErrLineTooLong) {
		return "", errEventTooLarge()
	}
	if err != nil {
		return "", err
	}
	return string(line), nil
}
