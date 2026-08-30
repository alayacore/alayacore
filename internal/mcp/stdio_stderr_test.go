package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The transport used to leave cmd.Stderr nil, which os/exec documents as
// "connect to the null device" — not, as the comment claimed, "flow to the
// parent's stderr naturally". Every startup diagnostic was discarded, so a
// server that died on launch produced a bare EOF with no cause.

func TestStdioTransportCapturesServerStderr(t *testing.T) {
	const diagnostic = "Error: Cannot find module '@acme/mcp-server-db'"

	tr, err := NewStdioTransport(os.Args[0],
		[]string{"-test.run=^TestStdioTransportCapturesServerStderr$"},
		map[string]string{
			"MCP_TEST_SERVER":          "1",
			"MCP_TEST_SERVER_DIE_WITH": diagnostic,
		}, "")
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}

	// Close waits for the process to exit, and cmd.Wait only returns once the
	// stderr pipe has been drained, so the tail is complete afterwards.
	tr.Close()

	if got := tr.StderrTail(); got != diagnostic {
		t.Errorf("StderrTail() = %q, want %q", got, diagnostic)
	}
}

func TestConnectErrorNamesTheServerDiagnostics(t *testing.T) {
	const diagnostic = "postgres: FATAL password authentication failed for user"

	c := NewClient(ServerConfig{
		Name:         "failing-db",
		ProtoVersion: protocolVersion20250326,
		Command:      os.Args[0],
		Args:         []string{"-test.run=^TestConnectErrorNamesTheServerDiagnostics$"},
		Env: map[string]string{
			"MCP_TEST_SERVER":          "1",
			"MCP_TEST_SERVER_DIE_WITH": diagnostic,
		},
	})

	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected Connect to fail against a server that dies")
	}
	if !strings.Contains(err.Error(), diagnostic) {
		t.Errorf("Connect() error = %q, want it to include the server's diagnostic %q", err, diagnostic)
	}
}

func TestWithStderrDetail(t *testing.T) {
	sentinel := errors.New("EOF")

	if got := withStderrDetail(nil, &StdioTransport{}); got != nil {
		t.Errorf("withStderrDetail(nil, …) = %v, want nil", got)
	}

	// A transport with nothing captured keeps the error unchanged.
	empty := &StdioTransport{}
	if got := withStderrDetail(sentinel, empty); !errors.Is(got, sentinel) || got.Error() != sentinel.Error() {
		t.Errorf("withStderrDetail with empty tail = %q, want the bare error", got)
	}

	full := &StdioTransport{stderrTail: &boundedTail{max: maxStderrTail}}
	_, _ = full.stderrTail.Write([]byte("boom: missing config"))
	got := withStderrDetail(sentinel, full)
	if !errors.Is(got, sentinel) {
		t.Errorf("wrapped error must keep the original chain, got %v", got)
	}
	if !strings.Contains(got.Error(), "boom: missing config") {
		t.Errorf("withStderrDetail() = %q, want it to carry the tail", got)
	}
}

// boundedTail must keep only the tail and survive concurrent use: exec copies
// the child's stderr on its own goroutine while the parent reads the tail.
func TestBoundedTailKeepsLastBytesAndIsConcurrencySafe(t *testing.T) {
	b := &boundedTail{max: 16}

	done := make(chan struct{})
	go func() {
		for range 200 {
			_, _ = b.Write([]byte("0123456789"))
		}
		close(done)
	}()
	for range 50 {
		_ = b.tail()
	}
	<-done

	if got := b.tail(); len(got) > 16 {
		t.Errorf("tail length = %d, want <= 16 (unbounded growth is what this guards)", len(got))
	}
	if !strings.HasSuffix(b.tail(), "89") {
		t.Errorf("tail = %q, want the most recent bytes", b.tail())
	}
}

// docs/mcp.md promises that --debug-log captures the server's stderr. This
// test is what keeps that promise honest.
func TestStdioTransportWritesServerStderrToDebugLog(t *testing.T) {
	const diagnostic = "server: fatal config error: missing API key"
	dir := t.TempDir()

	tr, err := NewStdioTransport(os.Args[0],
		[]string{"-test.run=^TestStdioTransportWritesServerStderrToDebugLog$"},
		map[string]string{
			"MCP_TEST_SERVER":          "1",
			"MCP_TEST_SERVER_DIE_WITH": diagnostic,
		}, dir)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	tr.Close() // also closes the debug writer, flushing it

	logs, err := filepath.Glob(filepath.Join(dir, "alayacore-debug-mcp-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("no debug log was created")
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), diagnostic) {
		t.Errorf("debug log does not contain the server stderr.\nlog=%q\nwant=%q", data, diagnostic)
	}
}
