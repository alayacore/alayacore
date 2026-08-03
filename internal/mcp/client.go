package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// ClientState represents the state of an MCP client connection.
type ClientState int

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateInitializing
	StateReady
	StateFailed
	StateStale // server tool list changed, needs restart
)

// Client manages a connection to a single MCP server.
//
// CONCURRENCY MODEL:
//   - transport: atomic.Value — safe for concurrent reads (sendRequest) without mutex
//   - closeDone: atomic.Bool — Close() and monitor atomically claim the right
//     to close closedCh via Swap(true). Only one succeeds, no mutex needed.
//   - state: atomic.Int32 — CAS for safe state transitions
//
// A dedicated monitor goroutine watches transport.Done(). If the transport
// dies unexpectedly (process crash, connection drop), it transitions the
// client to StateFailed and signals it via closedCh.
//
// There is no Reconnect — if a server dies, the client stays failed.
// The caller should create a new Client if it needs to reconnect.
//
// The closed channel (closedCh) is NOT replaced after creation — it is
// closed once, either by Close() or by the monitor on unexpected death.
type Client struct {
	config     ServerConfig
	tokenStore auth.TokenStore
	transport  atomic.Value // stores Transport or nil
	state      atomic.Int32 // stores ClientState as int32

	// adapter handles protocol-version-specific behavior
	// (handshake, _meta injection, and HTTP transport hooks).
	adapter Adapter

	// Server capabilities reported during initialization.
	capabilities ServerCapabilities
	serverInfo   ImplementationInfo
	instructions string

	// staleReason is set when the server is marked stale (e.g. tool list changed).
	staleReason string

	// toolsCache caches tool definitions (with parsed HeaderMappings) keyed
	// by tool name. Used by CallTool to inject x-mcp-header HTTP headers.
	toolsCache map[string]*Tool

	// Request ID counter.
	reqID atomic.Int32

	// closeDone is set to true when the client is shut down.
	closeDone atomic.Bool
	closedCh  chan struct{}
}

// NewClient creates a new MCP client. Call Connect() to establish the connection.
func NewClient(config ServerConfig) *Client {
	return &Client{
		config:     config,
		tokenStore: config.TokenStore,
		closedCh:   make(chan struct{}),
		toolsCache: make(map[string]*Tool),
	}
}

// Name returns the human-readable name of this server.
func (c *Client) Name() string {
	return c.config.Name
}

// State returns the current connection state.
func (c *Client) State() ClientState {
	return ClientState(c.state.Load())
}

// ServerInfo returns the server's implementation info from initialization.
func (c *Client) ServerInfo() ImplementationInfo {
	return c.serverInfo
}

// Instructions returns the server's instructions from initialization, if any.
func (c *Client) Instructions() string {
	return c.instructions
}

// MarkStale marks the server as stale, indicating its tool list has changed.
func (c *Client) MarkStale(reason string) {
	c.state.Store(int32(StateStale))
	c.staleReason = reason
}

// Connect establishes the transport and performs MCP initialization.
//
//nolint:gocyclo
func (c *Client) Connect(ctx context.Context) error {
	if !c.state.CompareAndSwap(int32(StateDisconnected), int32(StateConnecting)) {
		return fmt.Errorf("already connecting")
	}
	defer func() {
		c.state.CompareAndSwap(int32(StateConnecting), int32(StateFailed))
		c.state.CompareAndSwap(int32(StateInitializing), int32(StateFailed))
	}()

	// proto-version is required — no default.
	switch c.config.ProtoVersion {
	case protocolVersion20241105:
		c.adapter = NewAdapterV20241105()
		if c.config.URL != "" {
			return fmt.Errorf("proto-version 2024-11-05 only supports stdio transport")
		}
	case protocolVersion20250326:
		c.adapter = NewAdapterV20250326()
	case protocolVersion20250618:
		c.adapter = NewAdapterV20250618()
	case protocolVersion20251125:
		c.adapter = NewAdapterV20251125()
	case protocolVersion20260728:
		c.adapter = NewAdapterV20260728()
	case "":
		return fmt.Errorf("proto-version is required (e.g. proto-version=2025-11-25)")
	default:
		return fmt.Errorf("unsupported proto-version %q", c.config.ProtoVersion)
	}

	transport, err := c.createTransport()
	if err != nil {
		return err
	}

	// For authorization_code, try loading persisted token before skipping.
	if c.needsPersistedAuth() {
		transport.Close()
		return ErrNeedsAuth
	}

	// Set up OAuth auth provider if configured.
	c.setupAuth(transport)

	c.storeTransport(transport)
	c.setupNotificationHandler(transport)
	c.state.Store(int32(StateInitializing))

	// Negotiate protocol version using the configured adapter.
	// Attach adapter to HTTP transport before handshake so version headers
	// (MCP-Protocol-Version, etc.) are included on all requests.
	if ht, ok := transport.(*HTTPTransport); ok {
		if ha, ok := c.adapter.(HTTPAdapter); ok {
			ht.SetHTTPAdapter(ha)
		}
	}

	_, err = c.negotiateAndHandshake(ctx)
	if err != nil {
		transport.Close()
		return fmt.Errorf("%w", err)
	}

	c.state.Store(int32(StateReady))

	// Let the adapter start version-specific resources (e.g. GET stream)
	// after the transport is ready. Only for HTTP transport; stdio doesn't
	// use OnTransportReady.
	if ht, ok := transport.(*HTTPTransport); ok {
		if ha, ok := c.adapter.(HTTPAdapter); ok {
			if err := ha.OnTransportReady(ctx, ht); err != nil {
				if c.config.DebugDir != "" {
					if dw := ht.DebugWriter(); dw != nil {
						fmt.Fprintf(dw, "MCP: OnTransportReady failed for %q: %v\n", c.config.Name, err)
					}
				}
				// Non-fatal — the client still works for tool calls via POST.
			}
		}
	}

	c.startMonitor()
	return nil
}

// listAllPages is a generic pagination helper for MCP list methods.
func listAllPages[T any, P any](ctx context.Context, c *Client, op string, method string, extract func(*P) ([]T, string)) ([]T, error) {
	if c.State() != StateReady {
		return nil, fmt.Errorf("%q: %s: %w", c.config.Name, op, c.stateError(op))
	}

	type listParams struct {
		Cursor string `json:"cursor,omitempty"`
	}

	var all []T
	var cursor string

	for {
		var params any
		if cursor != "" {
			params = listParams{Cursor: cursor}
		}

		result, err := c.sendRequest(ctx, method, params)
		if err != nil {
			return nil, fmt.Errorf("%q: %s: %w", c.config.Name, op, err)
		}

		var page P
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("%q: %s: parse: %w", c.config.Name, op, err)
		}

		items, nextCursor := extract(&page)
		all = append(all, items...)

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return all, nil
}

// ListTools fetches the list of available tools from the server.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	tools, err := listAllPages(ctx, c, "list tools", methodListTools,
		func(p *ListToolsResult) ([]Tool, string) { return p.Tools, p.NextCursor })
	if err != nil {
		return nil, err
	}

	// Per the 2026-07-28 spec, clients using Streamable HTTP MUST reject
	// tool definitions with invalid x-mcp-header annotations (exclude
	// them from tools/list). Other transports and protocol versions MAY
	// ignore the annotations entirely.
	rejectInvalid := c.adapter != nil && c.adapter.ProtocolVersion() == protocolVersion20260728
	if rejectInvalid {
		if _, ok := c.loadTransport().(*HTTPTransport); !ok {
			rejectInvalid = false
		}
	}

	// Parse header mappings and filter out invalid tool definitions.
	valid := make([]Tool, 0, len(tools))
	for i := range tools {
		t := &tools[i]
		t.HeaderMappings = parseHeaderMappings(t.InputSchema)
		if rejectInvalid {
			if verr := validateXMcpHeaderAnnotations(t.InputSchema); verr != nil {
				if ht, ok := c.loadTransport().(*HTTPTransport); ok && ht.DebugWriter() != nil {
					fmt.Fprintf(ht.DebugWriter(), "MCP: rejecting tool %q: %v\n", t.Name, verr)
				}
				continue
			}
		}
		valid = append(valid, *t)
	}
	tools = valid

	// Update tools cache with parsed header mappings for x-mcp-header
	// support. Rebuilt against the filtered slice so pointers stay valid.
	c.toolsCache = make(map[string]*Tool, len(tools))
	for i := range tools {
		c.toolsCache[tools[i].Name] = &tools[i]
	}

	// Push tool header mappings to the adapter for Mcp-Param-{Name}
	// injection on subsequent tools/call requests.
	if ha, ok := c.adapter.(HTTPAdapter); ok {
		ha.SetToolHeaderMappings(tools)
	}

	return tools, nil
}

// CallTool invokes a tool on the server and returns the result.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*CallToolResult, error) {
	if c.State() != StateReady {
		return nil, fmt.Errorf("%q: call %s: %w", c.config.Name, name, c.stateError(name))
	}

	result, err := c.sendRequest(ctx, methodCallTool, CallToolRequest{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("call %s on %q: %w", name, c.config.Name, err)
	}

	var callResult CallToolResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &callResult, nil
}

// HasTools returns true if the server advertised tool support.
func (c *Client) HasTools() bool {
	return c.capabilities.Tools != nil
}

// HasResources returns true if the server advertised resource support.
func (c *Client) HasResources() bool {
	return c.capabilities.Resources != nil
}

// ListResources fetches the list of available resources from the server.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	return listAllPages(ctx, c, "list resources", methodListResources,
		func(p *ListResourcesResult) ([]Resource, string) { return p.Resources, p.NextCursor })
}

// ReadResource reads a resource by URI from the server.
func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if c.State() != StateReady {
		return nil, fmt.Errorf("%q: read resource: %w", c.config.Name, c.stateError("read resource"))
	}

	result, err := c.sendRequest(ctx, methodReadResource, ReadResourceRequest{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("read resource %q on %q: %w", uri, c.config.Name, err)
	}

	var readResult ReadResourceResult
	if err := json.Unmarshal(result, &readResult); err != nil {
		return nil, fmt.Errorf("parse resources/read result: %w", err)
	}

	return &readResult, nil
}

// HasPrompts returns true if the server advertised prompt support.
func (c *Client) HasPrompts() bool {
	return c.capabilities.Prompts != nil
}

// ListPrompts fetches the list of available prompts from the server.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	return listAllPages(ctx, c, "list prompts", methodListPrompts,
		func(p *ListPromptsResult) ([]Prompt, string) { return p.Prompts, p.NextCursor })
}

// GetPrompt fetches a prompt by name with optional arguments.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	if c.State() != StateReady {
		return nil, fmt.Errorf("%q: get prompt: %w", c.config.Name, c.stateError("get prompt"))
	}

	result, err := c.sendRequest(ctx, methodGetPrompt, GetPromptRequest{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("get prompt %q on %q: %w", name, c.config.Name, err)
	}

	var promptResult GetPromptResult
	if err := json.Unmarshal(result, &promptResult); err != nil {
		return nil, fmt.Errorf("parse prompts/get result: %w", err)
	}

	return &promptResult, nil
}

// Close shuts down the client and its transport.
func (c *Client) Close() error {
	if !c.closeDone.Swap(true) {
		ch := c.closedCh
		close(ch)

		// Notify adapter before closing transport.
		// Use a short timeout for best-effort cleanup (e.g. session DELETE).
		if c.adapter != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c.adapter.OnClose(cleanupCtx)
		}

		if tp := c.loadTransport(); tp != nil {
			err := tp.Close()
			c.state.Store(int32(StateDisconnected))
			return err
		}
		c.state.Store(int32(StateDisconnected))
	}
	return nil
}

// Done returns a channel that closes when the client is shut down.
func (c *Client) Done() <-chan struct{} {
	return c.closedCh
}

// createTransport creates a Transport based on the server config.
func (c *Client) createTransport() (Transport, error) {
	switch {
	case c.config.Command != "":
		t, err := NewStdioTransport(c.config.Command, c.config.Args, c.config.Env, c.config.DebugDir)
		if err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		return t, nil
	case c.config.URL != "":
		return NewHTTPTransport(c.config.URL, c.config.DebugDir), nil
	default:
		return nil, fmt.Errorf("no command or URL specified")
	}
}

// needsPersistedAuth returns true if the server needs authorization_code
// auth but no token is available (in-memory or on disk).
func (c *Client) needsPersistedAuth() bool {
	if c.config.Auth == nil || c.config.Auth.Type != AuthTypeAuthorizationCode {
		return false
	}
	if c.config.Auth.obtainedToken != nil {
		return false
	}
	if c.tokenStore != nil {
		loaded, loadErr := c.tokenStore.LoadToken(c.config.Name)
		if loadErr == nil && loaded != nil && (loaded.Valid() || loaded.RefreshToken != "") {
			c.config.Auth.obtainedToken = loaded
			return false
		}
	}
	return true
}

// setupAuth creates an auth provider for the transport, if configured.
func (c *Client) setupAuth(transport Transport) {
	if c.config.Auth == nil {
		return
	}

	// RFC 8707 resource indicator for token refresh — required by the
	// 2026-07-28 spec only; empty for legacy protocol versions.
	resource := ""
	if c.config.ProtoVersion == protocolVersion20260728 {
		resource = c.config.URL
	}

	provider := newAuthProvider(c.config.Auth, c.tokenStore, c.config.Name, resource)
	if provider == nil {
		return
	}

	if ht, ok := transport.(*HTTPTransport); ok {
		ht.SetAuthProvider(provider)
	}
}

// ============================================================================
// Transport Death Monitor
// ============================================================================

// startMonitor launches a goroutine that watches for transport death.
func (c *Client) startMonitor() {
	tp := c.loadTransport()
	if tp == nil {
		return
	}

	go c.monitorTransport(tp)
}

// monitorTransport waits for the transport to finish and transitions the
// client to StateFailed if it dies unexpectedly.
func (c *Client) monitorTransport(tp Transport) {
	<-tp.Done()

	if !c.state.CompareAndSwap(int32(StateReady), int32(StateFailed)) {
		return
	}

	if !c.closeDone.Swap(true) {
		ch := c.closedCh
		close(ch)
	}
}

// setupNotificationHandler registers a notification handler on the transport.
func (c *Client) setupNotificationHandler(tp Transport) {
	h := NotificationHandler(c.handleNotification)

	// Both transport types support SetNotificationHandler via their
	// Transport interface or type-specific method.
	switch t := tp.(type) {
	case *StdioTransport:
		t.SetNotificationHandler(h)
	case *HTTPTransport:
		t.SetNotificationHandler(h)
	}
}

// handleNotification processes a server-to-client notification.
func (c *Client) handleNotification(method string) {
	switch method {
	case methodNotificationsToolsListChanged:
		c.MarkStale("server tool list changed, restart required")
	case methodNotificationsResourcesListChanged:
		c.MarkStale("server resource list changed, restart required")
	case methodNotificationsPromptsListChanged:
		c.MarkStale("server prompt list changed, restart required")
	}
}

// Ping sends a ping request to check server health.
func (c *Client) Ping(ctx context.Context) error {
	if c.State() != StateReady {
		return fmt.Errorf("%q: ping: %w", c.config.Name, c.stateError("ping"))
	}
	_, err := c.sendRequest(ctx, methodPing, nil)
	return err
}

// resetState resets the client to StateDisconnected for reconnection.
func (c *Client) resetState() {
	if tp := c.loadTransport(); tp != nil {
		tp.Close()
		c.storeTransport(nil)
	}
	c.state.Store(int32(StateDisconnected))
}

// connectWithOAuthToken persists the obtained OAuth token and reconnects.
func (c *Client) connectWithOAuthToken(ctx context.Context, token *auth.Token) error {
	c.config.Auth.obtainedToken = token
	if c.tokenStore != nil {
		_ = c.tokenStore.SaveToken(c.Name(), token)
	}
	c.resetState()
	if err := c.Connect(ctx); err != nil {
		c.config.Auth.obtainedToken = nil
		return fmt.Errorf("%q: connect after auth: %w", c.Name(), err)
	}
	return nil
}

// ============================================================================
// Transport access helpers
// ============================================================================

// loadTransport returns the current transport, or nil.
func (c *Client) loadTransport() Transport {
	v := c.transport.Load()
	if v == nil {
		return nil
	}
	tp, ok := v.(Transport)
	if !ok {
		return nil
	}
	return tp
}

// storeTransport sets the current transport.
func (c *Client) storeTransport(t Transport) {
	if t == nil {
		c.transport.Store(nil)
	} else {
		c.transport.Store(t)
	}
}

// stateError returns a descriptive error for the current client state.
// It does NOT include the server name — callers wrap with %q instead.
func (c *Client) stateError(string) error {
	st := c.State()
	switch st {
	case StateFailed:
		return fmt.Errorf("server connection lost")
	case StateStale:
		return fmt.Errorf("%s", c.staleReason)
	default:
		return fmt.Errorf("not ready (state=%d)", st)
	}
}

// ============================================================================
// Protocol Negotiation
// ============================================================================

// negotiateAndHandshake performs the handshake and verifies the server
// supports the configured proto-version.
func (c *Client) negotiateAndHandshake(ctx context.Context) (string, error) {
	version, err := c.adapter.Handshake(ctx, c)
	if err != nil {
		return "", fmt.Errorf("handshake: %w", err)
	}
	if version != c.config.ProtoVersion {
		return "", fmt.Errorf("protocol version mismatch: configured %q, server returned %q",
			c.config.ProtoVersion, version)
	}
	return version, nil
}

// isHandshakeMethod returns true if the method is a handshake method.
func isHandshakeMethod(method string) bool {
	return method == methodInitialize || method == methodDiscover
}

// ============================================================================
// JSON-RPC Request/Response
// ============================================================================

// sendRequest sends a JSON-RPC request and returns the response result.
func (c *Client) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	tp := c.loadTransport()
	if tp == nil {
		return nil, fmt.Errorf("no transport")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	id := requestID(fmt.Sprintf("%d", c.reqID.Add(1)))
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsData = data
	}

	// Inject _meta via adapter if the protocol requires it.
	if c.adapter != nil {
		meta := c.adapter.BuildRequestMeta(c)
		if meta != nil {
			var err error
			paramsData, err = injectMeta(paramsData, meta)
			if err != nil {
				return nil, fmt.Errorf("inject _meta: %w", err)
			}
		}
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  paramsData,
	}

	result, err := tp.SendReceive(ctx, req)
	if err != nil {
		if !isHandshakeMethod(method) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			// On stdio, always send a cancellation notification (no per-
			// request stream to close). On HTTP, only send if the protocol
			// version uses notification-based cancellation.
			if _, isHTTP := tp.(*HTTPTransport); !isHTTP || c.adapter.CancelByNotification() {
				// Use a fresh context with timeout: the request ctx is already
				// canceled (that's why we're here), so we need a live context.
				notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer notifyCancel()
				c.sendCanceledNotification(notifyCtx, id, err)
			}
		}
		return nil, err
	}

	// Validate result via adapter.
	if err := c.validateResult(method, result); err != nil {
		return nil, err
	}

	return result, nil
}

// validateResult delegates to the adapter's ValidateResult. No-ops if no
// adapter is set (e.g. before Connect).
func (c *Client) validateResult(method string, result json.RawMessage) error {
	if c.adapter == nil {
		return nil
	}
	return c.adapter.ValidateResult(method, result)
}

// sendCanceledNotification sends a cancellation notification to the server.
// Uses the provided ctx (typically the client's request context) for timeout
// and cancellation. The ctx should NOT be the canceled request context itself
// (which triggered this call) — the caller passes a context with a short
// timeout derived from the client's lifecycle.
func (c *Client) sendCanceledNotification(ctx context.Context, id requestID, cause error) {
	reason := "request canceled"
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "timeout"
	}

	_ = c.sendNotification(ctx, methodNotificationsCanceled, CanceledNotificationParams{
		RequestID: id,
		Reason:    reason,
	})
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
	tp := c.loadTransport()
	if tp == nil {
		return fmt.Errorf("no transport")
	}

	// Marshal params and inject _meta, same as sendRequest.
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal notification params: %w", err)
		}
		paramsData = data
	}
	if c.adapter != nil {
		meta := c.adapter.BuildRequestMeta(c)
		if meta != nil {
			var err error
			paramsData, err = injectMeta(paramsData, meta)
			if err != nil {
				return fmt.Errorf("inject _meta into notification: %w", err)
			}
		}
	}

	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      requestID(""), // notification: no ID (omitempty omits empty string)
		Method:  method,
		Params:  paramsData,
	}

	return tp.Send(ctx, req)
}

// compile-time interface checks.
var _ error = (*RPCError)(nil)

// newAuthProvider creates an auth.TokenProvider from the given AuthConfig.
// resource is the RFC 8707 resource indicator (MCP server URL) sent on
// token refresh; empty for protocol versions that do not require it.
func newAuthProvider(cfg *AuthConfig, tokenStore auth.TokenStore, serverName, resource string) auth.TokenProvider {
	if cfg == nil {
		return nil
	}

	switch cfg.Type {
	case AuthTypeStatic:
		if cfg.Token == "" {
			return nil
		}
		return auth.NewCached(&auth.StaticProvider{
			TokenValue: &auth.Token{
				AccessToken: cfg.Token,
				TokenType:   "Bearer",
			},
		})

	case AuthTypeAuthorizationCode:
		if cfg.obtainedToken != nil {
			base := auth.NewCached(&auth.StaticProvider{
				TokenValue: cfg.obtainedToken,
			})
			if tokenStore == nil {
				return base
			}
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
				Resource:         resource,
			}
			return auth.NewPersistentTokenProvider(base, tokenStore, serverName, refreshCfg)
		}
		if tokenStore != nil {
			refreshCfg := &auth.RefreshConfig{
				TokenEndpoint:    cfg.TokenEndpoint,
				ClientID:         cfg.ClientID,
				ClientSecret:     cfg.ClientSecret,
				ClientAuthMethod: cfg.ClientAuthMethod,
				Resource:         resource,
			}
			return auth.NewPersistentTokenProvider(nil, tokenStore, serverName, refreshCfg)
		}
		return nil

	default:
		return nil
	}
}
