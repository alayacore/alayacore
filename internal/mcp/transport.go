package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Transport defines the interface for MCP communication channels.
// MCP uses JSON-RPC 2.0 over stdio, Streamable HTTP, or other transports.
type Transport interface {
	// Send marshals and sends a JSON-RPC notification (no response expected).
	// The context is used for cancellation and timeout — particularly for
	// transports where sending may block (e.g. SSE HTTP POST).
	Send(ctx context.Context, req jsonrpcRequest) error

	// SendReceive sends a JSON-RPC request and waits for the matching
	// response, matched by request ID. Context cancellation unregisters
	// the pending request without affecting the transport — the response
	// is simply discarded when it arrives.
	SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error)

	// Close shuts down the transport.
	Close() error

	// Done returns a channel that's closed when the transport has
	// encountered a fatal error or been closed.
	Done() <-chan struct{}
}

// ============================================================================
// Shared Helpers
// ============================================================================

// maxStderrTail bounds how many bytes of an MCP server's stderr are kept for
// inclusion in connection error messages. A chatty or runaway server cannot
// grow this, and the tail is the useful part (the last thing it printed
// before dying).
const maxStderrTail = 8 * 1024

// maxTextResponseBytes bounds a non-JSON response body that MCP turns into an
// error message. Such a message is shown to the user and fed to the model, so
// the remote server must not get to decide its size.
const maxTextResponseBytes = 1 << 20 // 1MB

// boundedTail is a goroutine-safe ring buffer keeping the last max bytes
// written to it. It implements io.Writer, which makes it suitable as
// exec.Cmd.Stderr: the child's output is copied by an exec-internal
// goroutine while the parent may read the tail at any time.
type boundedTail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *boundedTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

// tail returns the last max bytes captured, trimmed of surrounding
// whitespace. Empty when the process wrote nothing.
func (b *boundedTail) tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.buf))
}

// stderrDiagnostician is implemented by transports whose remote process can
// report diagnostic output (a stdio MCP server writes to stderr). It is an
// optional capability rather than part of Transport because HTTP transports
// have no such stream.
type stderrDiagnostician interface {
	StderrTail() string
}

// withStderrDetail enriches a transport error with the server's captured
// stderr, so a failed connection reports the cause ("Cannot find module
// '@modelcontextprotocol/server-x'") instead of a bare EOF or timeout.
// Returns err unchanged when there is nothing to add.
func withStderrDetail(err error, tp Transport) error {
	if err == nil {
		return nil
	}
	sd, ok := tp.(stderrDiagnostician)
	if !ok {
		return err
	}
	tail := sd.StderrTail()
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w (server stderr: %s)", err, tail)
}

// mapToEnvSlice converts a map[string]string to "KEY=VALUE" strings.
func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}

// formatJSON pretty-prints a JSON byte slice for debug logging.
// Falls back to the raw string on error.
func formatJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}

// ============================================================================
// JSON-RPC Message Dispatch
// ============================================================================

// dispatchResponse sends a JSON-RPC response to the waiting caller by
// matching request ID. If no caller is waiting, the response is discarded.
// The pending map must be protected by mu.
func dispatchResponse(resp jsonrpcResponse, pending map[requestID]chan<- jsonrpcResponse, mu sync.Locker, debugWriter io.Writer, rawData []byte) {
	mu.Lock()
	ch, ok := pending[resp.ID]
	if ok {
		delete(pending, resp.ID)
	}
	mu.Unlock()

	if debugWriter != nil && rawData != nil {
		fmt.Fprintf(debugWriter, "<<< %s\n", formatJSON(rawData))
	}

	if ok {
		// Non-blocking send; channel has buffer 1 and receiver is
		// waiting (unless context was canceled after lookup).
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
	// No pending request for this ID — discard the response.
}

// ServerRequestHandler is a callback for handling server-to-client requests
// on SSE streams. In 2025-11-25, servers may send requests such as ping.
// In 2026-07-28+, servers do not send JSON-RPC requests (they use MRTR
// InputRequiredResult instead). The handler should respond to the request
// using the transport's Send method.
type ServerRequestHandler func(ctx context.Context, id requestID, method string)

// NotificationHandler is a callback for handling server-to-client notifications.
// These are JSON-RPC notifications (no ID) sent by the server (e.g.
// notifications/tools/list_changed).
type NotificationHandler func(method string)

// parseAndDispatchJSONRPC parses a JSON-RPC message and dispatches
// responses to waiting callers. Returns nil on success, or an error
// if the data cannot be parsed.
//
// Batch responses are not supported (MCP uses single-response only).
// Server-to-client requests are detected and forwarded to handleServerReq.
// Server-to-client notifications (no ID) are forwarded to handleNotification.
func parseAndDispatchJSONRPC(ctx context.Context, data []byte, pending map[requestID]chan<- jsonrpcResponse, mu sync.Locker, debugWriter io.Writer, handleServerReq ServerRequestHandler, handleNotification NotificationHandler) error {
	// Check for server-to-client requests first.
	// A JSON-RPC request has a "method" field and an "id" field.
	var reqFields struct {
		Method string    `json:"method"`
		ID     requestID `json:"id"`
	}
	if err := json.Unmarshal(data, &reqFields); err == nil && reqFields.Method != "" {
		if reqFields.ID != "" && handleServerReq != nil {
			handleServerReq(ctx, reqFields.ID, reqFields.Method)
		} else if handleNotification != nil {
			handleNotification(reqFields.Method)
		}
		// Notifications (no ID) are silently accepted even without handler.
		return nil
	}

	// Try single response.
	var resp jsonrpcResponse
	if err := json.Unmarshal(data, &resp); err == nil {
		dispatchResponse(resp, pending, mu, debugWriter, data)
		return nil
	}

	return fmt.Errorf("invalid JSON-RPC message: %s", string(data))
}

// injectMeta merges a _meta object into serialized JSON-RPC params.
//
// It unmarshals params into a map, inserts (or overwrites) the _meta key,
// then re-marshals. This approach:
//   - Naturally handles existing _meta keys (overwrites instead of duplicating)
//   - Avoids fragile raw-JSON string manipulation
//   - Produces valid JSON regardless of params structure
//
// The output JSON may have keys in a different order than the input
// (Go map iteration order). This is acceptable per JSON spec since
// JSON objects are unordered collections.
func injectMeta(params json.RawMessage, meta any) (json.RawMessage, error) {
	if meta == nil {
		return params, nil
	}

	metaData, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal _meta: %w", err)
	}

	// No existing params: create a new object with just _meta.
	if len(params) == 0 || string(params) == "null" {
		return json.RawMessage(`{"_meta":` + string(metaData) + `}`), nil
	}

	// Unmarshal existing params into a map. This naturally handles all
	// JSON types and will error on non-objects (arrays, scalars).
	var obj map[string]json.RawMessage
	if uerr := json.Unmarshal(params, &obj); uerr != nil {
		return nil, fmt.Errorf("params must be a JSON object for _meta injection: %w", uerr)
	}

	// Insert or overwrite _meta.
	obj["_meta"] = metaData

	result, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("serialize params with _meta: %w", err)
	}
	return result, nil
}
