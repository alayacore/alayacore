package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
)

// StdioTransport communicates with an MCP server via stdin/stdout.
// JSON-RPC messages are newline-delimited JSON (NDJSON).
//
// A single background goroutine (readLoop) reads all response lines from
// the scanner and dispatches them to waiting callers by request ID.
// This eliminates per-call goroutine leaks and prevents response
// desynchronization on context cancellation.
type StdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	scanner   *bufio.Scanner
	closeOnce sync.Once // guards the Close() cleanup sequence
	doneOnce  sync.Once // guards close(done) — shared with the monitor goroutine
	done      chan struct{}
	mu        sync.Mutex // protects send serialization

	// processExited is closed by the monitor goroutine when the child
	// process exits (cmd.Wait returns). Close() waits on it instead of
	// calling cmd.Wait() itself — os/exec.Cmd.Wait is NOT safe to call
	// concurrently from multiple goroutines (it mutates internal state).
	processExited chan struct{}

	// readLoop dispatches responses here by request ID.
	pending   map[requestID]chan<- jsonrpcResponse
	pendingMu sync.Mutex
	readerWg  sync.WaitGroup

	debugWriter io.WriteCloser // non-nil when --debug-log is enabled; logs raw JSON-RPC

	// stderrTail holds the last bytes the server wrote to stderr, so a
	// connection failure can report the real cause instead of a bare EOF.
	stderrTail *boundedTail

	// Notification handler for server-to-client notifications.
	notificationHandler NotificationHandler
}

// NewStdioTransport creates a stdio transport that spawns the given command.
// debugDir "" = no debug logging; "." = write to CWD.
func NewStdioTransport(command string, args []string, env map[string]string, debugDir string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Set environment variables.
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), mapToEnvSlice(env)...)
	}

	// Initialize the debug writer before spawning anything: a debug log the
	// user asked for but cannot be opened is a startup configuration error,
	// reported rather than silently connecting without logs. Creating it here
	// also means the failure path needs no process cleanup.
	var debugWriter io.WriteCloser
	if debugDir != "" {
		dw, err := debug.NewDebugWriter(debugDir, "alayacore-debug-mcp")
		if err != nil {
			stdin.Close()
			stdout.Close()
			return nil, err
		}
		debugWriter = dw
	}

	// Capture the server's stderr. With cmd.Stderr left nil, os/exec connects
	// the child's stderr to the null device, so every startup diagnostic
	// (missing module, unhandled rejection, Python ImportError) is discarded
	// and the user is left with a bare EOF/timeout error. Keep a bounded tail
	// of it to append to connection failures; do NOT pass it through to
	// os.Stderr, which would scribble over the TUI. When --debug-log is on,
	// also tee it to the transport log so a post-mortem has the full stream
	// rather than only the surviving tail.
	stderrTail := &boundedTail{max: maxStderrTail}
	var stderrSink io.Writer = stderrTail
	if debugWriter != nil {
		// No lock needed: os/exec copies a given stream from one goroutine,
		// so the two destinations are never written concurrently.
		stderrSink = io.MultiWriter(stderrTail, debugWriter)
	}
	cmd.Stderr = stderrSink

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		if debugWriter != nil {
			debugWriter.Close()
		}
		return nil, fmt.Errorf("start command: %w", err)
	}

	t := &StdioTransport{
		cmd:           cmd,
		stdin:         stdin,
		scanner:       bufio.NewScanner(stdout),
		done:          make(chan struct{}),
		processExited: make(chan struct{}),
		pending:       make(map[requestID]chan<- jsonrpcResponse),
		stderrTail:    stderrTail,
		debugWriter:   debugWriter,
	}

	if debugWriter != nil {
		fmt.Fprintf(debugWriter, "MCP debug log started for: %s %v\n", command, args)
	}

	// Start dedicated reader goroutine.
	t.readerWg.Add(1)
	go t.readLoop()

	// Monitor goroutine — the SOLE owner of cmd.Wait().
	// os/exec.Cmd.Wait is not safe to call concurrently: Close() waits on
	// processExited instead of calling Wait itself. On unexpected process
	// exit (Close() never called), doneOnce also closes done so callers
	// waiting on Done() are unblocked. doneOnce makes this race-free with
	// Close()'s own close(done).
	go func() {
		_ = cmd.Wait() // process exit detected via close(processExited)
		close(t.processExited)
		t.doneOnce.Do(func() { close(t.done) })
	}()

	return t, nil
}

// readLoop is the dedicated background goroutine that reads all JSON-RPC
// response lines from the scanner and dispatches them by request ID.
// Server-to-client requests (e.g. ping) are handled inline.
func (t *StdioTransport) readLoop() {
	defer t.readerWg.Done()

	// Context tied to transport lifetime: canceled when Close() is called.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-t.done; cancel() }()

	for t.scanner.Scan() {
		data := t.scanner.Bytes()
		if err := parseAndDispatchJSONRPC(ctx, data, t.pending, &t.pendingMu, t.debugWriter, t.handleServerRequest, t.notificationHandler); err != nil {
			if t.debugWriter != nil {
				fmt.Fprintf(t.debugWriter, "MCP: malformed response line (len=%d): %v\n",
					len(data), err)
			}
		}
	}

	// Scanner error or EOF — close all remaining pending channels.
	t.pendingMu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.pendingMu.Unlock()
}

// handleServerRequest handles a JSON-RPC request from the server (e.g. ping).
// Responses are sent back through the transport.
func (t *StdioTransport) handleServerRequest(_ context.Context, id requestID, method string) {
	switch method {
	case methodPing:
		// Respond with empty result.
		resp := jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Result:  json.RawMessage(`{}`),
		}
		data, _ := json.Marshal(resp) // static struct, cannot fail
		t.mu.Lock()
		_, _ = t.stdin.Write(append(data, '\n'))
		t.mu.Unlock()

	default:
		// Method not found — respond with error.
		resp := jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Error: &jsonrpcError{
				Code:    -32601, // METHOD_NOT_FOUND
				Message: "Method not found: " + method,
			},
		}
		data, _ := json.Marshal(resp) // static struct, cannot fail
		t.mu.Lock()
		_, _ = t.stdin.Write(append(data, '\n'))
		t.mu.Unlock()
	}
}

// SetNotificationHandler registers a handler for server-to-client notifications.
func (t *StdioTransport) SetNotificationHandler(h NotificationHandler) {
	t.notificationHandler = h
}

func (t *StdioTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	_ = ctx
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	data = append(data, '\n')

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> %s %s\n", req.Method, formatJSON(data[:len(data)-1]))
	}

	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// SendReceive sends a JSON-RPC request and waits for the matching response
// by request ID. On context cancellation, the pending request is unregistered
// and the response is discarded when it arrives — no transport disruption.
func (t *StdioTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	// Create a buffered channel for this request's response.
	respCh := make(chan jsonrpcResponse, 1)

	// Register the pending request before sending, so there's no race
	// between a fast response arriving and registration.
	t.pendingMu.Lock()
	select {
	case <-t.done:
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}
	t.pending[req.ID] = respCh
	t.pendingMu.Unlock()

	// Double-check: done may have been closed between the first check and
	// registration (readLoop exit → monitor closing done).  If so,
	// clean up immediately — don't leave an orphaned pending entry.
	select {
	case <-t.done:
		t.pendingMu.Lock()
		delete(t.pending, req.ID)
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}

	// Remove from pending map on any exit path.
	var success bool
	defer func() {
		if !success {
			t.pendingMu.Lock()
			delete(t.pending, req.ID)
			t.pendingMu.Unlock()
		}
	}()

	if err := t.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// Wait for the matching response.
	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, io.EOF
		}
		if resp.Error != nil {
			return nil, &RPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		success = true
		return resp.Result, nil

	case <-ctx.Done():
		// Context canceled — unregister (defer handles cleanup).
		// The response will arrive and be discarded by readLoop.
		return nil, ctx.Err()

	case <-t.done:
		return nil, io.EOF
	}
}

// Close terminates the MCP server process gracefully per the MCP spec:
//  1. Close stdin to signal EOF to the server
//  2. Wait for the server to exit (with timeout)
//  3. SIGTERM if still running
//  4. SIGKILL if still running after another timeout
//
// cmd.Wait() is owned exclusively by the monitor goroutine (see
// NewStdioTransport) — this method waits on processExited instead of
// calling Wait itself, avoiding the concurrent-Wait data race.
func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		// Step 1: Close stdin to signal EOF.
		t.stdin.Close()

		// Step 2: Wait for the process to exit on its own.
		select {
		case <-t.processExited:
			// Process exited cleanly.
		case <-time.After(2 * time.Second):
			// Step 3: SIGTERM.
			if t.cmd != nil && t.cmd.Process != nil {
				_ = t.cmd.Process.Signal(os.Signal(syscall.SIGTERM)) // best-effort
			}
			select {
			case <-t.processExited:
				// Process exited after SIGTERM.
			case <-time.After(3 * time.Second):
				// Step 4: SIGKILL.
				if t.cmd != nil && t.cmd.Process != nil {
					_ = t.cmd.Process.Kill() // SIGKILL always succeeds on Unix
				}
				<-t.processExited
			}
		}

		// Wait for readLoop to finish processing pending responses.
		t.readerWg.Wait()

		// Close debug log file if open.
		if t.debugWriter != nil {
			t.debugWriter.Close()
		}

		// Signal that transport is done. If the process died unexpectedly
		// (monitor closed done first), this is a no-op.
		t.doneOnce.Do(func() { close(t.done) })
	})
	return nil
}

// Done returns a channel that closes when the process exits.
func (t *StdioTransport) Done() <-chan struct{} {
	return t.done
}

// StderrTail returns the last bytes the server wrote to stderr, satisfying
// stderrDiagnostician so connection failures can name their real cause.
// Returns "" when the server printed nothing.
func (t *StdioTransport) StderrTail() string {
	if t.stderrTail == nil {
		return ""
	}
	return t.stderrTail.tail()
}
