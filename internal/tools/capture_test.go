package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// newTestCapture returns a capture preloaded with s, for tests that build tool
// output without running a command.
func newTestCapture(t *testing.T, s string) *capture {
	t.Helper()
	c := newCapture(maxCommandOutput)
	if _, err := c.Write([]byte(s)); err != nil {
		t.Fatalf("preload: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestCaptureStaysInMemoryUnderBudget(t *testing.T) {
	c := newCapture(1024)
	defer c.Close()

	data := strings.Repeat("a", 1024) // exactly the budget
	if _, err := c.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}

	if c.spilled() {
		t.Error("a stream at the budget must not spill")
	}
	if c.String() != data {
		t.Errorf("String() length = %d, want %d", len(c.String()), len(data))
	}
}

// The inline rendering must never pull a spilled stream back into memory.
// Before this was pinned, the "could not save to a file" fallback called
// writeOut into a strings.Builder: with a 500MB spill and a full disk — the
// precise combination that triggers the fallback — it re-allocated the whole
// spill, reintroducing the very bug the capture exists to prevent.
func TestLargeOutputFallbackStaysMemoryBounded(t *testing.T) {
	const spillSize = 8 * 1024 * 1024 // 8MB, far over the 64KB inline budget

	stdout := newCapture(maxCommandOutput)
	defer stdout.Close()
	if _, err := stdout.Write([]byte(strings.Repeat("m", spillSize))); err != nil {
		t.Fatal(err)
	}
	if !stdout.spilled() {
		t.Fatal("test setup: expected the stream to have spilled")
	}
	stderr := newCapture(maxCommandOutput)
	defer stderr.Close()

	// Make saving fail the same way a full/read-only temp dir would.
	saved := procTmpDir
	procTmpDirOnce.Do(procTmpDirInit)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	procTmpDir = filepath.Join(blocker, "sub")
	defer func() { procTmpDir = saved }()

	parts, err := handleCommandOutput(stdout, stderr, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := parts[0].(*llm.TextPart).Text

	// Bounded: the in-memory prefix plus the explanatory note — never the
	// whole spilled stream.
	if len(text) > maxCommandOutput+4096 {
		t.Errorf("fallback returned %d bytes; the spilled %d bytes were materialized into memory",
			len(text), spillSize)
	}
	if !strings.Contains(text, "could not be saved to a file") {
		t.Errorf("expected an explanation of the failure, got %.200q", text)
	}
}

// The regression this guards: output used to be buffered whole before the cap
// was applied, so 286MB of command output cost ~1.1GB of heap to deliver a
// 126-byte answer, and with the default unlimited timeout a non-terminating
// producer grew until the OOM killer ended the process.
func TestCaptureBoundsMemoryAndKeepsEverything(t *testing.T) {
	const (
		budget = 4096
		rounds = 150 // 150 x 4KB = 600KB, ~150x the budget
	)

	c := newCapture(budget)
	defer c.Close()

	chunk := []byte(strings.Repeat("z", budget))
	for range rounds {
		if _, err := c.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}

	if !c.spilled() {
		t.Fatal("expected the stream to have spilled to disk")
	}
	if got, want := c.size(), int64(rounds*budget); got != want {
		t.Errorf("size() = %d, want %d", got, want)
	}
	if c.mem.Len() > budget {
		t.Errorf("in-memory prefix grew to %d bytes, over the %d budget", c.mem.Len(), budget)
	}

	// Nothing may be lost: the whole stream must be recoverable by streaming.
	var rebuilt bytes.Buffer
	if err := c.writeOut(&rebuilt); err != nil {
		t.Fatalf("writeOut: %v", err)
	}
	if rebuilt.Len() != rounds*budget {
		t.Errorf("recovered %d bytes, want %d", rebuilt.Len(), rounds*budget)
	}
	if strings.TrimLeft(rebuilt.String(), "z") != "" {
		t.Error("recovered content is corrupted")
	}
}

func TestCaptureLineTotals(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   int64
	}{
		{"empty", nil, 0},
		{"partial last line counts", []string{"a\nb"}, 2},
		{"trailing newline adds nothing", []string{"a\nb\n"}, 2},
		{"split across writes", []string{"a\n", "b", "\nc"}, 3},
		{"split mid-newline-free run", []string{"aaaa", "aa"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCapture(1 << 20)
			defer c.Close()
			for _, ch := range tt.chunks {
				if _, err := c.Write([]byte(ch)); err != nil {
					t.Fatal(err)
				}
			}
			if got := c.lineTotal(); got != tt.want {
				t.Errorf("lineTotal() = %d, want %d", got, tt.want)
			}
		})
	}
}

// A capture whose spill file cannot be created must drop the excess and say so,
// never fall back to unbounded growth.
func TestCaptureDropsExcessWhenSpillUnavailable(t *testing.T) {
	// Consume the lazy temp-dir initializer first: otherwise the first
	// createProcTmpFile below would run procTmpDirInit and overwrite the
	// sabotaged path, making this test depend on which test ran before it.
	procTmpDirOnce.Do(procTmpDirInit)
	saved := procTmpDir
	// CreateTemp fails because the parent is an ordinary file, not a
	// directory — a check that holds regardless of who runs the suite.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	procTmpDir = filepath.Join(blocker, "sub")
	defer func() { procTmpDir = saved }()

	c := newCapture(64)
	defer c.Close()

	if _, err := c.Write([]byte(strings.Repeat("q", 1000))); err != nil {
		t.Fatalf("Write must not fail on a dropped spill: %v", err)
	}
	// Many further writes must keep dropping rather than retrying the
	// filesystem (or, worse, buffering in RAM).
	for range 50 {
		if _, err := c.Write([]byte(strings.Repeat("w", 1000))); err != nil {
			t.Fatalf("Write must not fail on a dropped spill: %v", err)
		}
	}
	if !c.truncated() {
		t.Fatal("expected the capture to report truncation")
	}
	if c.mem.Len() > 64 {
		t.Errorf("memory grew to %d bytes, over the 64 budget", c.mem.Len())
	}
	if c.size() != 51000 {
		t.Errorf("size() = %d, want the true 51000 — accounting must not lie", c.size())
	}
}

// Oversized command output is saved to a file and the message points at it.
func TestHandleLargeCommandOutputSavesWholeStream(t *testing.T) {
	stdout := newTestCapture(t, strings.Repeat("L\n", 40000)) // 80KB > 64KB budget
	defer stdout.Close()
	stderr := newTestCapture(t, "")
	defer stderr.Close()

	parts, err := handleCommandOutput(stdout, stderr, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := parts[0].(*llm.TextPart).Text
	if !strings.Contains(text, "saved to:") {
		t.Fatalf("expected a saved-file message, got %.200q", text)
	}
	if strings.Contains(text, "LLLL") {
		t.Fatal("oversized output must not be returned inline")
	}

	path := savedPathFromMessage(t, text)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("referenced file unreadable: %v", err)
	}
	if int64(len(data)) != stdout.size() {
		t.Errorf("saved %d bytes, capture reported %d", len(data), stdout.size())
	}
}

func savedPathFromMessage(t *testing.T, msg string) string {
	t.Helper()
	_, after, ok := strings.Cut(msg, "saved to: ")
	if !ok {
		t.Fatalf("no 'saved to:' in %q", msg)
	}
	return strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
}
