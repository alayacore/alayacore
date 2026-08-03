//nolint:dupl // intentional: each version is a self-contained adapter
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alayacore/alayacore/internal/version"
)

// AdapterV20241105 implements the MCP 2024-11-05 protocol.
//
// TRANSPORT
//
//	stdio:  ✅ fully supported
//	HTTP:   ❌ not supported (2024-11-05 uses legacy HTTP+SSE pattern:
//	        client opens SSE connection first, server sends endpoint event,
//	        client POSTs to discovered URL, responses come on SSE stream.
//	        The current transport layer only implements Streamable HTTP.)
//
// FEATURES IMPLEMENTED
//
//	✅ initialize / initialized handshake
//	✅ capabilities in handshake request (roots: nil, sampling: nil)
//	✅ version negotiation (strict match required)
//	✅ request cancel via cancel notification
//	✅ ping response on stdio (handled by StdioTransport internally)
//
// FEATURES NOT IMPLEMENTED (relative to 2024-11-05 spec)
//
//	❌ roots list — ClientCapabilities.roots is nil, server should not
//	   send roots/list requests (this is intentional for alayacore's use case)
//	❌ sampling — ClientCapabilities.sampling is nil, server should not
//	   send sampling/createMessage requests (intentional)
//	❌ experimental capabilities — not declared
//	❌ logging — server may send logging notifications; they are silently
//	   discarded (no NotificationHandler registered for log messages)
//	❌ HTTP+SSE transport — see TRANSPORT above
//	❌ SSE stream resumability / Last-Event-ID — not applicable (no HTTP)
//	❌ progress notifications — clients may request them via _meta.progressToken,
//	   but alayacore never sets progressToken and ignores incoming progress
//	   notifications
//
// SHARED FEATURES (not in adapter, handled at other layers)
//
//	✅ OAuth 2.1 authorization — handled by internal/mcp/auth
//	   (2024-11-05 predates the OAuth spec, but auth is transport-level
//	   and works identically across all HTTP versions)
type AdapterV20241105 struct{}

// NewAdapterV20241105 creates a new adapter for the 2024-11-05 protocol.
func NewAdapterV20241105() *AdapterV20241105 {
	return &AdapterV20241105{}
}

// ProtocolVersion returns "2024-11-05".
func (a *AdapterV20241105) ProtocolVersion() string {
	return protocolVersion20241105
}

// Handshake performs the 2024-11-05 initialize/initialized handshake.
func (a *AdapterV20241105) Handshake(ctx context.Context, c *Client) (string, error) {
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

// BuildRequestMeta returns nil — 2024-11-05 does not require structured _meta
// in every request.
func (a *AdapterV20241105) BuildRequestMeta(_ *Client) any {
	return nil
}

// ValidateResult returns nil — 2024-11-05 has no resultType field requirement.
func (a *AdapterV20241105) ValidateResult(_ string, _ json.RawMessage) error {
	return nil
}

// CancelByNotification returns true — 2024-11-05 uses a cancellation
// notification as its cancellation mechanism.
func (a *AdapterV20241105) CancelByNotification() bool { return true }

// ServerRequestHandler is a no-op — 2024-11-05 does not support HTTP transport
// in this implementation, so this callback is never invoked.
func (a *AdapterV20241105) ServerRequestHandler(_ context.Context, _ requestID, _ string) {}

// EnrichRequest is a no-op — 2024-11-05 does not require any HTTP headers
// (no MCP-Protocol-Version, no MCP-Session-Id, no Mcp-* headers).
func (a *AdapterV20241105) EnrichRequest(_ *http.Request, _ string, _ json.RawMessage) {}

// SetToolHeaderMappings is a no-op — 2024-11-05 does not use Mcp-Param headers.
func (a *AdapterV20241105) SetToolHeaderMappings(_ []Tool) {}

// HandleResponseHeaders is a no-op — 2024-11-05 HTTP+SSE does not use
// response headers for session management.
func (a *AdapterV20241105) HandleResponseHeaders(_ *http.Response) {}

// OnTransportReady is a no-op — 2024-11-05 is rejected for HTTP transport
// in this implementation, so this method is never called.
func (a *AdapterV20241105) OnTransportReady(_ context.Context, _ *HTTPTransport) error {
	return nil
}

// OnClose is a no-op — 2024-11-05 has no session or GET stream to clean up
// when used over stdio transport.
func (a *AdapterV20241105) OnClose(_ context.Context) {}
