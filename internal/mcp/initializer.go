package mcp

// Initializer manages the entire MCP initialization lifecycle end-to-end.
//
// Usage:
//
//	initializer := mcp.NewInitializer(configs)
//	initializer.Start(ctx)
//	for evt := range initializer.Events() { … }
//	<-initializer.Done()
//
// The session drives the flow by:
//  1. Reading events from Events() channel
//  2. For "auth_required" events: showing a dialog, sending result via mcp_confirm
//  3. For Ctrl+G: calling initializer.Cancel()
//  4. For "done"/"canceled" event: applying final results or cleaning up
//
// Each server runs in its own goroutine. After connecting, each server
// discovers tools, resources, and prompts before sending "connected".
// This means "connected" means the server is fully initialized and ready.
//
// Results are collected via a channel. After all servers complete,
// run() builds the final tools list and system prompt in the original
// config order (deterministic for provider caching) and sends "done".

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp/auth"
)

var errSkipped = errors.New("skipped")

// ============================================================================
// OAuth Discovery Helpers
// ============================================================================

// resolveAuthConfig discovers authorization server metadata and resolves
// the OAuth client credentials for a server.
// client_id and client_secret must be configured by the user in mcp.conf
// via auth-client-id and auth-client-secret.
func resolveAuthConfig(ctx context.Context, cfg *AuthConfig, serverURL string) (*auth.ASMetadata, string, error) {
	meta, err := discoverASMetadata(ctx, cfg, serverURL)
	if err != nil {
		return nil, "", fmt.Errorf("discover AS: %w", err)
	}

	if cfg.ClientID == "" {
		return nil, "", fmt.Errorf("%s requires auth-client-id in mcp.conf. "+
			"Register an OAuth app with the service and set auth-client-id and "+
			"auth-client-secret (if needed). See docs/oauth.md for details", meta.Issuer)
	}

	return meta, cfg.ClientID, nil
}

// discoverASMetadata discovers the authorization server metadata for an
// MCP server. It follows the MCP OAuth discovery chain:
//  1. If token_endpoint is configured, derive issuer from it and try.
//  2. Try direct well-known discovery from the MCP server URL.
//  3. Discover Protected Resource Metadata (from well-known or 401).
//  4. Extract authorization_servers from resource metadata.
//  5. Try well-known discovery on each authorization server URL.
func discoverASMetadata(ctx context.Context, authCfg *AuthConfig, serverURL string) (*auth.ASMetadata, error) {
	// Step 1: If token_endpoint is configured, derive issuer from it and try.
	if authCfg != nil && authCfg.TokenEndpoint != "" {
		issuer := deriveIssuer(authCfg.TokenEndpoint)
		if meta, err := auth.DiscoverASMetadata(ctx, issuer); err == nil {
			return meta, nil
		}
	}

	// Step 2: Try direct well-known discovery from the server URL.
	if meta, err := auth.DiscoverASMetadata(ctx, serverURL); err == nil {
		return meta, nil
	}

	// Step 3: Discover Protected Resource Metadata.
	prm, err := auth.DiscoverProtectedResource(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("discover OAuth for %s: %w", serverURL, err)
	}

	// Step 4: Try each authorization server.
	for _, asURL := range prm.AuthorizationServers {
		if meta, err := auth.DiscoverASMetadata(ctx, asURL); err == nil {
			return meta, nil
		}
	}

	return nil, fmt.Errorf("no authorization server metadata found for %s (discovered servers: %v)",
		serverURL, prm.AuthorizationServers)
}

// deriveIssuer attempts to extract the issuer URL from a token endpoint URL.
// e.g. "https://auth.example.com/token" → "https://auth.example.com"
func deriveIssuer(tokenEndpoint string) string {
	for i := len(tokenEndpoint) - 1; i >= 0; i-- {
		if tokenEndpoint[i] == '/' {
			return tokenEndpoint[:i]
		}
	}
	return tokenEndpoint
}

// InitEvent covers everything that happens during MCP initialization.
// The session receives these from Events() and reacts accordingly.
type InitEventType string

const (
	InitConnecting  InitEventType = "connecting"
	InitConnected   InitEventType = "connected"
	InitFailed      InitEventType = "failed"
	InitAuthConfirm InitEventType = "auth_required"
	InitAuthRunning InitEventType = "auth_running"
	InitDone        InitEventType = "done"
	InitCanceled    InitEventType = "canceled"
)

type InitEvent struct {
	Type   InitEventType
	Server string
	URL    string // set for "auth_required"
	Error  string // set for "failed"

	// Set for "done" — fully converted results
	Tools       []llm.Tool
	SysFragment string
	Manager     *Manager
}

// serverResult holds all discovery data for one server.
type serverResult struct {
	name      string
	tools     []Tool
	resources []Resource
	prompts   []Prompt
	instrs    string
}

// authCodeResult carries the authorization code, redirect URI, and the
// RFC 9207 issuer parameter from the adapter's callback server back to
// the init goroutine.
type authCodeResult struct {
	code        string
	redirectURI string
	iss         string
}

// Initializer orchestrates MCP initialization from start to finish.
// Thread-safe: all public methods are safe to call from any goroutine.
type Initializer struct {
	manager *Manager
	configs []ServerConfig

	events  chan InitEvent // session reads from this
	done    chan struct{}
	started sync.Once

	// Per-server channel for OAuth auth code results.
	authCodeChs map[string]chan authCodeResult

	mu           sync.Mutex // guards authCodeChs and eventsClosed
	eventsClosed bool

	cancel context.CancelFunc // set by Start(), cancels the init context
	ctx    context.Context    // set by Start(); aborts a blocking deliverEvent send
}

// NewInitializer creates an Initializer from server configurations.
// Call Start() to begin initialization.
func NewInitializer(configs []ServerConfig) *Initializer {
	return &Initializer{
		manager:     NewManager(configs),
		configs:     configs,
		events:      make(chan InitEvent, 64),
		done:        make(chan struct{}),
		authCodeChs: make(map[string]chan authCodeResult),
	}
}

// Events returns a channel of initialization events.
// The session must read from this channel until it's closed.
func (in *Initializer) Events() <-chan InitEvent { return in.events }

// Done returns a channel that's closed when initialization is complete.
func (in *Initializer) Done() <-chan struct{} { return in.done }

// Manager returns the underlying MCP Manager.
// Valid before Done() — it holds the client objects even before connections.
func (in *Initializer) Manager() *Manager { return in.manager }

// Start begins initialization in a background goroutine.
// Idempotent — subsequent calls are no-ops.
func (in *Initializer) Start(ctx context.Context) {
	in.started.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		in.cancel = cancel
		in.ctx = runCtx
		go in.run(runCtx)
	})
}

// Cancel aborts the entire initialization.
// Safe to call concurrently — cancels the init context, causing run()
// to exit cleanly. Idempotent.
func (in *Initializer) Cancel() {
	if in.cancel != nil {
		in.cancel()
	}
}

// registerAuthCodeCh creates a buffered auth code channel for a server
// and registers it in the map. The channel must be created BEFORE sending
// the "auth_required" event so that SendAuthCodeResult() can never race
// with registration — the channel is findable the instant the adapter
// sees the event.
func (in *Initializer) registerAuthCodeCh(server string) chan authCodeResult {
	ch := make(chan authCodeResult, 1)
	in.mu.Lock()
	in.authCodeChs[server] = ch
	in.mu.Unlock()
	return ch
}

// unregisterAuthCodeCh removes the auth code channel for a server from
// the map. Idempotent — safe to call after the entry was already removed.
func (in *Initializer) unregisterAuthCodeCh(server string) {
	in.mu.Lock()
	delete(in.authCodeChs, server)
	in.mu.Unlock()
}

// SendAuthCodeResult delivers the authorization code from the adapter's
// callback server to the init goroutine waiting in runOAuthForServer.
// iss is the RFC 9207 issuer parameter from the authorization response,
// if the callback carried one (empty on the manual path).
// Returns false if no init goroutine is waiting for this server.
func (in *Initializer) SendAuthCodeResult(server string, code string, redirectURI string, iss string) bool {
	in.mu.Lock()
	ch, ok := in.authCodeChs[server]
	in.mu.Unlock()
	if !ok {
		return false
	}
	ch <- authCodeResult{code: code, redirectURI: redirectURI, iss: iss}
	return true
}

// ============================================================================
// Internal: run() — launches per-server goroutines, collects results via channel
// ============================================================================

func (in *Initializer) run(ctx context.Context) {
	defer close(in.done)
	defer func() {
		if ctx.Err() != nil {
			in.sendEvent(InitEvent{Type: InitCanceled})
		}
		in.mu.Lock()
		in.eventsClosed = true
		close(in.events)
		in.mu.Unlock()
	}()

	clients := in.manager.Clients()
	n := len(clients)
	resultCh := make(chan serverResult, n)

	for _, c := range clients {
		go func(client *Client) {
			resultCh <- in.collectServerResult(ctx, client)
		}(c)
	}

	// Collect all results, but don't block on shutdown.
	results := make(map[string]serverResult, n)
	for i := 0; i < n; i++ {
		select {
		case r := <-resultCh:
			results[r.name] = r
		case <-ctx.Done():
			return
		}
	}

	if ctx.Err() != nil {
		return
	}

	var evt InitEvent
	in.buildFinalResults(results, &evt)
	// Deliver InitDone with guaranteed delivery: dropping it (as the lossy
	// sendEvent would when the channel is full) makes the session treat a
	// successful init as aborted — MCP tools never load.
	in.deliverEvent(evt)
}

// collectServerResult handles the full lifecycle of a single server:
// connect (with OAuth if needed), discover capabilities, and return results.
func (in *Initializer) collectServerResult(ctx context.Context, c *Client) serverResult {
	var r serverResult
	r.name = c.Name()

	in.sendEvent(InitEvent{Type: InitConnecting, Server: c.Name()})

	if err := in.connectServer(ctx, c); err != nil {
		in.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		return r
	}

	in.discoverCapabilities(ctx, c, &r)
	in.sendEvent(InitEvent{Type: InitConnected, Server: c.Name()})
	return r
}

// connectServer connects to the server, running the OAuth authorization
// flow first if the server requires it.
func (in *Initializer) connectServer(ctx context.Context, c *Client) error {
	if !c.needsPersistedAuth() {
		if err := c.Connect(ctx); err != nil {
			return fmt.Errorf("%q: %w", c.Name(), err)
		}
		return nil
	}
	return in.connectOAuth(ctx, c)
}

// connectOAuth runs the OAuth authorization code flow and then connects.
func (in *Initializer) connectOAuth(ctx context.Context, c *Client) error {
	cfg := c.config.Auth

	// 1. Discover authorization server metadata.
	meta, clientID, err := resolveAuthConfig(ctx, cfg, c.config.URL)
	if err != nil {
		return fmt.Errorf("%q: %w", c.Name(), err)
	}

	cfg.TokenEndpoint = meta.TokenEndpoint
	cfg.ClientID = clientID
	authMethod, err := auth.SelectAuthMethod(meta)
	if err != nil {
		return fmt.Errorf("%q: %w", c.Name(), err)
	}
	cfg.ClientAuthMethod = authMethod

	// 2. Run the interactive OAuth flow.
	if err := in.runOAuthForServer(ctx, c, meta, clientID); err != nil {
		if errors.Is(err, errSkipped) {
			return fmt.Errorf("%q: skipped", c.Name())
		}
		return err
	}

	return nil
}

// runOAuthForServer runs the OAuth flow for a single server.
//
// It builds the authorization URL with {{redirect_uri}} and {{state}} placeholders
// and sends it to the adapter. The adapter starts a local callback server,
// fills in the placeholders, opens the browser, and sends the authorization
// code back via SendAuthCodeResult().
func (in *Initializer) runOAuthForServer(ctx context.Context, c *Client, meta *auth.ASMetadata, clientID string) error {
	cfg := c.config.Auth

	pkce, err := auth.NewPKCE()
	if err != nil {
		return fmt.Errorf("%q: pkce: %w", c.Name(), err)
	}

	placeholderURI := "{{redirect_uri}}"
	placeholderState := "{{state}}"

	// RFC 8707 resource indicator — the canonical MCP server URL.
	// Required by the 2026-07-28 spec (MUST be in authorization and token
	// requests); older protocol versions do not define it, so leave empty.
	resource := ""
	if c.config.ProtoVersion == protocolVersion20260728 {
		resource = c.config.URL
	}

	authURL, err := auth.BuildAuthorizationURL(meta, &auth.AuthCodeConfig{
		ClientID:     clientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
		Resource:     resource,
	}, pkce, placeholderURI, placeholderState)
	if err != nil {
		return fmt.Errorf("%q: build auth URL: %w", c.Name(), err)
	}

	// Register the auth code channel BEFORE sending the auth_required
	// event. Otherwise SendAuthCodeResult() could arrive before the
	// channel exists (adapter confirms instantly) and be rejected with
	// NOT_FOUND, while this goroutine blocks forever waiting for a
	// code that was already delivered.
	authCodeCh := in.registerAuthCodeCh(c.Name())

	// Send URL template to adapter and wait for result.
	in.sendEvent(InitEvent{
		Type:   InitAuthConfirm,
		Server: c.Name(),
		URL:    authURL,
	})

	var acr authCodeResult
	select {
	case acr = <-authCodeCh:
	case <-ctx.Done():
		in.unregisterAuthCodeCh(c.Name())
		return fmt.Errorf("%q: %w", c.Name(), ctx.Err())
	}

	in.unregisterAuthCodeCh(c.Name())

	if acr.code == "" {
		return errSkipped
	}

	// RFC 9207: validate the issuer before redeeming the authorization
	// code. A present `iss` must exactly match the recorded issuer; if the
	// AS advertises `authorization_response_iss_parameter_supported` and
	// `iss` is absent, the response is rejected.
	if issErr := auth.ValidateIssParam(meta, acr.iss); issErr != nil {
		return fmt.Errorf("%q: %w", c.Name(), issErr)
	}

	in.sendEvent(InitEvent{Type: InitAuthRunning, Server: c.Name()})

	oauthToken, err := auth.ExchangeCode(ctx, meta, &auth.AuthCodeConfig{
		ClientID:     clientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
		Resource:     resource,
	}, pkce, acr.redirectURI, acr.code)
	if err != nil {
		return fmt.Errorf("%q: exchange code: %w", c.Name(), err)
	}

	if oauthToken.AccessToken == "" {
		return fmt.Errorf("%q: OAuth returned empty access token", c.Name())
	}

	token := &auth.Token{
		AccessToken:      oauthToken.AccessToken,
		TokenType:        oauthToken.TokenType,
		RefreshToken:     oauthToken.RefreshToken,
		ExpiresAt:        oauthToken.ExpiresAt,
		Scopes:           oauthToken.Scopes,
		TokenEndpoint:    meta.TokenEndpoint,
		ClientID:         clientID,
		ClientAuthMethod: cfg.ClientAuthMethod,
	}
	return c.connectWithOAuthToken(ctx, token)
}

// discoverCapabilities discovers tools, resources,
// and prompts for a connected server.
func (in *Initializer) discoverCapabilities(ctx context.Context, c *Client, r *serverResult) {
	if c.HasTools() {
		if tools, err := c.ListTools(ctx); err != nil {
			in.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		} else {
			r.tools = tools
		}
	}
	if c.HasResources() {
		if resources, err := c.ListResources(ctx); err != nil {
			in.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		} else {
			r.resources = resources
		}
	}
	if c.HasPrompts() {
		if prompts, err := c.ListPrompts(ctx); err != nil {
			in.sendEvent(InitEvent{Type: InitFailed, Server: c.Name(), Error: err.Error()})
		} else {
			r.prompts = prompts
		}
	}
	if instr := c.Instructions(); instr != "" {
		r.instrs = instr
	}
}

// buildFinalResults builds the tools list, system prompt fragment,
// and error list in config order (deterministic for provider caching),
// then writes them into evt.
func (in *Initializer) buildFinalResults(results map[string]serverResult, evt *InitEvent) {
	var allTools []llm.Tool
	var frag strings.Builder

	for _, cfg := range in.configs {
		r, ok := results[cfg.Name]
		if !ok {
			continue
		}

		if len(r.tools) > 0 {
			serverTools := ToolsToAgentTools(map[string][]Tool{cfg.Name: r.tools}, in.manager)
			allTools = append(allTools, serverTools...)
		}

		if len(r.resources) > 0 {
			allTools = append(allTools, newReadResourceTool(cfg.Name, in.manager))
			formatResourceContext(&frag, cfg.Name, r.resources)
		}

		if len(r.prompts) > 0 {
			allTools = append(allTools, newGetPromptTool(cfg.Name, in.manager))
			formatPromptContext(&frag, cfg.Name, r.prompts)
		}

		if r.instrs != "" {
			frag.WriteString(fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", cfg.Name, r.instrs))
		}
	}

	evt.Type = InitDone
	evt.Tools = dedupeToolNames(allTools)
	evt.SysFragment = frag.String()
	evt.Manager = in.manager
}

func formatResourceContext(frag *strings.Builder, name string, resources []Resource) {
	frag.WriteString(fmt.Sprintf("\n\nAvailable resources from MCP server %q:", name))
	for _, r := range resources {
		frag.WriteString(fmt.Sprintf("\n  - %s", r.URI))
		if r.Name != "" {
			frag.WriteString(fmt.Sprintf(" (name: %q", r.Name))
			if r.Description != "" {
				frag.WriteString(fmt.Sprintf(", description: %q", r.Description))
			}
			if r.MIMEType != "" {
				frag.WriteString(fmt.Sprintf(", mimeType: %q", r.MIMEType))
			}
			frag.WriteString(")")
		} else if r.Description != "" {
			frag.WriteString(fmt.Sprintf(" (description: %q)", r.Description))
		}
	}
}

func formatPromptContext(frag *strings.Builder, name string, prompts []Prompt) {
	frag.WriteString(fmt.Sprintf("\n\nAvailable prompts from MCP server %q:", name))
	for _, p := range prompts {
		frag.WriteString(fmt.Sprintf("\n  - %s", p.Name))
		if p.Description != "" {
			frag.WriteString(fmt.Sprintf(" (description: %q)", p.Description))
		}
		if len(p.Arguments) > 0 {
			frag.WriteString("\n    Arguments:")
			for _, a := range p.Arguments {
				required := ""
				if a.Required {
					required = " (required)"
				}
				frag.WriteString(fmt.Sprintf("\n      - %s: %s%s", a.Name, a.Description, required))
			}
		}
	}
}

// ============================================================================
// Helper
// ============================================================================

func (in *Initializer) sendEvent(evt InitEvent) {
	in.mu.Lock()
	defer in.mu.Unlock()

	if in.eventsClosed {
		return
	}
	select {
	case in.events <- evt:
	default:
	}
}

// deliverEvent sends a terminal event (InitDone) to the session, blocking
// until it is received or the init context is canceled. Unlike sendEvent,
// it never drops the event: the lossy path is fine for progress updates
// but a dropped InitDone would leave the session in the wrong lifecycle
// state. Blocking is safe — the session's main loop reads events
// continuously, and if the session has exited, its cancellation has
// already propagated to the init context, so the send cannot block
// forever. Must only be called from run() after all per-server goroutines
// have finished, so holding mu across the send cannot stall them.
func (in *Initializer) deliverEvent(evt InitEvent) {
	in.mu.Lock()
	defer in.mu.Unlock()

	if in.eventsClosed {
		return
	}
	select {
	case in.events <- evt:
	case <-in.ctx.Done():
	}
}
