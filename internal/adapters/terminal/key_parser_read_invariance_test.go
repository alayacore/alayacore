package terminal

// Reading a stream in pieces must be indistinguishable from reading it at once.
//
// This is the invariant the input loop is built on: a read stops at a fixed
// boundary (`inputReadSize` bytes, `consoleEventsPerRead` events), so a long
// burst cuts *every* sequence in half as a matter of course, and the cut is
// invisible only if the parser finishes what a boundary started. It is stated
// here as a property rather than as one test per sequence because the failure is
// never "this sequence" — it is "some byte of a reply, or of a marker, arrived as
// typing", and which byte depends on where an arbitrary boundary fell.
//
// Every stream is therefore parsed once whole and once in reads of every size
// from one byte up. Both must produce the same messages, and so must the
// silence-timeout flush that resolves whatever the stream leaves outstanding.

import (
	"fmt"
	"strings"
	"testing"
)

// invarianceStreams are the shapes whose bytes a boundary can divide: the two
// mouse encodings, the string controls a terminal replies with unprompted, both
// bracketed-paste markers, the chords that share those introducers, keys, and
// text that is no sequence at all.
func invarianceStreams() []string {
	paste := pasteStart + "two\r\nlines" + pasteEnd
	return []string{
		"\x1b[<35;60;10M",
		"\x1b[<35;135;1M\x1b[<35;176;1M",
		"\x1b[M !#",
		"\x1b[M abc",
		"\x1b[35;60;10M",
		"\x1b]0;window title\x07",
		"\x1b]11;rgb:1e1e/1e1e/1e1e\x07",
		"\x1b]52;c;aGVsbG8=\x07",
		"\x1bP1+r6d6978\x1b\\",
		"\x1bXpayload\x1b\\",
		"\x1b^payload\x1b\\",
		"\x1b_payload\x1b\\",
		paste,
		paste + "z",
		pasteStart + strings.Repeat("x", 40) + pasteEnd,
		pasteStart + "tail looks like a marker\x1b[20",
		pasteStart + "never closed",
		"\x1b[I",
		"\x1b[O",
		"\x1b[I\x1b[200~q\x1b[201~\x1b[O",
		"\x1b[A\x1b[B\x1b[C\x1b[D",
		"\x1b\x1b[A",
		"\x1ba",
		"\x1b<c",
		"\x1b[1;4Z",
		"\x1bOP",
		"hello\r\nworld",
		"\x1b[200~日本語👍\x1b[201~",
		"\x1b[200~\x1b[201~",
		"\x1b",
		"\x1b[",
		"\x1b]",
		"\x1b[2",
		"a\x1b",
		strings.Repeat("x", 300),
		strings.Repeat("\x1b[<12;34;56M", 20),
		"\x1b[200~" + strings.Repeat("y", 300) + pasteEnd,
	}
}

func TestParseIsInvariantToReadBoundaries(t *testing.T) {
	for _, stream := range invarianceStreams() {
		whole, wholeFlush := parseReads([][]byte{[]byte(stream)})

		for size := 1; size <= len(stream); size++ {
			var reads [][]byte
			for i := 0; i < len(stream); i += size {
				reads = append(reads, []byte(stream[i:min(i+size, len(stream))]))
			}
			got, gotFlush := parseReads(reads)
			if got != whole || gotFlush != wholeFlush {
				t.Errorf("%q read in %d-byte reads is not what the same bytes read at once give:\n"+
					"\tone read:       %s / flush %s\n"+
					"\t%d bytes a read: %s / flush %s",
					esc(stream), size, whole, wholeFlush, size, got, gotFlush)
				break
			}
		}
	}
}

// parseReads feeds each read to one parser and returns the messages delivered,
// followed by whatever the silence timeout resolves at the end of the stream.
func parseReads(reads [][]byte) (msgs, flushed string) {
	p := &InputParser{}
	var out []string
	for _, r := range reads {
		out = renderMsgs(out, p.Parse(r))
	}
	return strings.Join(out, " "), strings.Join(renderMsgs(nil, p.Flush()), " ")
}

// renderMsgs names each message without spilling paste bytes into the failure
// line: what the comparison is about is which messages exist, and a 300-byte
// paste would bury the difference that matters. Short content is kept verbatim.
func renderMsgs(dst []string, msgs []any) []string {
	for _, m := range msgs {
		switch v := m.(type) {
		case PasteMsg:
			if len(v.Content) > 24 {
				dst = append(dst, fmt.Sprintf("paste(len=%d)", len(v.Content)))
			} else {
				dst = append(dst, "paste("+esc(v.Content)+")")
			}
		case KeyPressMsg:
			dst = append(dst, "key:"+Key(v).String())
		case FocusMsg:
			dst = append(dst, "focus")
		case BlurMsg:
			dst = append(dst, "blur")
		default:
			dst = append(dst, fmt.Sprintf("%T", m))
		}
	}
	return dst
}

// esc makes the control bytes of a stream legible in a failure line.
func esc(s string) string {
	return strings.NewReplacer("\x1b", "E", "\x07", "BEL", "\r", "\\r", "\n", "\\n").Replace(s)
}
