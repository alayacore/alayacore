package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/lines"
)

// textValue is the value the tests frame: the shape a content frame carries.
const textValue = `{"type":"text","text":"hi"}`

func mustEncode(t *testing.T, tag, value string) []byte {
	t.Helper()
	frame, err := Encode(tag, value)
	if err != nil {
		t.Fatalf("Encode(%q, %q) error = %v", tag, value, err)
	}
	return frame
}

// nextFrame returns the next frame from a decoder, failing the test on an error.
func nextFrame(t *testing.T, d *Decoder) (tag, value string) {
	t.Helper()
	tag, value, err := d.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	return tag, value
}

// drain reads frames until the decoder needs more bytes, returning their tags.
func drain(t *testing.T, d *Decoder) []string {
	t.Helper()
	var tags []string
	for {
		tag, _, err := d.Next()
		if errors.Is(err, ErrIncomplete) {
			return tags
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		tags = append(tags, tag)
	}
}

func TestEncode(t *testing.T) {
	want := TagUserX + textValue + "\n"
	if got := string(mustEncode(t, TagUserX, textValue)); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncode_ControlFrameIsTheTagAlone(t *testing.T) {
	for _, tag := range []string{TagUserEnd, TagInputEnd} {
		if got := string(mustEncode(t, tag, "")); got != tag+"\n" {
			t.Errorf("Encode(%q, \"\") = %q, want %q", tag, got, tag+"\n")
		}
	}
}

func TestEncode_RejectsRawLineBreak(t *testing.T) {
	for _, value := range []string{"{\n}", "{\r}", "{\r\n}", "a\nb"} {
		if _, err := Encode(TagUserX, value); err == nil {
			t.Errorf("Encode(%q) succeeded, want an error", value)
		}
	}
}

// TestEncode_RejectsIndentedJSON pins the invariant the whole format rests on: a
// value with a line break in it cannot be written, so no reader ever has to cope
// with one. Under the fixed-length format an indented value was legal.
func TestEncode_RejectsIndentedJSON(t *testing.T) {
	indented, err := json.MarshalIndent(map[string]string{"type": "text", "text": "hi"}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if _, err := Encode(TagUserX, string(indented)); err == nil {
		t.Errorf("Encode(%q) succeeded, want an error", indented)
	}
}

func TestEncode_RejectsValueThatIsNotAnObject(t *testing.T) {
	for _, value := range []string{"plain text", "[]", "42", `"quoted"`, "null"} {
		if _, err := Encode(TagUserX, value); err == nil {
			t.Errorf("Encode(%q) succeeded, want an error", value)
		}
	}
	// An empty value stays legal: the control frames' line is the tag alone.
	if _, err := Encode(TagUserEnd, ""); err != nil {
		t.Errorf("Encode(%q, \"\") = %v, want nil", TagUserEnd, err)
	}
}

func TestEncode_RejectsInvalidTag(t *testing.T) {
	for _, tag := range []string{"", "A", "ABC", "A1", "1A", "A-"} {
		if _, err := Encode(tag, textValue); err == nil {
			t.Errorf("Encode(%q) succeeded, want an error", tag)
		}
	}
}

// TestEncodeAndDecoderAgreeOnTheBound ties the two counts together: Encode's bound
// counts the terminator, and so does the decoder's, so a frame written at the
// bound is exactly a frame that reads back — and one byte over it is neither.
func TestEncodeAndDecoderAgreeOnTheBound(t *testing.T) {
	const value = `{"type":"text"}`
	frameLen := len(TagUserX) + len(value) + 1

	// What Encode writes is what the bound counts: the tag, the value, and the
	// terminator.
	if frame := mustEncode(t, TagUserX, value); len(frame) != frameLen {
		t.Fatalf("Encode() produced %d bytes, want %d", len(frame), frameLen)
	}

	atBound := newDecoder(frameLen)
	atBound.Feed(mustEncode(t, TagUserX, value))
	if tag, _, err := atBound.Next(); err != nil || tag != TagUserX {
		t.Errorf("Next() at the bound = %q, %v; want the frame", tag, err)
	}

	oneUnder := newDecoder(frameLen - 1)
	oneUnder.Feed(mustEncode(t, TagUserX, value))
	if _, _, err := oneUnder.Next(); !errors.Is(err, lines.ErrLineTooLong) {
		t.Errorf("Next() one byte under the bound = %v, want lines.ErrLineTooLong", err)
	}
}

// TestFitsInFrame checks the bound without allocating MaxFrameSize bytes.
func TestFitsInFrame(t *testing.T) {
	if err := fitsInFrame(MaxFrameSize); err != nil {
		t.Errorf("fitsInFrame(MaxFrameSize) = %v, want nil", err)
	}
	if err := fitsInFrame(MaxFrameSize + 1); err == nil {
		t.Error("fitsInFrame(MaxFrameSize+1) = nil, want an error")
	}
}

func TestDecoder_Next(t *testing.T) {
	tests := []struct {
		name string
		feed []string
		want []string
	}{
		{name: "one frame", feed: []string{"UX{}\n"}, want: []string{TagUserX}},
		{name: "two frames in one feed", feed: []string{"UX{}\nUE\n"}, want: []string{TagUserX, TagUserEnd}},
		{name: "frame split across feeds", feed: []string{"UX{", "}\n"}, want: []string{TagUserX}},
		{name: "byte at a time", feed: []string{"A", "x", "{", "}", "\n"}, want: []string{TagAssistantXDelta}},
		{name: "CRLF", feed: []string{"UE\r\n"}, want: []string{TagUserEnd}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Decoder{}
			var got []string
			for _, chunk := range tt.feed {
				d.Feed([]byte(chunk))
				got = append(got, drain(t, d)...)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("frames = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecoder_Next_RejectsMalformedFrame(t *testing.T) {
	tests := []struct {
		name string
		feed string
		want string
	}{
		{name: "blank line", feed: "\n", want: "blank line"},
		{name: "one byte line", feed: "U\n", want: "shorter than"},
		{name: "tag is not two letters", feed: textValue + "\n", want: "two-letter tag"},
		{name: "value is not an object", feed: "UXhello\n", want: "must be a JSON object"},
		// A space after the tag is not a separator: the value starts at byte 2, so
		// the space is its first character. Documents write `UX {...}` for
		// readability, and this is the case that keeps that from becoming bytes.
		{name: "space after the tag", feed: "UX {\"type\":\"text\"}\n", want: "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Decoder{}
			d.Feed([]byte(tt.feed))

			_, _, err := d.Next()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Next() error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestDecoder_Next_Incomplete(t *testing.T) {
	d := &Decoder{}
	d.Feed([]byte("UX{}"))

	if _, _, err := d.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Next() error = %v, want ErrIncomplete", err)
	}
	if got := d.Pending(); got != 4 {
		t.Errorf("Pending() = %d, want the 4 buffered bytes", got)
	}

	d.Feed([]byte("\n"))
	if tag, _ := nextFrame(t, d); tag != TagUserX {
		t.Errorf("tag = %q, want %q", tag, TagUserX)
	}
}

// TestDecoder_Next_ErrorIsTerminal is the contract that keeps a malformed frame
// from being skipped: the stream is finished by it, so the frames behind it are
// never read and a caller cannot resynchronise onto the middle of the stream.
func TestDecoder_Next_ErrorIsTerminal(t *testing.T) {
	d := &Decoder{}
	d.Feed([]byte("bad\nUX{}\n"))

	_, _, first := d.Next()
	if first == nil {
		t.Fatal("Next() accepted a malformed frame")
	}
	if _, _, second := d.Next(); second == nil || second.Error() != first.Error() {
		t.Errorf("second Next() error = %v, want the same as %v", second, first)
	}

	// More bytes, including a well-formed frame, change nothing.
	d.Feed([]byte("UE\n"))
	if _, _, third := d.Next(); third == nil || third.Error() != first.Error() {
		t.Errorf("Next() after more bytes = %v, want the stream still finished by %v", third, first)
	}
}

func TestDecoder_Next_FrameTooLong(t *testing.T) {
	d := newDecoder(8)
	d.Feed([]byte("UX{" + strings.Repeat("x", 8)))

	_, _, err := d.Next()
	if !errors.Is(err, lines.ErrLineTooLong) {
		t.Errorf("Next() error = %v, want lines.ErrLineTooLong", err)
	}
}

// TestDecoder_Next_MultiLineValueIsNeverTwoFrames documents the two-layer
// guarantee. The first fragment of an indented value passes the shape check (it
// begins with a tag and '{') and fails JSON decoding instead; every later
// fragment fails the shape check. Either way the stream is rejected, and no
// fragment is ever read as a frame.
func TestDecoder_Next_MultiLineValueIsNeverTwoFrames(t *testing.T) {
	const indented = "UF{\"call_id\":\"c1\",\"output\":[\n{\"type\":\"text\",\"text\":\"hi\"}\n],\"is_error\":false}\n"

	d := &Decoder{}
	d.Feed([]byte(indented))

	tag, value := nextFrame(t, d)
	if tag != TagUserF {
		t.Fatalf("first fragment tag = %q, want %q", tag, TagUserF)
	}
	if json.Valid([]byte(value)) {
		t.Fatalf("first fragment value %q is valid JSON; the case needs a truncated one", value)
	}

	if _, _, err := d.Next(); err == nil {
		t.Error("Next() read a fragment of a multi-line value as a frame")
	}
}

func TestReader_ReadFrame(t *testing.T) {
	stream := append(mustEncode(t, TagUserX, textValue), mustEncode(t, TagUserEnd, "")...)

	r := NewReader(bytes.NewReader(stream))
	for i, want := range []string{TagUserX, TagUserEnd} {
		tag, _, err := r.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: ReadFrame() error = %v", i, err)
		}
		if tag != want {
			t.Errorf("frame %d tag = %q, want %q", i, tag, want)
		}
	}
	if _, _, err := r.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Errorf("past the last frame ReadFrame() error = %v, want io.EOF", err)
	}
}

// TestReader_UnterminatedFrame checks the strict end of stream: bytes without a
// terminator are a frame cut short, which is reported rather than handed over as
// a frame.
func TestReader_UnterminatedFrame(t *testing.T) {
	r := newReader(strings.NewReader("UX{}"), 1024)

	_, _, err := r.ReadFrame()
	if err == nil || errors.Is(err, io.EOF) {
		t.Errorf("ReadFrame() error = %v, want an error that is not io.EOF", err)
	}
}

// TestReader_ErrorIsTerminal is the Reader's side of the same contract.
func TestReader_ErrorIsTerminal(t *testing.T) {
	r := newReader(strings.NewReader("bad\nUX{}\n"), 1024)

	_, _, first := r.ReadFrame()
	if first == nil {
		t.Fatal("ReadFrame() accepted a malformed frame")
	}
	if _, _, second := r.ReadFrame(); second == nil || second.Error() != first.Error() {
		t.Errorf("second ReadFrame() error = %v, want the same as %v", second, first)
	}
}

func TestReader_FrameTooLong(t *testing.T) {
	r := newReader(strings.NewReader(strings.Repeat("x", 16)), 8)

	_, _, err := r.ReadFrame()
	if !errors.Is(err, lines.ErrLineTooLong) {
		t.Errorf("ReadFrame() error = %v, want lines.ErrLineTooLong", err)
	}
}
