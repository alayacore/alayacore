package mcp

import (
	"context"
	"encoding/json"
	"net/http"
)

// Adapter is the common interface that all protocol version adapters
// must implement. These methods are used regardless of transport type.
type Adapter interface {
	// ProtocolVersion returns the protocol version string
	// (e.g. "2025-11-25" or "2026-07-28").
	ProtocolVersion() string

	// Handshake performs the initial handshake with the server.
	Handshake(ctx context.Context, c *Client) (string, error)

	// BuildRequestMeta constructs the _meta field for a JSON-RPC request.
	// Returns nil if no _meta should be injected.
	BuildRequestMeta(c *Client) any

	// ValidateResult checks a JSON-RPC result for protocol-version-specific
	// correctness. Returns nil if OK, error if the result is invalid or
	// requires capabilities the client does not have.
	ValidateResult(method string, result json.RawMessage) error

	// CancelByNotification returns true if this protocol version uses a
	// cancellation notification as the cancellation mechanism.
	// When false, transport-level cancellation is used instead
	// (e.g. closing the SSE response stream for HTTP 2026-07-28+).
	CancelByNotification() bool

	// OnClose is called when the client is shutting down.
	// The ctx carries a short timeout for best-effort cleanup
	// (e.g. sending session DELETE). Implementations should respect
	// ctx cancellation and not block beyond the timeout.
	OnClose(ctx context.Context)
}

// HTTPAdapter extends Adapter with HTTP transport-specific hooks.
// These are only called for HTTP transport; stdio transport does not
// use HTTP headers or GET streams.
type HTTPAdapter interface {
	Adapter

	// EnrichRequest modifies the outgoing HTTP request before it is sent.
	// method and params carry the JSON-RPC method and params for this POST
	// (empty for GET requests). The adapter can inject version-specific
	// headers (e.g. MCP-Protocol-Version, MCP-Session-Id, Mcp-Method,
	// Mcp-Name, Mcp-Param-{Name}).
	EnrichRequest(req *http.Request, method string, params json.RawMessage)

	// HandleResponseHeaders processes HTTP response headers after each
	// response is received (e.g. extract MCP-Session-Id).
	HandleResponseHeaders(resp *http.Response)

	// OnTransportReady is called after the handshake completes and the
	// transport is ready. The adapter can start version-specific
	// resources (e.g. GET SSE stream for 2025-11-25).
	OnTransportReady(ctx context.Context, transport *HTTPTransport) error

	// ServerRequestHandler handles a JSON-RPC request from the server on
	// an SSE stream. 2025-11-25 responds to ping; 2026-07-28 has no
	// server-to-client requests and is a no-op.
	// The ctx is tied to the transport's lifetime — it is canceled when
	// the transport is closed, so implementations should use it for
	// outbound requests (e.g. responding to ping). A non-nil error is the
	// reply's failure, recorded by the transport that called it.
	ServerRequestHandler(ctx context.Context, id requestID, method string) error

	// SetToolHeaderMappings feeds the adapter tool header mappings from the
	// last ListTools response, used for Mcp-Param-{Name} header injection
	// in EnrichRequest for tools/call requests.
	SetToolHeaderMappings(tools []Tool)
}

// compile-time checks.
var (
	_ Adapter = (*AdapterV20241105)(nil)
	_ Adapter = (*AdapterV20250326)(nil)
	_ Adapter = (*AdapterV20250618)(nil)
	_ Adapter = (*AdapterV20251125)(nil)
	_ Adapter = (*AdapterV20260728)(nil)
)
