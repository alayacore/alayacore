package tools

import (
	"context"
	"strings"
	"testing"
)

func TestStreamingWriterSnapshot(t *testing.T) {
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	// First write: automatic flush fires (zero lastTick) and emits the
	// completed line via lastLine.
	w.Write([]byte("downloading\n"))
	// Progress-bar rewrites via '\r' — only the last state survives.
	w.Write([]byte(" 42%\r"))
	w.Write([]byte(" 99%"))
	w.flushPreview()

	// Authoritative buffer keeps the full output.
	if got := w.buf.String(); got != "downloading\n 42%\r 99%" {
		t.Errorf("buf = %q, want full output", got)
	}
	// Snapshot is the current line after the last '\r'.
	if got := w.tail.String(); got != " 99%" {
		t.Errorf("tail = %q, want %q", got, " 99%")
	}
	// Intermediates (" 42%") must never appear; final state wins.
	for _, s := range sent {
		if s == " 42%" {
			t.Errorf("intermediate progress state leaked: %v", sent)
		}
	}
	if len(sent) == 0 || sent[len(sent)-1] != " 99%" {
		t.Errorf("sent = %v, want last entry %q", sent, " 99%")
	}
}

func TestStreamingWriterTrailingCR(t *testing.T) {
	// A progress bar ending with '\r' keeps the pre-CR content as the
	// current line: the rewrite (if any) has not started yet.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })
	w.Write([]byte("build started\n"))
	w.Write([]byte(" 42%\r"))
	w.flushPreview()

	if got := w.tail.String(); got != " 42%" {
		t.Errorf("tail = %q, want %q (pre-CR content preserved)", got, " 42%")
	}
	if len(sent) == 0 || sent[len(sent)-1] != " 42%" {
		t.Errorf("sent = %v, want last entry %q", sent, " 42%")
	}
}

func TestStreamingWriterCRLF(t *testing.T) {
	// Windows CRLF line endings must not lose the completed line.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.Write([]byte("line one\r\n"))
	w.Write([]byte("line two\r\n"))
	w.flushPreview()

	if got := w.buf.String(); got != "line one\r\nline two\r\n" {
		t.Errorf("buf = %q", got)
	}
	if len(sent) == 0 || sent[len(sent)-1] != "line two" {
		t.Errorf("sent = %v, want last entry %q", sent, "line two")
	}
}

func TestStreamingWriterCRLFAcrossChunks(t *testing.T) {
	// '\r' at the end of one chunk and '\n' at the start of the next
	// is still a CRLF line ending.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.Write([]byte("partial\r"))
	w.Write([]byte("\nnext"))
	w.flushPreview()

	if got := w.buf.String(); got != "partial\r\nnext" {
		t.Errorf("buf = %q", got)
	}
	if got := w.tail.String(); got != "next" {
		t.Errorf("tail = %q, want %q", got, "next")
	}
	if len(sent) == 0 || sent[len(sent)-1] != "next" {
		t.Errorf("sent = %v, want last entry %q", sent, "next")
	}
}

func TestStreamingWriterMultiline(t *testing.T) {
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.Write([]byte("line1\n"))
	w.Write([]byte("line2\n"))
	w.Write([]byte("line3"))
	w.flushPreview()

	if got := w.buf.String(); got != "line1\nline2\nline3" {
		t.Errorf("buf = %q", got)
	}
	// Current line wins; completed lines are kept as lastLine fallback.
	if got := w.tail.String(); got != "line3" {
		t.Errorf("tail = %q, want %q", got, "line3")
	}
	if len(sent) == 0 || sent[len(sent)-1] != "line3" {
		t.Errorf("last sent = %v, want \"line3\"", sent)
	}
	// Completed lines appear via lastLine when the current line is empty.
	w2 := newStreamingWriter(func(s string) { sent = append(sent, s) })
	w2.Write([]byte("done\n"))
	w2.flushPreview()
	last := sent[len(sent)-1]
	if last != "done" {
		t.Errorf("last sent = %q, want %q", last, "done")
	}
}

func TestStreamingWriterLongLine(t *testing.T) {
	w := newStreamingWriter(nil)
	long := strings.Repeat("x", maxPreviewLen+100)
	w.Write([]byte(long))

	if got := w.tail.Len(); got != maxPreviewLen {
		t.Errorf("tail len = %d, want %d", got, maxPreviewLen)
	}
	if !strings.HasSuffix(w.tail.String(), strings.Repeat("x", maxPreviewLen)) {
		t.Error("tail should keep the last maxPreviewLen bytes")
	}
}

func TestStreamingWriterLastLineTruncated(t *testing.T) {
	// A single over-long line completed with '\n' must also have its
	// lastLine fallback truncated to maxPreviewLen.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })
	long := strings.Repeat("y", maxPreviewLen+100)
	w.Write([]byte(long + "\n"))
	w.flushPreview()

	if got := len(w.lastLine); got != maxPreviewLen {
		t.Errorf("lastLine len = %d, want %d", got, maxPreviewLen)
	}
	if len(sent) == 0 || len(sent[len(sent)-1]) != maxPreviewLen {
		t.Errorf("last preview len = %d, want %d", len(sent[len(sent)-1]), maxPreviewLen)
	}
}

func TestStreamingWriterNoDeltaCallback(t *testing.T) {
	// onDelta nil — preview machinery is inert, buffering still works.
	w := newStreamingWriter(nil)
	w.Write([]byte("hello\nworld"))
	w.flushPreview() // must not panic
	if got := w.buf.String(); got != "hello\nworld" {
		t.Errorf("buf = %q", got)
	}
}

func TestExecuteCommandStreamingEndToEnd(t *testing.T) {
	var previews []string
	contents, err := executeCommandStreaming(context.Background(), executeCommandInput{
		Command: "echo hello && echo world",
	}, func(s string) { previews = append(previews, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(contents)
	if text != "hello\nworld\n" {
		t.Errorf("result = %q, want %q", text, "hello\nworld\n")
	}

	// The preview channel must have delivered at least the last line.
	found := false
	for _, p := range previews {
		if strings.Contains(p, "world") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no preview containing %q, got %v", "world", previews)
	}
}
