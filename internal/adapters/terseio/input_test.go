package terseio

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

func TestReadAllPrompt_MultiLine(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader("line one\nline two\n\n"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}

	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2 (UT + UE), got %q", len(frames), buf.String())
	}
	if frames[0].tag != tlv.TagUserT {
		t.Errorf("frame[0] tag = %q, want %q", frames[0].tag, tlv.TagUserT)
	}
	// Trailing newlines trimmed, inner newlines preserved.
	if frames[0].value != "line one\nline two" {
		t.Errorf("prompt = %q, want %q", frames[0].value, "line one\nline two")
	}
	if frames[1].tag != tlv.TagUserEnd {
		t.Errorf("frame[1] tag = %q, want %q", frames[1].tag, tlv.TagUserEnd)
	}
}

func TestReadAllPrompt_SingleLineNoNewline(t *testing.T) {
	var buf bytes.Buffer
	err := readAllPrompt(&buf, strings.NewReader("single line"))
	if err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}
	frames := parseTLVFrames(t, buf.Bytes())
	if len(frames) != 2 || frames[0].value != "single line" {
		t.Errorf("frames = %+v, want UT with %q + UE", frames, "single line")
	}
}

func TestReadAllPrompt_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	if err := readAllPrompt(&buf, strings.NewReader("\n\n")); err != nil {
		t.Fatalf("readAllPrompt() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty input produced %d bytes, want 0", buf.Len())
	}
}
