// Package sse reads Server-Sent Events from a byte stream.
//
// One reader, because this tree had three. The two provider decoders and the MCP
// HTTP transport each folded SSE fields by hand, and each had to answer the same
// two questions: where does a size bound fall, and what does a stream that ends
// mid-event mean. They answered both differently — a bound on a line where the
// event was the unit, and an event dropped at end-of-stream by one reader and
// delivered by the others — neither of which any protocol asked for.
//
// # The field model
//
// An event is a run of field lines ended by a blank line. "data:" fields
// accumulate, joined with LF: the spec lets one event's payload span several
// lines, which is why the bound below is on the event and not on a line.
// "event:" sets the type. A line beginning with ':' is a comment, and a field
// this reader does not know is ignored, as the spec requires. The field name may
// be followed by one space, which is not part of the value.
//
// An event carrying no data is not returned. It is legal SSE — a keep-alive, or
// a bare "event:" line — and there is nothing in it for a caller to act on.
//
// # The end of a stream
//
// A stream that ends inside an event hands that event over instead of dropping
// it. The bytes did arrive, and a caller that cannot use them (truncated JSON,
// say) is the one that can say so; silence would turn a missing trailing blank
// line into a missing answer.
//
// # The bound
//
// limit bounds one line and one assembled event. The line bound keeps a stream
// that never sends a newline from growing without bound; the event bound keeps a
// peer from growing one event one small line at a time. They are the same number
// because they are the same ceiling on one message — a caller holding two
// different numbers has to justify the difference to itself every time it reads
// them, and nothing in this tree does.
package sse

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/lines"
)

// Event is one SSE event.
type Event struct {
	// Type is the value of the event's "event:" field, or "" when the stream
	// sent none. It is the last one seen in the event: the spec lets a repeated
	// field overwrite the earlier value.
	Type string

	// Data is the event's "data:" values joined with LF, without their field
	// name or the one optional space after it.
	Data string
}

// ErrTooLarge reports that a line, or the event it was folding into, passed the
// reader's limit. It is the condition, not the wording: a provider calls an
// oversized event a runaway tool call, the MCP transport calls it a message past
// what the transports agreed to carry, and each says so in its own terms.
var ErrTooLarge = errors.New("sse: exceeds the size limit")

// Reader reads events from a stream. It is not safe for concurrent use: a caller
// that feeds it from more than one goroutine owns the synchronization.
type Reader struct {
	lines *lines.Reader
	limit int

	// The event being assembled.
	typ     string
	data    strings.Builder
	hasData bool

	event Event
	eof   bool
	err   error
}

// NewReader returns a Reader that pulls events from src, refusing any line or
// assembled event longer than limit bytes.
func NewReader(src io.Reader, limit int) *Reader {
	return &Reader{lines: lines.NewReader(src, limit), limit: limit}
}

// Next advances to the next event, reporting whether there is one. Once it
// reports false, Err says why: nil means the stream ended.
func (r *Reader) Next() bool {
	if r.err != nil || r.eof {
		return false
	}

	r.typ = ""
	r.data.Reset()
	r.hasData = false

	for {
		line, err := r.lines.ReadLine()
		if err != nil {
			if errors.Is(err, lines.ErrLineTooLong) {
				// A line over the limit is an event over the limit: no event of
				// an acceptable size could be assembled from it.
				r.err = fmt.Errorf("%w: %v", ErrTooLarge, err)
				return false
			}
			if !errors.Is(err, io.EOF) {
				r.err = err
				return false
			}
			// The stream ended. Deliver what is pending, if anything, and stop:
			// the next call reports false without reading again.
			r.eof = true
			return r.emit()
		}

		s := string(line)
		switch {
		case s == "":
			// The event boundary. A blank line with nothing accumulated
			// separates keep-alives; it is not an empty event.
			if r.hasData {
				r.emit()
				return true
			}

		case strings.HasPrefix(s, "data:"):
			if r.hasData {
				r.data.WriteByte('\n')
			}
			r.data.WriteString(fieldValue(s, len("data:")))
			r.hasData = true
			if r.data.Len() > r.limit {
				r.err = fmt.Errorf("%w: %d bytes (limit %d)", ErrTooLarge, r.data.Len(), r.limit)
				return false
			}

		case strings.HasPrefix(s, "event:"):
			r.typ = fieldValue(s, len("event:"))

		default:
			// A comment (":…") or a field this reader does not know. Both are
			// skipped by design: the spec forbids refusing an event over a field
			// the reader was not written to understand.
		}
	}
}

// Event returns the event Next just advanced to.
func (r *Reader) Event() Event { return r.event }

// Err returns the error that stopped the reader, or nil at the end of the stream.
func (r *Reader) Err() error { return r.err }

// emit records the assembled event. It reports false when nothing was
// accumulated, which is what keeps an event without data invisible to callers.
func (r *Reader) emit() bool {
	if !r.hasData {
		return false
	}
	r.event = Event{Type: r.typ, Data: r.data.String()}
	r.hasData = false
	return true
}

// fieldValue strips a field's name (nameLen bytes, colon included) from a line,
// and the one space the spec allows after it.
func fieldValue(line string, nameLen int) string {
	if len(line) > nameLen && line[nameLen] == ' ' {
		return line[nameLen+1:]
	}
	return line[nameLen:]
}
