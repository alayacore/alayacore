package sessionmigrate

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/wire"
)

// v14Frame frames a value the way v13 and v14 both framed it, and the way nothing
// in the program does any more: a 2-byte tag, a 4-byte big-endian length, and the
// value.
func v14Frame(tag, value string) []byte {
	frame := make([]byte, 6+len(value))
	copy(frame, tag)
	binary.BigEndian.PutUint32(frame[2:6], uint32(len(value)))
	copy(frame[6:], value)
	return frame
}

// v14File builds a session file at the given version with the given body frames,
// separated the way both v13 and v14 separated them.
func v14File(version int, frames ...[]byte) []byte {
	var b strings.Builder
	b.WriteString("---\ncreated_at: 2026-01-01T00:00:00Z\nmessage_version: ")
	b.WriteString(strconv.Itoa(version))
	b.WriteString("\n---\n")
	for _, f := range frames {
		b.WriteString("\n\n")
		b.Write(f)
	}
	return []byte(b.String())
}

// bodyOf returns the part of a session file after its frontmatter.
func bodyOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	_, body, err := split(raw)
	if err != nil {
		t.Fatalf("split(%q): %v", raw, err)
	}
	return body
}

// TestV14ToV15_CompactsTheValues is the step's one real job. An indented value is
// legal in v14 and cannot be one in v15 — a value that is not one line is not a
// frame — so the step compacts every value on the way through. Key order is kept,
// which is what lets a hand-edited file convert instead of producing a file nothing
// can read.
func TestV14ToV15_CompactsTheValues(t *testing.T) {
	const indented = "{\n  \"id\": \"1\",\n  \"type\": \"text\",\n  \"text\": \"hi\"\n}"
	raw := v14File(14, v14Frame("UX", indented), v14Frame("UE", ""))

	out, err := V14ToV15(raw)
	if err != nil {
		t.Fatalf("V14ToV15: %v", err)
	}

	want := "---\ncreated_at: 2026-01-01T00:00:00Z\nmessage_version: 15\n---\n" +
		"UX{\"id\":\"1\",\"type\":\"text\",\"text\":\"hi\"}\nUE\n"
	if string(out) != want {
		t.Errorf("V14ToV15() =\n%q\nwant\n%q", out, want)
	}
}

// TestV14ToV15_ReadsCRLF covers the same tolerance on the migration path: a v14
// file whose line endings an editor translated converts like any other, and comes
// out LF, which is what this writer writes.
func TestV14ToV15_ReadsCRLF(t *testing.T) {
	lf := v14File(14, v14Frame("UX", `{"type":"text","text":"hi"}`))
	crlf := strings.ReplaceAll(string(lf), "\n", "\r\n")

	fromLF, err := V14ToV15(lf)
	if err != nil {
		t.Fatalf("V14ToV15(lf): %v", err)
	}
	fromCRLF, err := V14ToV15([]byte(crlf))
	if err != nil {
		t.Fatalf("V14ToV15(crlf): %v", err)
	}
	if string(fromCRLF) != string(fromLF) {
		t.Errorf("CRLF input converted to\n%q\nwant the same as LF input\n%q", fromCRLF, fromLF)
	}
}

func TestV14ToV15_LeavesACurrentFileAlone(t *testing.T) {
	raw := []byte("---\nmessage_version: 15\n---\nUE\n")

	out, err := V14ToV15(raw)
	if err != nil {
		t.Fatalf("V14ToV15: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("V14ToV15() = %q, want the file unchanged", out)
	}
}

// TestV14ToV15_SendsV13ToTheOtherStep checks that the refusal names the step to
// run: a v13 file is one generation further back, and being told which script
// performs that conversion is the difference between a two-command upgrade and a
// puzzle. The second step's converter is not the one that knows v13 tags.
func TestV14ToV15_SendsV13ToTheOtherStep(t *testing.T) {
	_, err := V14ToV15(v14File(13, v14Frame("UT", "hello")))
	if err == nil {
		t.Fatal("V14ToV15 accepted a v13 file")
	}
	if !strings.Contains(err.Error(), "session_upgrade_v14.sh") {
		t.Errorf("error = %q, want it to name the v13 step's script", err)
	}
}

// TestV13ToV14_LeavesItsOwnOutputAlone is the pair of V14ToV15's no-op, and it is
// the shape both steps share: a file at my output version is one I have nothing to
// do, so it comes back untouched rather than as an error.
func TestV13ToV14_LeavesItsOwnOutputAlone(t *testing.T) {
	raw := v14File(14, v14Frame("UX", `{"type":"text"}`))

	out, err := V13ToV14(raw)
	if err != nil {
		t.Fatalf("V13ToV14: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("V13ToV14() = %q, want the file unchanged", out)
	}
}

// TestSetVersion_TouchesTheKeyNotAMentionOfIt pins which line the step rewrites: a
// commented line that merely contains the key's text is not the key. Searching the
// frontmatter for that text found the comment first, so the real key kept its old
// version and the conversion reported success anyway.
func TestSetVersion_TouchesTheKeyNotAMentionOfIt(t *testing.T) {
	front := []byte("---\n# message_version: 12 was here\ncreated_at: 2026-01-01T00:00:00Z\nmessage_version: 13\n---\n")

	got := string(setVersion(front, 14))
	if !strings.Contains(got, "# message_version: 12 was here") {
		t.Errorf("setVersion rewrote the commented line:\n%s", got)
	}
	if !strings.Contains(got, "\nmessage_version: 14\n") {
		t.Errorf("setVersion did not set the key:\n%s", got)
	}
}

// TestReframe_NamesTheFrameThatFailed pins which byte an error reports: the one
// the frame *starts* at. pos has already moved past the frame by the time a value
// is compacted or converted, so a message that printed it pointed at the next
// frame's tag — past the bytes the reader is being asked about, and at EOF for the
// last frame in the file.
func TestReframe_NamesTheFrameThatFailed(t *testing.T) {
	var body []byte
	body = append(body, v14Frame("UX", `{"type":"text","text":"fine"}`)...)
	body = append(body, []byte("\n\n")...)
	badAt := len(body)
	body = append(body, v14Frame("UX", "not json")...)

	_, err := reframe(body, encodeV14, nil)
	if err == nil {
		t.Fatal("reframe accepted a value that is not JSON")
	}
	if want := fmt.Sprintf("at byte %d", badAt); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v;\nwant it to name %s, where the bad frame starts", err, want)
	}
}

// TestV13ToV14_RefusesANewerFile is the other side of that: a file ahead of this
// step is not one it may touch. Running the steps out of order would otherwise
// reinterpret a v15 body as length-prefixed frames.
func TestV13ToV14_RefusesANewerFile(t *testing.T) {
	raw := []byte("---\nmessage_version: 15\n---\nUE\n")

	if _, err := V13ToV14(raw); err == nil {
		t.Error("V13ToV14 accepted a v15 file")
	}
}

// TestV13ToV14_WritesTheV14Framing pins the framing the first step has to produce:
// the second step reads v14, so a v13 → v14 conversion that emitted lines instead
// would leave it nothing to read.
func TestV13ToV14_WritesTheV14Framing(t *testing.T) {
	out, err := V13ToV14(v14File(13, v14Frame("UT", "hello")))
	if err != nil {
		t.Fatalf("V13ToV14: %v", err)
	}

	body := bodyOf(t, out)
	body = body[bytes.IndexByte(body, 'U'):] // past the blank line v14 kept between frames
	if len(body) < 6 || string(body[:2]) != wire.TagUserX {
		t.Fatalf("body starts with %q, want a UX tag", body[:min(6, len(body))])
	}
	if got, want := int(binary.BigEndian.Uint32(body[2:6])), len(`{"type":"text","text":"hello"}`); got != want {
		t.Errorf("length field = %d, want %d — the output is not v14-framed", got, want)
	}
}

// TestV15ToV16_RewritesTheNumberAndNothingElse is the whole step: v16 renamed
// vocabulary, which a file does not carry, so the body must come through byte
// for byte and only the frontmatter's number may move.
func TestV15ToV16_RewritesTheNumberAndNothingElse(t *testing.T) {
	body := "UX{\"id\":\"1\",\"type\":\"text\",\"value\":\"hello\"}\nUX{\"id\":\"2\",\"type\":\"text\",\"value\":\"world\"}\n"
	raw := []byte("---\ncreated_at: 2026-01-01T00:00:00Z\nmessage_version: 15\n---\n" + body)

	out, err := V15ToV16(raw)
	if err != nil {
		t.Fatalf("V15ToV16: %v", err)
	}
	want := "---\ncreated_at: 2026-01-01T00:00:00Z\nmessage_version: 16\n---\n" + body
	if string(out) != want {
		t.Errorf("V15ToV16() =\n%q\nwant\n%q", out, want)
	}
}

func TestV15ToV16_LeavesACurrentFileAlone(t *testing.T) {
	raw := []byte("---\nmessage_version: 16\n---\nUE\n")

	out, err := V15ToV16(raw)
	if err != nil {
		t.Fatalf("V15ToV16: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("V15ToV16() = %q, want the file unchanged", out)
	}
}

// TestV15ToV16_RefusesABodyItCannotRead: a file whose frontmatter claims v15
// while its body is still the length-prefixed framing is not v15. Rewriting its
// number would produce a v16 file that cannot load, which is the one outcome a
// migration must not produce.
func TestV15ToV16_RefusesABodyItCannotRead(t *testing.T) {
	raw := []byte("---\nmessage_version: 15\n---\nUE\x00\x00\x00\x05hello\n")

	if _, err := V15ToV16(raw); err == nil {
		t.Fatal("V15ToV16 accepted a body that is not one frame per line")
	}
}

// TestV15ToV16_SendsV14ToTheOtherStep mirrors the v13 refusal above: being told
// which script performs the missing conversion is the difference between a
// two-command upgrade and a puzzle.
func TestV15ToV16_SendsV14ToTheOtherStep(t *testing.T) {
	raw := []byte("---\nmessage_version: 14\n---\nUE\n")

	_, err := V15ToV16(raw)
	if err == nil {
		t.Fatal("V15ToV16 accepted a v14 file")
	}
	if !strings.Contains(err.Error(), "session_upgrade_v15.sh") {
		t.Errorf("refusal does not name the step to run first: %v", err)
	}
}
