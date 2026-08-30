package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// ============================================================================
// HTTP Transport
// ============================================================================
//
// HTTPTransport implements the MCP Streamable HTTP transport in a
// protocol-version-agnostic way. It handles:
//   - HTTP POST with JSON-RPC messages
//   - Response parsing (JSON or SSE stream)
//   - OAuth token injection
//   - Debug logging
//
// Protocol-version-specific behavior (session management, GET stream,
// header injection) is handled by the HTTPAdapter attached to this
// transport. The adapter's hooks are called before each request and after
// each response.

// HTTPTransport communicates with an MCP server via HTTP POST.
// It is protocol-version agnostic — version-specific behavior is
// delegated to the attached HTTPAdapter.
type HTTPTransport struct {
	endpointURL string
	httpClient  *http.Client

	// adapter provides version-specific hooks (session ID, headers, etc.).
	// Set after handshake negotiation. May be nil initially.
	adapter HTTPAdapter

	// authProvider provides OAuth tokens for Authorization header injection.
	authProvider auth.TokenProvider

	// SSE stream from a POST response that's still active.
	postSSEStream *sseReadCloser
	postSSEMu     sync.Mutex

	pending   map[requestID]chan<- jsonrpcResponse
	pendingMu sync.Mutex

	closeOnce sync.Once
	done      chan struct{}

	debugWriter io.WriteCloser

	// Notification handler for server-to-client notifications.
	notificationHandler NotificationHandler
}

// sseReadCloser wraps an io.ReadCloser (HTTP response body) with the
// scanner and SSE parsing state needed to read SSE events from it.
type sseReadCloser struct {
	body     io.ReadCloser
	scanner  *bufio.Scanner
	closed   bool
	closedMu sync.Mutex
}

// newSSEReadCloser creates an sseReadCloser from an HTTP response body.
func newSSEReadCloser(body io.ReadCloser) *sseReadCloser {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseReadCloser{
		body:    body,
		scanner: scanner,
	}
}

// readEvent reads the next complete SSE event (a blank-line-delimited
// sequence of field lines). Returns the event type, data, and any error.
// If the stream is closed cleanly, returns io.EOF.
func (s *sseReadCloser) readEvent() (eventType, data string, err error) {
	s.closedMu.Lock()
	if s.closed {
		s.closedMu.Unlock()
		return "", "", io.EOF
	}
	s.closedMu.Unlock()

	var currentEvent string
	var currentData strings.Builder

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Empty line signals end of an SSE event.
		if line == "" {
			if currentEvent != "" || currentData.Len() > 0 {
				return currentEvent, currentData.String(), nil
			}
			continue
		}

		processSSELine(line, &currentEvent, &currentData)
	}

	if err := s.scanner.Err(); err != nil {
		return "", "", fmt.Errorf("SSE read: %w", err)
	}
	return "", "", io.EOF
}

// Close closes the underlying body.
func (s *sseReadCloser) Close() error {
	s.closedMu.Lock()
	s.closed = true
	s.closedMu.Unlock()
	return s.body.Close()
}

// NewHTTPTransport creates a new HTTP transport.
// debugDir "" = no debug logging; "." = write to CWD.
// It does NOT connect immediately; the first Send/SendReceive will POST
// to the endpoint.
func NewHTTPTransport(endpointURL string, debugDir string) (*HTTPTransport, error) {
	t := &HTTPTransport{
		endpointURL: endpointURL,
		httpClient: &http.Client{
			Timeout: 0,
		},
		pending: make(map[requestID]chan<- jsonrpcResponse),
		done:    make(chan struct{}),
	}

	if debugDir != "" {
		// Never fall back to os.Stderr (see debug.NewDebugWriter): this
		// transport closes the writer, which would kill the process's
		// stderr. A debug log that cannot be opened is reported instead.
		dw, err := debug.NewDebugWriter(debugDir, "alayacore-debug-mcp")
		if err != nil {
			return nil, err
		}
		t.debugWriter = dw
		fmt.Fprintf(dw, "MCP HTTP debug log started for: %s\n", endpointURL)
	}

	return t, nil
}

// SetHTTPAdapter attaches the HTTP adapter for version-specific request/response
// hooks (header injection, session ID extraction). Must be called before or
// during handshake, before any regular requests.
func (t *HTTPTransport) SetHTTPAdapter(a HTTPAdapter) {
	t.adapter = a
}

// ============================================================================
// Transport Interface
// ============================================================================

// Send sends a JSON-RPC notification (no response expected) via HTTP POST.
func (t *HTTPTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	resp, err := t.doPOST(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("POST notification: unexpected status %d (expected 202)", resp.StatusCode)
	}

	return nil
}

// SendReceive sends a JSON-RPC request and waits for the matching response.
// The server may respond with either:
//   - Content-Type: application/json (immediate JSON response)
//   - Content-Type: text/event-stream (SSE stream containing the response)
func (t *HTTPTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	resp, err := t.doPOST(ctx, req)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	contentType = strings.SplitN(contentType, ";", 2)[0]
	contentType = strings.TrimSpace(contentType)

	switch contentType {
	case "application/json":
		defer resp.Body.Close()
		return t.readJSONResponse(resp.Body)

	case "text/event-stream":
		return t.readSSEResponse(ctx, resp, req.ID)

	case "text/plain":
		defer resp.Body.Close()
		return t.readTextResponse(resp.Body)

	default:
		resp.Body.Close()
		return nil, fmt.Errorf("POST: unexpected Content-Type %q", contentType)
	}
}

// Close shuts down the transport.
func (t *HTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		// Close POST SSE stream if active.
		t.postSSEMu.Lock()
		if t.postSSEStream != nil {
			t.postSSEStream.Close()
		}
		t.postSSEMu.Unlock()

		if t.debugWriter != nil {
			t.debugWriter.Close()
		}

		close(t.done)
	})
	return nil
}

// Done returns a channel that closes when the transport is shut down.
func (t *HTTPTransport) Done() <-chan struct{} {
	return t.done
}

// SetNotificationHandler registers a handler for server-to-client notifications.
func (t *HTTPTransport) SetNotificationHandler(h NotificationHandler) {
	t.notificationHandler = h
}

// SetAuthProvider sets the OAuth token provider for this transport.
func (t *HTTPTransport) SetAuthProvider(ap auth.TokenProvider) {
	t.authProvider = ap
}

// DebugWriter returns the debug log writer, or nil if debug is not enabled.
func (t *HTTPTransport) DebugWriter() io.Writer {
	return t.debugWriter
}

// ============================================================================
// GET SSE Stream (Server-to-Client Messages)
// ============================================================================
//
// Only used in protocol version 2025-11-25. The adapter manages this
// stream via the StartGETStream / StopGETStream methods.

// StartGETStream starts a long-lived GET SSE connection for receiving
// server-to-client messages. Only valid for 2025-11-25 protocol.
// Returns a close function that terminates the stream.
func (t *HTTPTransport) StartGETStream(ctx context.Context) (func(), error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", t.endpointURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET request: %w", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	if t.adapter != nil {
		t.adapter.EnrichRequest(httpReq, "", nil)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("GET SSE: %w", err)
	}

	if resp.StatusCode == http.StatusMethodNotAllowed {
		resp.Body.Close()
		return nil, fmt.Errorf("GET SSE: server returned 405 Method Not Allowed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET SSE: unexpected status %d", resp.StatusCode)
	}

	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if contentType != "text/event-stream" {
		resp.Body.Close()
		return nil, fmt.Errorf("GET SSE: expected text/event-stream, got %q", contentType)
	}

	sr := newSSEReadCloser(resp.Body)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		defer sr.Close()

		// Context tied to transport lifetime: canceled when Close() is called.
		loopCtx, loopCancel := context.WithCancel(context.Background())
		defer loopCancel()
		go func() { <-t.done; loopCancel() }()

		var srh ServerRequestHandler
		if t.adapter != nil {
			srh = t.adapter.ServerRequestHandler
		}
		t.readSSELoop(loopCtx, sr, srh, t.notificationHandler)
	}()

	return func() { sr.Close(); <-closed }, nil
}

// readSSELoop reads SSE events from a stream and dispatches them.
func (t *HTTPTransport) readSSELoop(ctx context.Context, sr *sseReadCloser, handler ServerRequestHandler, notifHandler NotificationHandler) {
	for {
		eventType, data, err := sr.readEvent()
		if err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				if t.debugWriter != nil {
					fmt.Fprintf(t.debugWriter, "SSE read error: %v\n", err)
				}
			}
			return
		}

		switch eventType {
		case "message":
			if err := parseAndDispatchJSONRPC(ctx, []byte(data), t.pending, &t.pendingMu, t.debugWriter, handler, notifHandler); err != nil {
				if t.debugWriter != nil {
					fmt.Fprintf(t.debugWriter, "MCP HTTP: malformed message: %v\n", err)
				}
			}
		default:
			// Unknown event type — ignore per spec.
		}
	}
}

// ============================================================================
// HTTP Request Helpers
// ============================================================================

// doPOST sends an HTTP POST with the JSON-RPC message.
func (t *HTTPTransport) doPOST(ctx context.Context, req jsonrpcRequest) (*http.Response, error) {
	resp, err := t.doPOSTOnce(ctx, req)
	if err != nil {
		return nil, err
	}

	// If 401 and we have an auth provider, clear cached token and retry once.
	if resp.StatusCode == http.StatusUnauthorized && t.authProvider != nil {
		resp.Body.Close()
		resp, err = t.doPOSTOnce(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// doPOSTOnce is the inner POST implementation without 401 retry.
func (t *HTTPTransport) doPOSTOnce(ctx context.Context, req jsonrpcRequest) (*http.Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> %s %s\n", req.Method, formatJSON(data))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.endpointURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	// Let the adapter add version-specific headers (e.g. MCP-Protocol-Version,
	// MCP-Session-Id for 2025-11-25, Mcp-Method/Mcp-Name for 2026-07-28).
	if t.adapter != nil {
		t.adapter.EnrichRequest(httpReq, req.Method, req.Params)
	}

	// Inject auth token if available.
	if t.authProvider != nil {
		tok, tokErr := t.authProvider.Token(ctx)
		if tokErr == nil && tok != nil {
			httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		}
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	// Let the adapter process response headers (e.g. extract session ID).
	if t.adapter != nil {
		t.adapter.HandleResponseHeaders(resp)
	}

	return resp, nil
}

// readJSONResponse reads and parses a JSON-RPC response body.
func (t *HTTPTransport) readJSONResponse(body io.ReadCloser) (json.RawMessage, error) {
	var resp jsonrpcResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode JSON response: %w", err)
	}

	if resp.Error != nil {
		return nil, &RPCError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}
	}
	return resp.Result, nil
}

// readTextResponse reads a text/plain response body.
//
//nolint:unparam // signature matches readJSONResponse for switch consistency
func (t *HTTPTransport) readTextResponse(body io.ReadCloser) (json.RawMessage, error) {
	// Capped: the body becomes an error message shown to the user and fed to
	// the model, so a server cannot decide its size.
	data, err := io.ReadAll(io.LimitReader(body, maxTextResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read text response: %w", err)
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		msg = "empty text/plain response"
	}
	return nil, &RPCError{Code: -32000, Message: msg}
}

// readSSEResponse reads an SSE stream from a POST response.
func (t *HTTPTransport) readSSEResponse(ctx context.Context, resp *http.Response, reqID requestID) (json.RawMessage, error) {
	sr := newSSEReadCloser(resp.Body)

	t.postSSEMu.Lock()
	t.postSSEStream = sr
	t.postSSEMu.Unlock()

	defer func() {
		t.postSSEMu.Lock()
		if t.postSSEStream == sr {
			t.postSSEStream = nil
		}
		t.postSSEMu.Unlock()
		sr.Close()
	}()

	respCh := make(chan jsonrpcResponse, 1)

	t.pendingMu.Lock()
	select {
	case <-t.done:
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}
	t.pending[reqID] = respCh
	t.pendingMu.Unlock()

	var success bool
	defer func() {
		if !success {
			t.pendingMu.Lock()
			delete(t.pending, reqID)
			t.pendingMu.Unlock()
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)

		// Context tied to transport lifetime: canceled when Close() is called.
		loopCtx, loopCancel := context.WithCancel(context.Background())
		defer loopCancel()
		go func() { <-t.done; loopCancel() }()

		var srh ServerRequestHandler
		if t.adapter != nil {
			srh = t.adapter.ServerRequestHandler
		}
		t.readSSELoop(loopCtx, sr, srh, t.notificationHandler)
	}()

	select {
	case respData, ok := <-respCh:
		if !ok {
			return nil, io.EOF
		}
		if respData.Error != nil {
			return nil, &RPCError{
				Code:    respData.Error.Code,
				Message: respData.Error.Message,
				Data:    respData.Error.Data,
			}
		}
		success = true
		return respData.Result, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-t.done:
		return nil, io.EOF

	case <-readDone:
		t.pendingMu.Lock()
		delete(t.pending, reqID)
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("SSE stream ended before response for %q was received", reqID)
	}
}

// processSSELine parses a single SSE field line.
func processSSELine(line string, currentEvent *string, currentData *strings.Builder) {
	switch {
	case strings.HasPrefix(line, "event:"):
		if len(line) > 6 && line[6] == ' ' {
			*currentEvent = line[7:]
		} else {
			*currentEvent = line[6:]
		}

	case strings.HasPrefix(line, "data:"):
		if currentData.Len() > 0 {
			currentData.WriteString("\n")
		}
		if len(line) > 5 && line[5] == ' ' {
			currentData.WriteString(line[6:])
		} else {
			currentData.WriteString(line[5:])
		}

	case len(line) > 0 && line[0] == ':':
		// Comment — ignore.

	default:
		// Unknown field — ignore per SSE spec.
	}
}
