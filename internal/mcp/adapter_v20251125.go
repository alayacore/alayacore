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

// AdapterV20251125 implements the MCP 2025-11-25 protocol.
//
// TRANSPORT
//
//	stdio:  ✅ fully supported
//	HTTP:   ✅ Streamable HTTP (POST with optional SSE streaming response;
//	        GET stream for server-to-client messages; MCP-Session-Id header
//	        for session management; HTTP DELETE to terminate session;
//	        SSE polling via server-side disconnect)
//
// FEATURES IMPLEMENTED
//
//	✅ initialize / initialized handshake
//	✅ capabilities in handshake request (roots: nil, sampling: nil)
//	✅ version negotiation (strict match required)
//	✅ MCP-Protocol-Version header on all HTTP requests
//	✅ MCP-Session-Id header (receive from server, echo on requests)
//	✅ GET SSE stream for server-to-client messages (ping, listChanged)
//	✅ SSE polling — GET stream supports server-side disconnect and
//	   reconnection (handled by HTTPTransport.StartGETStream)
//	✅ ping response on SSE stream
//	✅ session termination via HTTP DELETE on close
//	✅ request cancel via cancel notification
//	✅ audio content type (handled by tool_adapter.go convertToolContent)
//	✅ resource_link content type (handled by tool_adapter.go convertToolContent)
//	✅ tool annotations (handled by tool_adapter.go formatAnnotations)
//	✅ ping response on stdio (handled by StdioTransport internally)
//
// FEATURES NOT IMPLEMENTED (relative to 2025-11-25 spec)
//
//	❌ roots list — ClientCapabilities.roots is nil (intentional)
//	❌ sampling — ClientCapabilities.sampling is nil (intentional);
//	   sampling with tools/toolChoice parameters not supported
//	❌ experimental capabilities — not declared
//	❌ elicitation — ClientCapabilities.elicitation is nil; URL mode
//	   elicitation not supported (intentional)
//	❌ completions capability — not declared
//	❌ logging — server may send logging notifications; silently discarded
//	❌ tasks extension — not declared in capabilities; experimental
//	   tasks not implemented
//	❌ OpenID Connect Discovery — not implemented in auth layer
//	❌ OAuth Client ID Metadata Documents — not implemented
//	❌ JSON Schema 2020-12 as default dialect — schema handling is
//	   pass-through; validation assumes JSON Schema draft-07 compatibility
//	❌ incremental scope consent via WWW-Authenticate — not implemented
//	   in auth layer
//	❌ tool icons — exposed metadata for tools/resources/prompts not
//	   processed; icons are ignored
//	❌ progress notifications — progressToken is never set; incoming
//	   progress notifications are silently discarded
//	❌ SSE stream resumability — not implemented; on disconnect the
//	   client re-establishes a fresh stream
//
// SHARED FEATURES (not in adapter, handled at other layers)
//
//	✅ OAuth 2.1 authorization + OIDC + Client ID Metadata Docs — handled
//	   by internal/mcp/auth, same for all Streamable HTTP versions
type AdapterV20251125 struct {
	// sessionID is the Mcp-Session-Id received from the server during
	// initialization. It is sent on all subsequent requests. Empty if
	// the server did not assign a session (which is allowed by spec).
	sessionID string

	// httpClient is the HTTP client from the transport, used to send
	// session termination DELETE on close. Set by OnTransportReady.
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

// NewAdapterV20251125 creates a new adapter for the 2025-11-25 protocol.
func NewAdapterV20251125() *AdapterV20251125 {
	return &AdapterV20251125{
		getSSEClosed: make(chan struct{}),
	}
}

// ProtocolVersion returns "2025-11-25".
func (a *AdapterV20251125) ProtocolVersion() string {
	return protocolVersion20251125
}

// Handshake performs the 2025-11-25 initialize/initialized handshake.
func (a *AdapterV20251125) Handshake(ctx context.Context, c *Client) (string, error) {
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

// BuildRequestMeta returns nil — 2025-11-25 does not require structured _meta
// in every request.
func (a *AdapterV20251125) BuildRequestMeta(_ *Client) any {
	return nil
}

// ValidateResult returns nil — 2025-11-25 has no resultType field requirement.
func (a *AdapterV20251125) ValidateResult(_ string, _ json.RawMessage) error {
	return nil
}

// CancelByNotification returns true — 2025-11-25 uses a cancellation
// notification as its cancellation mechanism.
func (a *AdapterV20251125) CancelByNotification() bool { return true }

// ServerRequestHandler handles a server-to-client request on an SSE stream
// (ping in 2025-11-25). Responds to ping, rejects unknown methods.
// Uses the provided ctx (tied to transport lifetime) for the outbound HTTP POST.
func (a *AdapterV20251125) ServerRequestHandler(ctx context.Context, id requestID, method string) {
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
// headers to the outgoing HTTP request. Ignores method/params — 2025-11-25
// does not use Mcp-Method or Mcp-Name headers.
func (a *AdapterV20251125) EnrichRequest(req *http.Request, _ string, _ json.RawMessage) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())
	if a.sessionID != "" {
		req.Header.Set("MCP-Session-Id", a.sessionID)
	}
}

// SetToolHeaderMappings is a no-op — 2025-11-25 does not use Mcp-Param headers.
func (a *AdapterV20251125) SetToolHeaderMappings(_ []Tool) {}

// HandleResponseHeaders extracts MCP-Session-Id from the response headers
// and stores it for subsequent requests. Per the 2025-11-25 spec, the
// server assigns a session ID in its InitializeResult response.
func (a *AdapterV20251125) HandleResponseHeaders(resp *http.Response) {
	if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
		a.sessionID = sid
	}
}

// OnTransportReady starts the GET SSE stream for server-to-client messages.
func (a *AdapterV20251125) OnTransportReady(ctx context.Context, transport *HTTPTransport) error {
	a.getSSEMu.Lock()
	defer a.getSSEMu.Unlock()

	// Reset closed channel if previous GET stream finished.
	select {
	case <-a.getSSEClosed:
		a.getSSEClosed = make(chan struct{})
	default:
	}

	// Check transport is still alive before starting a new stream.
	select {
	case <-transport.Done():
		return io.ErrClosedPipe
	default:
	}

	getCtx, cancel := context.WithCancel(ctx)
	a.getSSECancel = cancel

	// Store transport references for session cleanup on close.
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
func (a *AdapterV20251125) OnClose(ctx context.Context) {
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
// Best-effort — errors are ignored per spec (server may return 405).
func (a *AdapterV20251125) sendSessionDelete(ctx context.Context) {
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
