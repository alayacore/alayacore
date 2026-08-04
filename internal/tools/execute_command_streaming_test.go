package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
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
	if got := w.out.text(); got != " 99%" {
		t.Errorf("out snapshot = %q, want %q", got, " 99%")
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
	if got := w.out.text(); got != "line3" {
		t.Errorf("out snapshot = %q, want %q", got, "line3")
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

	if got := w.out.tail.Len(); got != maxPreviewLen {
		t.Errorf("tail len = %d, want %d", got, maxPreviewLen)
	}
	if !strings.HasSuffix(w.out.text(), strings.Repeat("x", maxPreviewLen)) {
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

	if got := len(w.out.lastLine); got != maxPreviewLen {
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
	w.WriteErr([]byte("warning\n"))
	w.flushPreview() // must not panic
	if got := w.buf.String(); got != "hello\nworld" {
		t.Errorf("buf = %q", got)
	}
	if got := w.errBuf.String(); got != "warning\n" {
		t.Errorf("errBuf = %q", got)
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

	if got := w.out.text(); got != " 42%" {
		t.Errorf("out snapshot = %q, want %q (pre-CR content preserved)", got, " 42%")
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
	if got := w.out.text(); got != "next" {
		t.Errorf("out snapshot = %q, want %q", got, "next")
	}
	if len(sent) == 0 || sent[len(sent)-1] != "next" {
		t.Errorf("sent = %v, want last entry %q", sent, "next")
	}
}

func TestStreamingWriterStderrOnly(t *testing.T) {
	// Progress bars on stderr (git/docker style) with empty stdout:
	// the preview must show the stderr line.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.WriteErr([]byte("Receiving objects:  42%\r"))
	w.WriteErr([]byte("Receiving objects:  99%\r"))
	w.flushPreview()

	if got := w.errBuf.String(); got != "Receiving objects:  42%\rReceiving objects:  99%\r" {
		t.Errorf("errBuf = %q", got)
	}
	if len(sent) == 0 || sent[len(sent)-1] != "Receiving objects:  99%" {
		t.Errorf("sent = %v, want stderr progress line", sent)
	}
}

func TestStreamingWriterRecentStreamStderr(t *testing.T) {
	// Most recently written stream wins: Write then WriteErr → stderr line.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.Write([]byte("Compiling main.go...\n"))
	w.WriteErr([]byte("warning: unused variable x\n"))
	w.flushPreview()

	if len(sent) == 0 {
		t.Fatal("no preview emitted")
	}
	if got := sent[len(sent)-1]; got != "warning: unused variable x" {
		t.Errorf("preview = %q, want stderr line (most recent)", got)
	}
}

func TestStreamingWriterRecentStreamStdout(t *testing.T) {
	// stderr written first, stdout most recent → stdout line wins.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	w.WriteErr([]byte("warning: x\n"))
	w.Write([]byte("Compiling main.go...\n"))
	w.Write([]byte("Linking..."))
	w.flushPreview()

	if len(sent) == 0 {
		t.Fatal("no preview emitted")
	}
	if got := sent[len(sent)-1]; got != "Linking..." {
		t.Errorf("preview = %q, want stdout line (most recent)", got)
	}
}

func TestStreamingWriterPreviewFallback(t *testing.T) {
	// Preferred stream empty → falls back to the other stream.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })

	// stderr is most recent but wrote only an empty line; stdout has content.
	w.Write([]byte("build output\n"))
	w.WriteErr([]byte("\n"))
	w.flushPreview()

	if len(sent) == 0 {
		t.Fatal("no preview emitted")
	}
	if got := sent[len(sent)-1]; got != "build output" {
		t.Errorf("preview = %q, want fallback to stdout", got)
	}
}

func TestStreamingWriterStderrCRLF(t *testing.T) {
	// CRLF on stderr must not lose the line either.
	var sent []string
	w := newStreamingWriter(func(s string) { sent = append(sent, s) })
	w.WriteErr([]byte("error: boom\r\n"))
	w.flushPreview()
	if len(sent) == 0 || sent[len(sent)-1] != "error: boom" {
		t.Errorf("sent = %v, want stderr line", sent)
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

func TestExecuteCommandStreamingStderrEndToEnd(t *testing.T) {
	var previews []string
	contents, err := executeCommandStreaming(context.Background(), executeCommandInput{
		Command: "echo out-line && echo err-line >&2",
	}, func(s string) { previews = append(previews, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(contents)
	if !strings.Contains(text, "out-line") || !strings.Contains(text, "err-line") {
		t.Errorf("result = %q, want both stdout and stderr lines", text)
	}

	// stderr line must appear in the previews.
	found := false
	for _, p := range previews {
		if strings.Contains(p, "err-line") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no preview containing %q, got %v", "err-line", previews)
	}
}

// TestStreamingWriterConcurrent verifies that concurrent Write/WriteErr
// calls from exec's stdout/stderr copy goroutines are race-free. Run
// with -race.
func TestStreamingWriterConcurrent(t *testing.T) {
	w := newStreamingWriter(func(string) {})

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i == 0 {
					if _, err := w.Write([]byte("stdout line\n")); err != nil {
						t.Errorf("Write: %v", err)
						return
					}
				} else {
					if _, err := w.WriteErr([]byte("stderr line\n")); err != nil {
						t.Errorf("WriteErr: %v", err)
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	w.flushPreview()

	// Both authoritative buffers must be complete and intact.
	if got := w.buf.String(); got != strings.Repeat("stdout line\n", 200) {
		t.Errorf("buf len = %d, want %d", len(got), len("stdout line\n")*200)
	}
	if got := w.errBuf.String(); got != strings.Repeat("stderr line\n", 200) {
		t.Errorf("errBuf len = %d, want %d", len(got), len("stderr line\n")*200)
	}
}

func TestStreamingWriterRuneBoundaryTruncation(t *testing.T) {
	// Block glyphs (█, 3 bytes each in UTF-8) must never be split by the
	// byte-cap truncation: the preview must end on a valid rune.
	block := "█" // U+2588, 3 bytes
	long := strings.Repeat(block, maxPreviewLen+10)

	w := newStreamingWriter(nil)
	w.Write([]byte(long))

	out := w.out.text()
	if !utf8.ValidString(out) {
		t.Fatalf("preview is not valid UTF-8: %q", out)
	}
	if len(out) > maxPreviewLen {
		t.Errorf("preview len = %d, want <= %d", len(out), maxPreviewLen)
	}
	// The preview must end with a complete block glyph (or be all blocks).
	if !strings.HasSuffix(out, block) {
		t.Errorf("preview should end with a complete block glyph, got tail %q", out[len(out)-6:])
	}
}

func TestStreamingWriterRuneBoundaryLastLine(t *testing.T) {
	// Same guarantee for the lastLine fallback path.
	long := strings.Repeat("界", maxPreviewLen+10) // CJK, 3 bytes each

	w := newStreamingWriter(nil)
	w.Write([]byte(long + "\n"))

	if !utf8.ValidString(w.out.lastLine) {
		t.Fatalf("lastLine is not valid UTF-8: %q", w.out.lastLine)
	}
	if len(w.out.lastLine) > maxPreviewLen {
		t.Errorf("lastLine len = %d, want <= %d", len(w.out.lastLine), maxPreviewLen)
	}
	if !strings.HasSuffix(w.out.lastLine, "界") {
		t.Errorf("lastLine should end with a complete CJK char, got tail %q", w.out.lastLine[len(w.out.lastLine)-6:])
	}
}
