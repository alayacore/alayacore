package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alayacore/alayacore/internal/version"
)

// AdapterV20260728 implements the MCP 2026-07-28 protocol (RC).
//
// This version is stateless — no initialize/initialized handshake, no session,
// no GET stream. Version identity, client info, and capabilities are carried
// in the _meta field of every request.
//
// TRANSPORT
//
//	stdio:  ✅ fully supported
//	HTTP:   ✅ Streamable HTTP (POST with optional SSE streaming response;
//	        Mcp-Method, Mcp-Name, Mcp-Param-{Name} headers on HTTP POST;
//	        no session, no GET stream, no HTTP DELETE)
//
// FEATURES IMPLEMENTED
//
//	✅ server/discover handshake (advertise supported versions)
//	✅ version negotiation via server/discover response
//	✅ _meta on every request (io.modelcontextprotocol/protocolVersion,
//	   io.modelcontextprotocol/clientInfo,
//	   io.modelcontextprotocol/clientCapabilities)
//	✅ resultType validation ("complete" accepted, "input_required" rejected)
//	✅ MCP-Protocol-Version header on all HTTP requests
//	✅ Mcp-Method header (mirrors JSON-RPC method name)
//	✅ Mcp-Name header (mirrors resource/tool/prompt name for
//	   tools/call, resources/read, prompts/get)
//	✅ Mcp-Param-{Name} headers (from x-mcp-header tool annotations)
//	✅ request cancel via cancel notification — fallback for stdio;
//	   HTTP uses transport-level cancellation
//	✅ audio content type (handled by adapter.go convertToolContent)
//	✅ resource_link content type (handled by adapter.go convertToolContent)
//	✅ tool annotations (handled by adapter.go formatAnnotations)
//	✅ ping response on stdio (handled by StdioTransport internally)
//
// FEATURES NOT IMPLEMENTED (relative to 2026-07-28-RC spec)
//
//	❌ MRTR (Multi Round-Trip Requests) — resultType "input_required" is
//	   explicitly rejected with an error; alayacore does not support
//	   multi-round-trip interactions
//	❌ subscriptions/listen — not implemented; server-to-client
//	   notifications (listChanged) are not received
//	❌ caching (ttlMs, cacheScope) — list results are not cached;
//	   ttlMs and cacheScope fields in responses are ignored
//	❌ Extensions — ClientCapabilities.extensions is nil; optional
//	   extensions beyond core protocol are not declared
//	❌ OpenTelemetry trace context (traceparent, tracestate,
//	   baggage in _meta) — not propagated
//	❌ error code renumbering — error codes returned by alayacore
//	   follow pre-2026-07-28 conventions; new error codes
//	   (-32020, -32021, -32022) are not used
//	❌ tasks extension — moved out of core protocol; not implemented
//	❌ elicitation — merged into MRTR; not implemented
//	❌ progress notifications — progressToken is never set; incoming
//	   progress notifications are silently discarded
//
// DEPRECATED FEATURES (spec-deprecated, intentionally not implemented)
//
//	⚠️ roots — deprecated in 2026-07-28 spec; not implemented
//	⚠️ sampling — deprecated in 2026-07-28 spec; not implemented
//	⚠️ logging — deprecated in 2026-07-28 spec; server notifications
//	   are silently discarded
//
// SHARED FEATURES (not in adapter, handled at other layers)
//
//	✅ OAuth 2.1 authorization — handled by internal/mcp/auth, same for
//	   all Streamable HTTP versions
type AdapterV20260728 struct {
	// toolHeaderMappings caches x-mcp-header annotations from the last
	// ListTools response, keyed by tool name. Used in EnrichRequest to
	// inject Mcp-Param-{Name} headers.
	toolHeaderMappings map[string][]HeaderMapping
}

// NewAdapterV20260728 creates a new adapter for the 2026-07-28 protocol.
func NewAdapterV20260728() *AdapterV20260728 {
	return &AdapterV20260728{
		toolHeaderMappings: make(map[string][]HeaderMapping),
	}
}

// ProtocolVersion returns "2026-07-28".
func (a *AdapterV20260728) ProtocolVersion() string {
	return protocolVersion20260728
}

// Handshake performs the 2026-07-28 server/discover handshake.
func (a *AdapterV20260728) Handshake(ctx context.Context, c *Client) (string, error) {
	result, err := c.sendRequest(ctx, methodDiscover, nil)
	if err != nil {
		return "", err
	}

	var discover DiscoverResult
	if err := json.Unmarshal(result, &discover); err != nil {
		return "", fmt.Errorf("parse server/discover result: %w", err)
	}

	c.capabilities = discover.Capabilities
	c.serverInfo = discover.ServerInfo
	c.instructions = discover.Instructions

	preferred := a.ProtocolVersion()
	for _, v := range discover.SupportedVersions {
		if v == preferred {
			return v, nil
		}
	}
	return "", fmt.Errorf("protocol version mismatch: configured %q, server supports %v",
		preferred, discover.SupportedVersions)
}

// BuildRequestMeta returns a structured RequestMetaObject for the
// 2026-07-28+ protocol. Every request must carry _meta with the
// protocol version, client identity, and capabilities.
func (a *AdapterV20260728) BuildRequestMeta(_ *Client) any {
	return &RequestMetaObject{
		ProtocolVersion: a.ProtocolVersion(),
		ClientInfo: &ImplementationInfo{
			Name:    "alayacore",
			Version: version.Version,
		},
		ClientCapabilities: &ClientCapabilities{
			Roots:       nil,
			Sampling:    nil,
			Elicitation: nil,
			Extensions:  nil,
		},
	}
}

// ValidateResult checks the resultType field required by 2026-07-28+.
// resultType must be "complete" or absent (for backward compat with
// 2025-11-25 servers). "input_required" is rejected since alayacore
// does not support multi-round-trip requests.
func (a *AdapterV20260728) ValidateResult(method string, result json.RawMessage) error {
	var rt struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(result, &rt); err != nil || rt.ResultType == "" {
		return nil // absent = "complete" (backward compat)
	}

	switch rt.ResultType {
	case "complete":
		return nil
	case "input_required":
		return fmt.Errorf("%s: server requested additional input (resultType=%q) but alayacore does not support multi-round-trip requests",
			method, rt.ResultType)
	default:
		return fmt.Errorf("%s: unrecognized resultType %q", method, rt.ResultType)
	}
}

// SetToolHeaderMappings caches tool header mappings for Mcp-Param-{Name}
// injection. Called by Client.ListTools after fetching tool definitions.
func (a *AdapterV20260728) SetToolHeaderMappings(tools []Tool) {
	a.toolHeaderMappings = make(map[string][]HeaderMapping, len(tools))
	for i := range tools {
		if len(tools[i].HeaderMappings) > 0 {
			m := make([]HeaderMapping, len(tools[i].HeaderMappings))
			copy(m, tools[i].HeaderMappings)
			a.toolHeaderMappings[tools[i].Name] = m
		}
	}
}

// EnrichRequest adds version-specific HTTP headers required by 2026-07-28:
//   - MCP-Protocol-Version (required on all requests)
//   - Mcp-Method (required on all POST requests)
//   - Mcp-Name (required for tools/call, resources/read, prompts/get)
//   - Mcp-Param-{Name} (required when tool has x-mcp-header annotations)
func (a *AdapterV20260728) EnrichRequest(req *http.Request, method string, params json.RawMessage) {
	req.Header.Set("MCP-Protocol-Version", a.ProtocolVersion())

	if method == "" {
		// GET request (2025-11-25 GET stream adapter compatibility).
		return
	}

	// Mcp-Method: mirror the JSON-RPC method.
	req.Header.Set("Mcp-Method", method)

	// Mcp-Name: mirror the resource/tool/prompt name for specific methods.
	// Values that cannot be safely represented as a plain ASCII header
	// value MUST use the Base64 sentinel format (=?base64?...?=).
	if name := extractMcpName(method, params); name != "" {
		encoded, _ := encodeHeaderValue(name, schemaTypeString)
		req.Header.Set("Mcp-Name", encoded)
	}

	// Mcp-Param-{Name}: mirror x-mcp-header annotated parameters.
	if method == methodCallTool && len(a.toolHeaderMappings) > 0 {
		a.injectParamHeaders(req, params)
	}
}

// injectParamHeaders reads the tool name and arguments from a tools/call
// params body and injects Mcp-Param-{Name} headers for any annotated
// parameters that have values in the arguments.
func (a *AdapterV20260728) injectParamHeaders(req *http.Request, params json.RawMessage) {
	var callReq struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &callReq); err != nil || callReq.Name == "" {
		return
	}

	mappings, ok := a.toolHeaderMappings[callReq.Name]
	if !ok || len(mappings) == 0 {
		return
	}

	headers := buildToolHeadersFromMappings(mappings, callReq.Arguments)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// extractMcpName extracts the resource or tool name from JSON-RPC params
// for the Mcp-Name header. Only applies to tools/call, resources/read,
// and prompts/get methods.
func extractMcpName(method string, params json.RawMessage) string {
	switch method {
	case methodCallTool, methodReadResource, methodGetPrompt:
	default:
		return ""
	}

	var fields struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if err := json.Unmarshal(params, &fields); err != nil {
		return ""
	}
	if fields.Name != "" {
		return fields.Name
	}
	return fields.URI
}

// buildToolHeadersFromMappings constructs Mcp-Param-{Name} headers from
// tool header mappings and call arguments.
func buildToolHeadersFromMappings(mappings []HeaderMapping, args json.RawMessage) map[string]string {
	if len(args) == 0 || len(mappings) == 0 {
		return nil
	}

	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return nil
	}

	headers := make(map[string]string, len(mappings))
	for _, m := range mappings {
		value, found := resolveNestedValue(argsMap, m.ParamPath)
		if !found || value == nil {
			continue
		}
		encoded, _ := encodeHeaderValue(value, m.ParamType)
		if encoded == "" {
			continue
		}
		headers["Mcp-Param-"+m.HeaderName] = encoded
	}

	if len(headers) == 0 {
		return nil
	}
	return headers
}

// HandleResponseHeaders is a no-op — 2026-07-28 has no session concept.
func (a *AdapterV20260728) HandleResponseHeaders(_ *http.Response) {}

// CancelByNotification returns false — 2026-07-28 uses transport-level
// cancellation (closing SSE stream on HTTP) instead of JSON-RPC
// notification. On stdio, the caller falls back to sending the
// notification because there is no per-request stream to close.
func (a *AdapterV20260728) CancelByNotification() bool { return false }

// ServerRequestHandler is a no-op — 2026-07-28 has no server-to-client
// requests on SSE streams (MRTR replaces them).
func (a *AdapterV20260728) ServerRequestHandler(_ context.Context, _ requestID, _ string) {}

// OnTransportReady is a no-op — 2026-07-28 has no GET stream.
func (a *AdapterV20260728) OnTransportReady(_ context.Context, _ *HTTPTransport) error {
	return nil
}

// OnClose is a no-op — 2026-07-28 has no session to terminate.
func (a *AdapterV20260728) OnClose(_ context.Context) {}
