//nolint:dupl // intentional: each version is a self-contained adapter
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/version"
)

// AdapterV20250326 implements the MCP 2025-03-26 protocol.
//
// This version introduced Streamable HTTP (replacing the legacy HTTP+SSE),
// OAuth 2.1 authorization, tool annotations, audio content type,
// completions capability, and JSON-RPC batch support.
//
// TRANSPORT
//
//	stdio:  ✅ fully supported
//	HTTP:   ✅ Streamable HTTP (POST with optional SSE streaming response;
//	        GET stream for server-to-client messages; MCP-Session-Id header
//	        for session management; HTTP DELETE to terminate session)
//
// FEATURES IMPLEMENTED
//
//	✅ initialize / initialized handshake
//	✅ capabilities in handshake request (roots: nil, sampling: nil)
//	✅ version negotiation (strict match required)
//	✅ MCP-Protocol-Version header on all HTTP requests
//	✅ MCP-Session-Id header (receive from server, echo on requests)
//	✅ GET SSE stream for server-to-client messages (ping, listChanged)
//	✅ ping response on SSE stream
//	✅ session termination via HTTP DELETE on close
//	✅ request cancel via cancel notification
//	✅ audio content type (handled by adapter.go convertToolContent)
//	✅ resource_link content type (handled by adapter.go convertToolContent)
//	✅ tool annotations (handled by adapter.go formatAnnotations)
//	✅ ping response on stdio (handled by StdioTransport internally)
//
// FEATURES NOT IMPLEMENTED (relative to 2025-03-26 spec)
//
//	❌ JSON-RPC batch requests — not supported by the transport layer;
//	   each JSON-RPC message is sent as a separate HTTP POST
//	❌ roots list — ClientCapabilities.roots is nil (intentional)
//	❌ sampling — ClientCapabilities.sampling is nil (intentional)
//	❌ experimental capabilities — not declared
//	❌ completions capability — not declared; server should not send
//	   completion/complete requests
//	❌ logging — server may send logging notifications; silently discarded
//	❌ SSE stream resumability / Last-Event-ID — GET stream is not
//	   resumed on disconnection; client re-establishes on reconnect
//	❌ progress notifications — progressToken is never set; incoming
//	   progress notifications are silently discarded
//
// SHARED FEATURES (not in adapter, handled at other layers)
//
//	✅ OAuth 2.1 authorization — handled by internal/mcp/auth, same for
//	   all Streamable HTTP versions (2025-03-26 and later)
type AdapterV20250326 struct {
	// sessionID is the Mcp-Session-Id received from the server during
	// initialization. It is sent on all subsequent requests.
	sessionID string

	// httpClient and endpointURL are used to send session termination
	// DELETE and to respond to server-to-client requests (ping).
	httpClient  *http.Client
	endpointURL string

	// getSSECancel cancels the GET SSE stream context.
	getSSECancel context.CancelFunc
	// getSSEClose terminates the GET SSE stream by closing its response body.
	getSSEClose func()
	// getSSEClosed is closed when the GET SSE goroutine exits.
	getSSEClosed chan struct{}
	getSSEMu     sync.Mutex
}

// NewAdapterV20250326 creates a new adapter for the 2025-03-26 protocol.
func NewAdapterV20250326() *AdapterV20250326 {
	return &AdapterV20250326{
		getSSEClosed: make(chan struct{}),
	}
}

// ProtocolVersion returns "2025-03-26".
func (a *AdapterV20250326) ProtocolVersion() string {
	return protocolVersion20250326
}

// Handshake performs the 2025-03-26 initialize/initialized handshake.
func (a *AdapterV20250326) Handshake(ctx context.Context, c *Client) (string, error) {
	initResult, err := c.sendRequest(ctx, methodInitialize, InitializeRequest{
		ProtocolVersion: a.ProtocolVersion(),
		Capabilities: ClientCapabilities{
			Roots:    nil,
			Sampling: nil,
		},
		ClientInfo: ImplementationInfo{
			Name:    "alayacore",
			Version: version.Version,
		},
	})
	if err != nil {
		return "", err
	}

	var result InitializeResult
	if err := json.Unmarshal(initResult, &result); err != nil {
		return "", fmt.Errorf("parse initialize result: %w", err)
	}

	if result.ProtocolVersion != a.ProtocolVersion() {
		return "", fmt.Errorf("protocol version mismatch: server returned %q, configured %q",
			result.ProtocolVersion, a.ProtocolVersion())
	}

	c.capabilities = result.Capabilities
	c.serverInfo = result.ServerInfo
	c.instructions = result.Instructions

	_ = c.sendNotification(ctx, methodNotificationsInitialized, nil)

	return result.ProtocolVersion, nil
}

// BuildRequestMeta returns nil — 2025-03-26 does not require structured _meta.
func (a *AdapterV20250326) BuildRequestMeta(_ *Client) any {
	return nil
}

// ValidateResult returns nil — 2025-03-26 has no resultType field requirement.
func (a *AdapterV20250326) ValidateResult(_ string, _ json.RawMessage) error {
	return nil
}

// CancelByNotification returns true — 2025-03-26 uses cancellation notification.
func (a *AdapterV20250326) CancelByNotification() bool { return true }

// ServerRequestHandler handles a server-to-client request on an SSE stream
// (ping in 2025-03-26). Responds to ping, rejects unknown methods.
// Uses the provided ctx (tied to transport lifetime) for the outbound HTTP POST.
func (a *AdapterV20250326) ServerRequestHandler(ctx context.Context, id requestID, method string) {
	if a.httpClient == nil || a.endpointURL == "" {
		return
	}

	// Cap to 10s but respect transport shutdown via ctx.
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp := jsonrpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &jsonrpcError{Code: -32601, Message: "Method not found: " + method},
	}
	if method == methodPing {
		resp.Error = nil
		resp.Result = json.RawMessage(`{}`)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", a.endpointURL, strings.NewReader(string(data)))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
	if a.sessionID != "" {
		httpReq.Header.Set("MCP-Session-Id", a.sessionID)
	}

	if r, err := a.httpClient.Do(httpReq); err == nil {
		r.Body.Close()
	}
}

// EnrichRequest adds MCP-Protocol-Version and (if assigned) MCP-Session-Id
// headers to the outgoing HTTP request.
func (a *AdapterV20250326) EnrichRequest(req *http.Request, _ string, _ json.RawMessage) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
	if a.sessionID != "" {
		req.Header.Set("MCP-Session-Id", a.sessionID)
	}
}

// SetToolHeaderMappings is a no-op — 2025-03-26 does not use Mcp-Param headers.
func (a *AdapterV20250326) SetToolHeaderMappings(_ []Tool) {}

// HandleResponseHeaders extracts MCP-Session-Id from the response headers.
func (a *AdapterV20250326) HandleResponseHeaders(resp *http.Response) {
	if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
		a.sessionID = sid
	}
}

// OnTransportReady starts the GET SSE stream for server-to-client messages.
func (a *AdapterV20250326) OnTransportReady(ctx context.Context, transport *HTTPTransport) error {
	a.getSSEMu.Lock()
	defer a.getSSEMu.Unlock()

	select {
	case <-a.getSSEClosed:
		a.getSSEClosed = make(chan struct{})
	default:
	}

	select {
	case <-transport.Done():
		return io.ErrClosedPipe
	default:
	}

	getCtx, cancel := context.WithCancel(ctx)
	a.getSSECancel = cancel

	a.httpClient = transport.httpClient
	a.endpointURL = transport.endpointURL

	closeFn, err := transport.StartGETStream(getCtx)
	if err != nil {
		cancel()
		a.getSSECancel = nil
		return err
	}
	a.getSSEClose = closeFn

	return nil
}

// OnClose terminates the session and cleans up the GET stream.
func (a *AdapterV20250326) OnClose(ctx context.Context) {
	a.getSSEMu.Lock()
	defer a.getSSEMu.Unlock()

	if a.getSSECancel != nil {
		a.getSSECancel()
		a.getSSECancel = nil
	}
	if a.getSSEClose != nil {
		a.getSSEClose()
		a.getSSEClose = nil
	}
	close(a.getSSEClosed)

	a.sendSessionDelete(ctx)
}

// sendSessionDelete sends an HTTP DELETE to terminate the session.
func (a *AdapterV20250326) sendSessionDelete(ctx context.Context) {
	if a.sessionID == "" || a.httpClient == nil {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, a.endpointURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
	req.Header.Set("MCP-Session-Id", a.sessionID)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
