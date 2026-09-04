package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Test Helper Process
// ============================================================================
//
// StdioTransport tests use the Go test helper process pattern:
// The test binary itself runs as the MCP server subprocess when
// MCP_TEST_SERVER=1 is set. This avoids needing to compile a
// separate binary and gives us full control over server behavior.
//
// The test server reads NDJSON from stdin and writes responses to
// stdout. It supports commands via environment variables:
//
//	MCP_TEST_SERVER_DELAY_MS  — delay before responding (ms)
//	MCP_TEST_SERVER_ECHO_RAW  — if set, echoes raw input back (no JSON-RPC parsing)
//	MCP_TEST_SERVER_WAIT_EOF  — if set, waits for stdin EOF before exiting
//
// The server responds to each request with a result containing the
// original method and params echoed back. Error responses are triggered
// when the method starts with "error/".

// TestMain handles the test helper subprocess pattern:
//   - If MCP_TEST_SERVER=1, run as the MCP server subprocess and exit.
//   - Otherwise, run tests normally.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		runMCPServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMCPServer implements a minimal MCP stdio server for testing.
// It reads NDJSON lines from stdin and writes responses to stdout.
//
// Supported method prefixes for special behaviors:
//
//	error/<name>   — respond with a JSON-RPC error
//	error/custom   — respond with error code -32000
//	s2c/<name>     — send a server-to-client ping before responding
//	batch/<name>   — respond with a JSON-RPC batch array (two responses)
//	malformed/<n>  — respond with invalid JSON bytes
func runMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	// Use a larger buffer for potentially large messages.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	delayStr := os.Getenv("MCP_TEST_SERVER_DELAY_MS")
	echoRaw := os.Getenv("MCP_TEST_SERVER_ECHO_RAW") != ""
	waitEOF := os.Getenv("MCP_TEST_SERVER_WAIT_EOF") != ""

	// Die-with-diagnostic mode: print to stderr and exit nonzero without ever
	// speaking the protocol. Models a real-world MCP server that fails at
	// startup ("Cannot find module …"), which is what the transport's stderr
	// tail exists to surface.
	if diag := os.Getenv("MCP_TEST_SERVER_DIE_WITH"); diag != "" {
		fmt.Fprintln(os.Stderr, diag)
		os.Exit(1)
	}

	for scanner.Scan() {
		data := scanner.Bytes()

		// Echo raw mode — just send back exactly what was received
		// as a JSON-RPC response.
		if echoRaw {
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			continue
		}

		// Parse the incoming JSON-RPC message.
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      requestID       `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			// Malformed input — skip.
			continue
		}

		// Delay if requested.
		if delayStr != "" {
			// strconv.Atoi, not fmt.Sscanf: Sscanf("%d") stops at the first
			// non-digit and reports success, so "5s" yields 5 with a nil error —
			// meaning "delay 5 seconds" would have silently become "delay 5
			// milliseconds" under a check that looked like it was validating the
			// value. Atoi rejects the whole string or accepts none of it.
			//
			// The helper has no *testing.T to fail, so a bad value has to be
			// reported some other way. Exiting beats what it replaces:
			// leaving ms at zero turns "respond after N ms" into "respond at
			// once", and the delay-dependent test then fails as a *missing
			// context cancellation* — a shape that reads as flaky and gets
			// re-run. Measured, not assumed: the failure text for that test is
			// the same either way, because its assertion is about behavior and
			// never sees a transport error. The message does reach a developer on
			// the paths the transport already wires — a child that dies during
			// connect gets its stderr appended by withStderrDetail (pinned by
			// TestConnectErrorNamesTheServerDiagnostics), and --debug-log captures
			// it regardless.
			ms, err := strconv.Atoi(delayStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "test server: cannot parse MCP_TEST_SERVER_DELAY_MS=%q: %v\n", delayStr, err)
				os.Exit(1)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}

		// Notifications (empty ID) get no response.
		if msg.ID == "" {
			continue
		}

		// ------------------------------------------------------------------
		// Special behavior based on method prefix
		// ------------------------------------------------------------------

		// Initialize: answer the handshake with the requested protocol version
		// so a full Client can connect to the test server and its transport can
		// be exercised end to end.
		if msg.Method == methodInitialize {
			var req struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &req)
			result, _ := json.Marshal(map[string]any{
				"protocolVersion": req.ProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "test-server", "version": "0"},
			})
			resp := jsonrpcResponse{JSONRPC: "2.0", ID: msg.ID, Result: result}
			data, _ := json.Marshal(resp)
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			continue
		}

		// Error mode: method starting with "error/" triggers an error response.
		if strings.HasPrefix(msg.Method, "error/") {
			code := -32601
			msgText := "Method not found: " + msg.Method
			if strings.Contains(msg.Method, "custom") {
				code = -32000
				msgText = "Custom error"
			}
			resp := jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error: &jsonrpcError{
					Code:    code,
					Message: msgText,
				},
			}
			data, _ := json.Marshal(resp)
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			continue
		}

		// Malformed mode: respond with non-JSON garbage.
		if strings.HasPrefix(msg.Method, "malformed/") {
			os.Stdout.Write([]byte("this is not json\n"))
			continue
		}

		// Ping from client → respond with empty result.
		if msg.Method == "ping" {
			resp := jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  json.RawMessage(`{}`),
			}
			data, _ := json.Marshal(resp)
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			continue
		}

		// Server-to-client request simulation.
		// If method starts with "s2c/", the server sends a request to the
		// client before responding to the original request.
		if strings.HasPrefix(msg.Method, "s2c/") {
			// Send a ping request to the client.
			serverReq := jsonrpcRequest{
				JSONRPC: "2.0",
				ID:      requestID("srv-ping"),
				Method:  "ping",
			}
			srvData, _ := json.Marshal(serverReq)
			os.Stdout.Write(srvData)
			os.Stdout.Write([]byte("\n"))
			// The server should read the client's response but we ignore it.
			// We need to consume the response from stdin to keep things in sync.
			if scanner.Scan() {
				// Client response consumed.
				_ = scanner.Bytes()
			}
		}

		// Normal response — echo method and params back in result.
		result := map[string]any{
			"echo_method": msg.Method,
		}
		if msg.Params != nil {
			result["echo_params"] = msg.Params
		}
		resp := jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  mustMarshal(result),
		}
		data, _ = json.Marshal(resp)
		os.Stdout.Write(data)
		os.Stdout.Write([]byte("\n"))
	}

	// If waitEOF is set, hang until stdin is actually closed.
	if waitEOF {
		// Read from stdin until EOF (after close).
		buf := make([]byte, 1)
		for {
			_, err := os.Stdin.Read(buf)
			if err != nil {
				break
			}
		}
	}

	// Slow-exit mode: linger this long before exiting so that transport
	// shutdown duration is observable. Reached only after stdin closes (the
	// read loop above) or the scan loop breaks, so it never delays the server
	// from serving requests.
	if s := os.Getenv("MCP_TEST_SERVER_EXIT_DELAY_MS"); s != "" {
		ms, err := strconv.Atoi(s)
		if err != nil {
			// Same reasoning as the request delay, with one difference worth
			// stating: no test currently notices this value at all. Feeding it
			// garbage leaves TestManagerCloseAllIsConcurrent passing both before
			// and after this check, because the delay only has to be long enough
			// for CloseAll to return first. So this line guards the value against
			// a future assertion, not a present one.
			fmt.Fprintf(os.Stderr, "test server: cannot parse MCP_TEST_SERVER_EXIT_DELAY_MS=%q: %v\n", s, err)
			os.Exit(1)
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// ============================================================================
// Test Helpers
// ============================================================================

// newStdioTestTransport creates a StdioTransport backed by the test binary
// itself as the subprocess. Additional environment variables can be passed
// to configure the test server behavior.
func newStdioTestTransport(t *testing.T, extraEnv map[string]string, debugDir string) *StdioTransport {
	t.Helper()

	// Build environment for the test server subprocess.
	env := map[string]string{
		"MCP_TEST_SERVER": "1",
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	transport, err := NewStdioTransport(
		os.Args[0],
		[]string{"-test.run=^TestStdioTransport$"},
		env,
		debugDir,
	)
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}

	// A failing stdio test usually failed in the child, and the child's stderr
	// goes to a bounded tail rather than the terminal (NewStdioTransport keeps it
	// so a dead server can be diagnosed at all). Only transport-level errors get
	// that tail attached, via withStderrDetail; a behavioral assertion that fails
	// produces no such error, and the reason stays unseen. Measured both ways:
	// without this hook, a bad MCP_TEST_SERVER_DELAY_MS fails the test as
	// "expected context cancellation error, got nil" — which reads as a flaky
	// timeout; with it, the run also prints "cannot parse ... invalid syntax".
	//
	// Registered above the Close cleanup, so LIFO ordering runs it last: Close()
	// waits for the child's stderr copy to finish, which guarantees the tail holds
	// everything it said, including output produced while dying.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if tail := transport.StderrTail(); tail != "" {
			t.Logf("test server stderr:\n%s", tail)
		}
	})

	// Register cleanup to ensure the subprocess is terminated.
	t.Cleanup(func() {
		transport.Close()
	})

	return transport
}

// waitForReady waits for the transport to be ready (not done).
// StdioTransport is ready immediately after construction, but we need
// to give the subprocess a moment to start.
func waitForReady(t *testing.T, transport *StdioTransport) {
	t.Helper()

	// The stdio transport is ready immediately after NewStdioTransport
	// returns (the subprocess is already started). Just verify Done
	// isn't closed.
	select {
	case <-transport.Done():
		t.Fatal("transport done channel is already closed")
	default:
	}
}

// ============================================================================
// Stdio Transport Tests
// ============================================================================

func TestStdioTransport_SendReceive(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return // skip when running as test server
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/method",
		Params:  json.RawMessage(`{"foo":"bar"}`),
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result["echo_method"] != "test/method" {
		t.Errorf("echo_method = %v, want %q", result["echo_method"], "test/method")
	}
}

func TestStdioTransport_SendReceive_Error(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	ctx := context.Background()
	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "error/unknown_method",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32601 {
		t.Errorf("error code = %d, want %d", rpcErr.Code, -32601)
	}
	if !strings.Contains(rpcErr.Message, "unknown_method") {
		t.Errorf("error message = %q, want containing 'unknown_method'", rpcErr.Message)
	}
}

func TestStdioTransport_Send_Notification(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	err := transport.Send(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/test",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// No response expected for notifications — just verify no error.
	// The server ignores notifications (empty ID).
}

func TestStdioTransport_SendReceive_WithNotification(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// Mix notifications and requests — notifications should not interfere.
	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	ctx := context.Background()

	// Send a notification first.
	err := transport.Send(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	if err != nil {
		t.Fatalf("Send notification error = %v", err)
	}

	// Then send a request — should still get a response.
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/after_notification",
	})
	if err != nil {
		t.Fatalf("SendReceive() after notification error = %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo_method"] != "test/after_notification" {
		t.Errorf("echo_method = %v, want %q", result["echo_method"], "test/after_notification")
	}
}

func TestStdioTransport_Close(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	// Close should not block or error.
	if err := transport.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Double close should be safe.
	if err := transport.Close(); err != nil {
		t.Errorf("Close() second call error = %v", err)
	}

	// Done should be closed.
	select {
	case <-transport.Done():
		// OK
	default:
		t.Error("Done() should be closed after Close()")
	}

	// SendReceive after close should return error.
	_, err := transport.SendReceive(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err == nil {
		t.Error("expected error after close, got nil")
	}
}

func TestStdioTransport_ContextCancellation(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, map[string]string{
		"MCP_TEST_SERVER_DELAY_MS": "5000", // Server delays 5s
	}, "")
	waitForReady(t, transport)

	// Create a context that cancels quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/slow",
	})
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestStdioTransport_ServerPing(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// The s2c/ prefix tells the test server to send a server-to-client
	// ping request before responding to the original request.
	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "s2c/ping_test",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo_method"] != "s2c/ping_test" {
		t.Errorf("echo_method = %v, want %q", result["echo_method"], "s2c/ping_test")
	}
}

func TestStdioTransport_MultipleConcurrentRequests(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	// Send 5 concurrent requests.
	const numRequests = 5
	var wg sync.WaitGroup
	errs := make([]error, numRequests)
	results := make([]string, numRequests)

	for i := range numRequests {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			resp, err := transport.SendReceive(context.Background(), jsonrpcRequest{
				JSONRPC: "2.0",
				ID:      requestID(fmt.Sprintf("c%d", idx)),
				Method:  fmt.Sprintf("concurrent/test/%d", idx),
			})
			if err != nil {
				errs[idx] = err
				return
			}

			var result map[string]any
			if err := json.Unmarshal(resp, &result); err != nil {
				errs[idx] = err
				return
			}
			results[idx] = fmt.Sprintf("method=%v", result["echo_method"])
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
		if results[i] != fmt.Sprintf("method=concurrent/test/%d", i) {
			t.Errorf("request %d result = %q, want %q", i, results[i], fmt.Sprintf("method=concurrent/test/%d", i))
		}
	}
}

func TestStdioTransport_DebugLogging(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// Debug mode should not crash.
	transport := newStdioTestTransport(t, nil, t.TempDir())
	waitForReady(t, transport)
	t.Cleanup(func() { transport.debugWriter.Close() })

	ctx := context.Background()
	resp, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/debug",
	})
	if err != nil {
		t.Fatalf("SendReceive() error = %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["echo_method"] != "test/debug" {
		t.Errorf("echo_method = %v, want %q", result["echo_method"], "test/debug")
	}

	// Also test notification with debug logging.
	err = transport.Send(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/debug_test",
	})
	if err != nil {
		t.Fatalf("Send() with debug error = %v", err)
	}
}

func TestStdioTransport_DoneOnClose(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	transport.Close()

	select {
	case <-transport.Done():
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Done() after Close()")
	}
}

func TestStdioTransport_DoneOnProcessExit(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, map[string]string{
		"MCP_TEST_SERVER_WAIT_EOF": "1",
	}, "")
	waitForReady(t, transport)

	// Close stdin to trigger server exit → then readLoop should exit
	// and done should be signaled.
	transport.stdin.Close()

	select {
	case <-transport.Done():
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Done() after stdin close")
	}
}

func TestStdioTransport_SendReceive_InvalidJSON(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// The test server responds with malformed (non-JSON) data when the
	// method starts with "malformed/". The readLoop should log a warning
	// and continue without crashing. The pending channel should eventually
	// be cleaned up when the transport is closed.
	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	// Register a pending request.
	ch := make(chan jsonrpcResponse, 1)
	transport.pendingMu.Lock()
	transport.pending["bad"] = ch
	transport.pendingMu.Unlock()

	// Use a context with timeout so the test doesn't hang.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := transport.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("bad"),
		Method:  "malformed/test",
	})
	if err == nil {
		t.Fatal("expected error from malformed response, got nil")
	}
	// Expected: context deadline exceeded because the response was
	// malformed and never dispatched to the channel.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("SendReceive error (expected deadline): %v", err)
	}

	// The pending entry should have been cleaned up by SendReceive's
	// defer (on context cancellation path).
	transport.pendingMu.Lock()
	_, ok := transport.pending["bad"]
	transport.pendingMu.Unlock()
	if ok {
		t.Error("pending entry 'bad' was not cleaned up after context cancel")
	}
}

func TestStdioTransport_ProcessExitDuringSendReceive(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// Use wait-eof mode so the server doesn't exit until stdin is closed.
	transport := newStdioTestTransport(t, map[string]string{
		"MCP_TEST_SERVER_WAIT_EOF": "1",
	}, "")
	waitForReady(t, transport)

	// Register a pending request.
	ch := make(chan jsonrpcResponse, 1)
	transport.pendingMu.Lock()
	transport.pending["orphan"] = ch
	transport.pendingMu.Unlock()

	// Close the transport — this closes stdin, which makes the server
	// exit, which closes readLoop, which cleans up all pending channels.
	transport.Close()

	// The orphan channel should be closed (readLoop cleans up).
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for channel cleanup")
	}
}

func TestStdioTransport_EnvironmentVariables(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	// Verify that environment variables are passed to the subprocess.
	transport := newStdioTestTransport(t, map[string]string{
		"MCP_TEST_CUSTOM_VAR": "custom_value",
	}, "")
	waitForReady(t, transport)

	// The test server inherits the environment. We can verify by checking
	// the transport's cmd.Env includes our custom variable.
	found := false
	for _, env := range transport.cmd.Env {
		if env == "MCP_TEST_CUSTOM_VAR=custom_value" {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom environment variable not found in subprocess env")
	}

	// Also verify the MCP_TEST_SERVER=1 is present.
	found = false
	for _, env := range transport.cmd.Env {
		if env == "MCP_TEST_SERVER=1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("MCP_TEST_SERVER=1 not found in subprocess env")
	}
}

func TestStdioTransport_SendReceive_WithCustomError(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	// "error/custom" triggers a custom error code -32000 in the test server.
	_, err := transport.SendReceive(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "error/custom_error",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32000 {
		t.Errorf("error code = %d, want %d", rpcErr.Code, -32000)
	}
	if rpcErr.Message != "Custom error" {
		t.Errorf("error message = %q, want %q", rpcErr.Message, "Custom error")
	}
}
