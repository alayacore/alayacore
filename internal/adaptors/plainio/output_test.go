package plainio

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

// encodeTLV is a test helper that builds a TLV frame.
func encodeTLV(tag, value string) []byte {
	data := []byte(value)
	msg := make([]byte, 6+len(data))
	msg[0] = tag[0]
	msg[1] = tag[1]
	binary.BigEndian.PutUint32(msg[2:], uint32(len(data)))
	copy(msg[6:], data)
	return msg
}

func TestExtractStreamPrefix(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantPrefix  string
		wantContent string
	}{
		{"no prefix", "hello", "", "hello"},
		{"with prefix", "[:0-1-t:]hello", "[:0-1-t:]", "hello"},
		{"empty content", "[:0-1-t:]", "[:0-1-t:]", ""},
		{"malformed", "[:no close", "", "[:no close"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, content := extractStreamPrefix(tt.value)
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
			if content != tt.wantContent {
				t.Errorf("content = %q, want %q", content, tt.wantContent)
			}
		})
	}
}

func TestNewlineBetweenDifferentStreamGroups(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Simulate: assistant text delta (stream prefix "[:0-1-t:]")
	msg1 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]hello ")
	msg2 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]world")
	// New step: different stream prefix "[:0-2-t:]"
	msg3 := encodeTLV(stream.TagTextAssistant, "[:0-2-t:]new step")

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	want := "hello world\nnew step"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoNewlineWithinSameStreamGroup(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]hello ")
	msg2 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]world")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "hello world"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenTextAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]some text")
	msg2 := encodeTLV(stream.TagTextReasoning, "[:0-1-r:]some reasoning")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "some text\nsome reasoning"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenReasoningAndText(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTLV(stream.TagTextReasoning, "[:0-1-r:]thinking...")
	msg2 := encodeTLV(stream.TagTextAssistant, "[:0-2-t:]answer")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "thinking...\nanswer"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoPrefixNoNewline(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Messages without stream prefixes should not cause newlines
	msg1 := encodeTLV(stream.TagTextAssistant, "hello ")
	msg2 := encodeTLV(stream.TagTextAssistant, "world")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "hello world"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestToolCallResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Stream some text
	msg1 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]hello")
	// Then a tool call (resets prefix)
	msg2 := encodeTLV(stream.TagFunctionCall, `{"id":"1","name":"read_file","input":"{}"}`)
	// Then more text with different prefix — should NOT get extra newline since tool call reset it
	msg3 := encodeTLV(stream.TagTextAssistant, "[:0-3-t:]result")

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	// After tool call, lastStreamPrefix is "" so the new prefix "[:0-3-t:]" doesn't trigger separator
	if !contains(got, "hello") || !contains(got, "result") {
		t.Errorf("output = %q", got)
	}
}

func TestUserPromptResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTLV(stream.TagTextAssistant, "[:0-1-t:]response")
	msg2 := encodeTLV(stream.TagTextUser, "next prompt")
	msg3 := encodeTLV(stream.TagTextAssistant, "[:1-1-t:]new response")

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	if !contains(got, "response") || !contains(got, "new response") {
		t.Errorf("output = %q", got)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
