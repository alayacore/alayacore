// Package wire defines AlayaCore's frame format: the bytes that carry every
// message between an adapter and the session, and the bytes that make up a
// session file's body.
//
// # Format
//
// One frame per line:
//
//	<tag><value>\n
//
// The tag is exactly two ASCII letters (see the Tag* constants). The value is a
// JSON object, with one exception: the control tags UE and CE carry no value, so
// their line is the tag alone. Nothing else follows the value on the line, and
// there are no separator lines: every line is exactly one frame.
//
// # The tag
//
// The two-byte tag names the frame's *plane*, not the concrete content kind —
// the kind travels inside the JSON as "type". Tags are grouped as a 2x2 over
// {into the model (U), out of the model (A)} x {content (X), function (F)}:
//
//   - UX: User content (a content item; both directions — input and echo)
//   - AX: Assistant content (complete/authoritative; empty if deltas preceded it)
//   - AF: Assistant function / tool call (complete/authoritative)
//   - UF: User function / tool result
//   - UE: User message end
//   - CE: Control-plane input end (the adapter has no more input; commands
//     may still follow — see TagInputEnd)
//   - CI: Command input (adapter → agent, JSON CmdMsg)
//   - CO: Command output (agent → adapter, JSON CmdResultMsg)
//   - SM: System message
//
// Function frames are kept apart from content frames because a tool call and
// its result are a *pair* correlated by call_id — they can run in parallel,
// interleave, stream half-way, and be reordered by id. They are not a
// self-contained content item, so they carry their own shape.
//
// Lowercase tags carry streaming delta / incremental content:
//
//   - Ax: Assistant content delta (streaming fragment; "type" says text or reasoning)
//   - Af: Assistant function / tool call delta (partial JSON argument)
//   - Uf: Tool result preview snapshot (ephemeral, display-only; the
//     authoritative result always arrives via UF)
//
// AX behavior varies by mode:
//
//   - Default (deltas enabled): Ax carries content; AX is an empty terminator.
//   - --no-delta mode: Ax is absent; AX carries the full content.
//
// The tag is deliberately checked as a *class* (two ASCII letters) and not
// against this list. A reader that rejected unknown tags would turn an additive
// change — a new tag — into a breaking one, and additivity is a promise the
// protocol makes: a client that knows only the older format keeps working when
// the core adds a tag it ignores.
//
// # The line
//
// The terminator is what carries the frame boundary, so a value may never
// contain a raw LF or CR: such a value would split one frame across two lines,
// and the second line would be reported as malformed rather than silently
// accepted as a frame of its own (see Decoder.Next). Encode rejects a value that
// contains one. The failure to look for is a writer that marshals JSON by hand
// instead of with encoding/json, which escapes both characters.
//
// A *trailing* CR is accepted on input — lines.Splitter strips it — and never
// written. That is a writer on a platform that translates line endings, and
// reading it is safe: a JSON object ends with '}', so a line ending in CR ended
// in CRLF.
//
// A blank line is an error, not a separator. This package is what knows that:
// lines.Splitter hands a blank line back like any other, and Decode is what
// rejects it, because only the frame format knows a blank line is never a frame.
//
// # Decoding
//
// Decoder validates a frame's *shape*: the tag is two ASCII letters and the
// value is empty or starts with '{'. It does not parse the JSON, so the framing
// layer stays independent of the payload's encoding and a value is never scanned
// twice. JSON validity is the consumer's business and is still a hard error
// there. The two checks together mean a value that was not one line cannot be
// mistaken for frames: no line of a multi-line JSON object begins with two ASCII
// letters, so its fragments fail the shape check.
//
// Decoding is terminal rather than skipping. A malformed frame ends the stream:
// every later call reports the same error, whatever bytes follow. There is no
// resynchronisation point this format trusts — the tag is a class check, not a
// search key — so a caller must never be able to step past a bad frame and
// resume somewhere in the middle of the stream.
package wire

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/lines"
)

// Tag constants - these are sent over the wire.
const (
	TagAssistantF = "AF" // JSON: call_id, name, input (function arguments, complete)
	TagUserF      = "UF" // JSON: call_id, output, is_error (function result)
	TagUserX      = "UX" // User content — JSON content item (type + text/uri/…)
	TagAssistantX = "AX" // Assistant content — JSON content item (type + text)

	TagUserEnd = "UE" // User message end — flushes staged content as one window

	// Command control plane — ID-correlated request/response, mirroring
	// the tool control plane (AF/UF). Payloads are plain JSON, no envelope.
	TagCommandIn  = "CI" // Command input: CmdMsg JSON (adapter → agent)
	TagCommandOut = "CO" // Command output: CmdResultMsg JSON (agent → adapter)

	// TagInputEnd is the control plane's half-close: the adapter has no more
	// input (its stdin reached EOF). It is TCP's FIN, not a torn-down
	// connection — no further *prompts* are coming, but commands may still
	// arrive on the same stream (an OAuth callback submits its :mcp_confirm
	// through the pipe that just ended).
	//
	// It is a control frame, not user content: never staged, echoed, persisted,
	// or replayed. An adapter with nothing more to say sends this instead of
	// closing its write end, because closing would end that command path too.
	TagInputEnd = "CE"

	TagSystemMsg = "SM" // System message JSON: {"type":"...","data":{...}}

	// Lowercase tags for streaming delta / incremental content.
	TagAssistantXDelta = "Ax" // Assistant content delta (type is text or reasoning)
	TagAssistantFDelta = "Af" // Assistant function / tool call delta (partial JSON argument)
	TagUserFDelta      = "Uf" // Tool result preview snapshot (ephemeral, non-authoritative)
)

// MaxFrameSize is the largest frame this format accepts, counting the tag, the
// value and the terminator — the bytes a decoder holds at once, which is the
// quantity that must not grow without bound.
//
// It is 64MB because that is what the MCP transports bound one of their messages
// by (internal/mcp's maxMessageBytes): the streams are shaped the same way, and a
// tool result that one of them carries is carried by the other. The two constants
// stay separate because the two bounds belong to different protocols and may
// drift, but they are meant to agree.
//
// It is as large as it is for the session file too: a session that saved a
// multi-megabyte base64 attachment must load again. Producers bound themselves
// well below it (a tool result is a 64KB text read or a 16MB media file), so
// hitting this is a bug rather than a use.
const MaxFrameSize = 64 << 20

// ErrIncomplete reports that the bytes fed so far do not hold a whole frame yet:
// more may follow. Re-exported from lines so that a caller speaking frames needs
// only this package.
var ErrIncomplete = lines.ErrIncomplete

// isLetter reports whether b is an ASCII letter. Tags are letters so that a
// reader can tell a frame from the fragments of one.
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// checkTag validates the tag half of the format. Extracted from Encode so the
// rule reads the same here as it does in Decode.
func checkTag(tag string) error {
	if len(tag) != 2 {
		return fmt.Errorf("wire: tag must be exactly 2 characters, got %q (len %d)", tag, len(tag))
	}
	if !isLetter(tag[0]) || !isLetter(tag[1]) {
		return fmt.Errorf("wire: tag must be two ASCII letters, got %q", tag)
	}
	return nil
}

// checkValueShape validates the value half of a frame. The format allows an
// empty value — a control frame's line is the tag alone — or a JSON object. It is
// a shape check, not a parse: '{' is as far as this package looks, and whether
// the bytes are well-formed JSON is the consumer's business.
//
// Both directions use it, so the package cannot write a frame it would refuse to
// read back.
func checkValueShape(value string) error {
	if value == "" || value[0] == '{' {
		return nil
	}
	return errors.New("wire: a frame's value must be a JSON object, or empty for a control frame")
}

// fitsInFrame reports whether a frame of this many bytes (tag, value and
// terminator) is within the bound, without building it. Extracted as a pure
// function so the MaxFrameSize path can be tested without allocating 64MB.
func fitsInFrame(frameLen int) error {
	if frameLen > MaxFrameSize {
		return fmt.Errorf("wire: frame of %d bytes exceeds the maximum %d", frameLen, MaxFrameSize)
	}
	return nil
}

// Encode builds the wire bytes of a single frame: the tag, the value, and the
// terminator.
//
// It rejects a tag that is not two ASCII letters, a value that is not empty or a
// JSON object, a value that contains a raw LF or CR (which would end the frame
// early), and a frame above MaxFrameSize. The caller must surface these rather
// than dropping the frame — a frame that was never written reads as the adapter
// going quiet.
func Encode(tag, value string) ([]byte, error) {
	if err := checkTag(tag); err != nil {
		return nil, err
	}
	if err := checkValueShape(value); err != nil {
		return nil, err
	}
	if i := strings.IndexAny(value, "\n\r"); i >= 0 {
		return nil, fmt.Errorf("wire: %s frame's value contains a raw %q at offset %d; a frame is one line",
			tag, value[i], i)
	}
	if err := fitsInFrame(len(tag) + len(value) + 1); err != nil {
		return nil, err
	}

	frame := make([]byte, 0, len(tag)+len(value)+1)
	frame = append(frame, tag...)
	frame = append(frame, value...)
	return append(frame, '\n'), nil
}

// WriteFrame writes one frame to w. It returns an error if the frame could not
// be encoded (never a truncated frame) or if the write failed.
func WriteFrame(w io.Writer, tag, value string) error {
	frame, err := Encode(tag, value)
	if err != nil {
		return err
	}
	_, err = w.Write(frame)
	return err
}

// Decoder turns a byte stream into frames, for callers that are handed bytes
// rather than reading them: the adapters, whose output arrives via Write.
//
// Call it as Feed followed by Next, until Next reports ErrIncomplete. The bounds
// and the buffering live in lines; this type is what turns a line into a frame,
// so the two cannot disagree about where a frame ends.
//
// The zero value is ready to use, so a struct that holds one — every adapter
// does — needs no constructor and no nil check.
type Decoder struct {
	sp *lines.Splitter
	// err is sticky: once a frame is malformed the stream is finished, and every
	// later call reports why. lines.Splitter has already consumed the line by the
	// time it is validated, so without this a caller looping until ErrIncomplete
	// would step over the bad frame and resume at the next one.
	err error
}

// splitter returns the decoder's splitter, creating it on first use.
func (d *Decoder) splitter() *lines.Splitter {
	if d.sp == nil {
		d.sp = lines.NewSplitter(MaxFrameSize)
	}
	return d.sp
}

// newDecoder returns a Decoder with the bound spelled out, so a test can reach
// the limit without allocating it. Everything else uses the zero value.
func newDecoder(limit int) *Decoder {
	return &Decoder{sp: lines.NewSplitter(limit)}
}

// Feed appends bytes received from the stream.
func (d *Decoder) Feed(p []byte) {
	d.splitter().Feed(p)
}

// Pending reports how many bytes of an unterminated frame are buffered.
func (d *Decoder) Pending() int {
	return d.splitter().Pending()
}

// Next returns the next complete frame and consumes it from the buffer.
//
// It returns ErrIncomplete when no terminator has arrived yet. Any other error is
// terminal: the stream is finished, and every later call reports the same error
// rather than reading what follows — a caller can never skip past a bad frame and
// resynchronise onto the middle of the stream.
func (d *Decoder) Next() (tag, value string, err error) {
	if d.err != nil {
		return "", "", d.err
	}
	line, err := d.splitter().Next()
	if err != nil {
		return "", "", err
	}
	tag, value, err = Decode(line)
	if err != nil {
		d.err = err
	}
	return tag, value, err
}

// Decode validates one frame's bytes and splits them into tag and value.
//
// The bytes may be the line a Reader or Decoder split out, or that line with its
// terminator still on it: a trailing LF, and a CR before it, are dropped first.
// That is not leniency — a value can never contain either character, so the only
// thing a trailing one can be is the terminator — and it makes this usable on a
// line read whole, as a session file's body may be.
func Decode(line []byte) (tag, value string, err error) {
	// Drop the terminator, if the caller passed the line whole.
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}

	switch {
	case len(line) == 0:
		return "", "", errors.New("wire: blank line; every line must be one frame")
	case len(line) < 2:
		return "", "", fmt.Errorf("wire: %s is shorter than a two-letter tag", quotedLine(line))
	case !isLetter(line[0]) || !isLetter(line[1]):
		return "", "", fmt.Errorf("wire: %s does not start with a two-letter tag", quotedLine(line))
	}

	tag = string(line[:2])
	value = string(line[2:])
	if err := checkValueShape(value); err != nil {
		return "", "", fmt.Errorf("%s frame: %w", tag, err)
	}
	return tag, value, nil
}

// quotedLine renders a line for an error message, cut short: a line may be tens
// of megabytes, and an error that echoes one is a second copy of the problem.
func quotedLine(line []byte) string {
	const shown = 64
	if len(line) > shown {
		return fmt.Sprintf("%q… (%d bytes)", line[:shown], len(line))
	}
	return fmt.Sprintf("%q", line)
}

// Reader reads frames from a stream: a pipe, a file, or a session file's body.
type Reader struct {
	lr *lines.Reader
	// err is sticky for the same reason Decoder's is: a malformed frame ends the
	// stream.
	err error
}

// NewReader returns a Reader that pulls frames from src.
func NewReader(src io.Reader) *Reader {
	return newReader(src, MaxFrameSize)
}

// newReader is NewReader with the bound spelled out, so a test can reach the
// limit without allocating it.
func newReader(src io.Reader, limit int) *Reader {
	lr := lines.NewReader(src, limit)
	// A frame cut short is a failure to report, not a frame to guess at — unlike
	// the MCP and SSE streams, which read a trailing line as a line.
	lr.RequireTerminator = true
	return &Reader{lr: lr}
}

// ReadFrame returns the next frame, blocking until one is available.
//
// At the end of the stream it returns io.EOF — but only at a frame boundary. A
// stream that ends mid-frame reports the leftover bytes instead: a frame that
// was cut short is a failure to report, not a frame to guess at. A malformed
// frame is terminal, exactly as it is for Decoder.
func (r *Reader) ReadFrame() (tag, value string, err error) {
	if r.err != nil {
		return "", "", r.err
	}
	line, err := r.lr.ReadLine()
	if err != nil {
		return "", "", err
	}
	tag, value, err = Decode(line)
	if err != nil {
		r.err = err
	}
	return tag, value, err
}
