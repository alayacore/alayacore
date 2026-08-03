package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RefreshConfig holds the parameters needed to refresh an access token.
type RefreshConfig struct {
	// TokenEndpoint is the OAuth token endpoint URL.
	TokenEndpoint string

	// ClientID is the OAuth client identifier.
	ClientID string

	// ClientSecret is the OAuth client secret (optional for public clients).
	ClientSecret string

	// ClientAuthMethod is the OAuth client authentication method to use.
	// Supported values: "none", "client_secret_basic", or "client_secret_post".
	// "none" means public client (no client authentication).
	// If empty, defaults to "client_secret_basic".
	ClientAuthMethod string

	// Resource is the RFC 8707 resource indicator (the canonical MCP
	// server URL). Required by the 2026-07-28 protocol (MUST be included
	// in token requests); empty for legacy versions.
	Resource string
}

// PersistentTokenProvider wraps a TokenProvider with on-disk persistence
// and automatic refresh-token based renewal.
//
// Token lifecycle:
//  1. On first Token() call, try loading from disk via TokenStore.
//  2. If the cached token is expired and has a refresh_token, use the
//     refresh grant to obtain a new token automatically.
//  3. If refresh fails (e.g. refresh token expired), fall back to the
//     inner provider (which may initiate a new interactive flow).
//  4. After any successful token acquisition (refresh or inner provider),
//     persist the token to disk.
type PersistentTokenProvider struct {
	inner    TokenProvider
	store    TokenStore
	serverID string // identifier for this server's token on disk
	refresh  *RefreshConfig
	mu       sync.Mutex
	cached   *Token
}

// NewPersistentTokenProvider creates a provider that caches in-memory,
// persists to disk, and automatically uses refresh tokens.
//
// Parameters:
//   - inner: the underlying provider (e.g. auth code flow, or nil for
//     providers that don't need a fallback)
//   - store: the TokenStore for persistence (may be nil to skip persistence)
//   - serverID: unique identifier for this server (used as the filename)
//   - refresh: configuration for token refresh (may be nil to disable refresh)
func NewPersistentTokenProvider(inner TokenProvider, store TokenStore, serverID string, refresh *RefreshConfig) *PersistentTokenProvider {
	return &PersistentTokenProvider{
		inner:    inner,
		store:    store,
		serverID: serverID,
		refresh:  refresh,
	}
}

// Token returns a valid access token, using refresh or fallback as needed.
func (p *PersistentTokenProvider) Token(ctx context.Context) (*Token, error) {
	tok := p.getCached()

	// Fast path: cached token is still valid.
	if tok != nil && tok.Valid() {
		cp := *tok
		return &cp, nil
	}

	// Cached token expired or missing — try loading from disk.
	if tok == nil {
		tok = p.tryLoadFromDisk()
		if tok != nil && tok.Valid() {
			cp := *tok
			return &cp, nil
		}
	}

	// If we have a token that is expired but has a refresh token, try refreshing.
	if tok != nil && tok.RefreshToken != "" && !tok.Valid() && p.canRefresh() {
		if newTok, err := p.refreshToken(ctx, tok.RefreshToken); err == nil {
			p.setCached(newTok)
			p.persistToken(newTok)
			cp := *newTok
			return &cp, nil
		}
		// Refresh failed — fall through to inner provider.
	}

	// Try inner provider as fallback.
	if p.inner != nil {
		return p.tokenFromInner(ctx)
	}

	// Return expired token if we have one (caller may still try it).
	if tok != nil {
		cp := *tok
		return &cp, nil
	}

	return nil, fmt.Errorf("no token available for %s", p.serverID)
}

// getCached returns the cached token, or nil.
func (p *PersistentTokenProvider) getCached() *Token {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cached
}

// setCached stores a token in the cache.
func (p *PersistentTokenProvider) setCached(tok *Token) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached = tok
}

// tryLoadFromDisk attempts to load a token from the TokenStore.
// Returns the loaded token (which may be expired) or nil.
func (p *PersistentTokenProvider) tryLoadFromDisk() *Token {
	if p.store == nil {
		return nil
	}
	loaded, err := p.store.LoadToken(p.serverID)
	if err != nil || loaded == nil {
		return nil
	}
	p.setCached(loaded)

	// If the loaded token has refresh metadata (token_endpoint, client_id)
	// but the provider's refresh config is empty, populate it from the
	// token so that automatic refresh works across restarts.
	if loaded.TokenEndpoint != "" && loaded.ClientID != "" {
		p.mu.Lock()
		if p.refresh == nil {
			p.refresh = &RefreshConfig{}
		}
		if p.refresh.TokenEndpoint == "" {
			p.refresh.TokenEndpoint = loaded.TokenEndpoint
		}
		if p.refresh.ClientID == "" {
			p.refresh.ClientID = loaded.ClientID
		}
		if p.refresh.ClientAuthMethod == "" {
			p.refresh.ClientAuthMethod = loaded.ClientAuthMethod
		}
		p.mu.Unlock()
	}

	return loaded
}

// canRefresh returns true if refresh configuration is available.
func (p *PersistentTokenProvider) canRefresh() bool {
	return p.refresh != nil
}

// tokenFromInner calls the inner provider and persists the result.
// If the inner provider returns an expired token with a refresh token,
// it attempts to refresh first.
func (p *PersistentTokenProvider) tokenFromInner(ctx context.Context) (*Token, error) {
	newTok, err := p.inner.Token(ctx)
	if err != nil {
		return nil, err
	}
	if newTok == nil || newTok.AccessToken == "" {
		return nil, fmt.Errorf("token provider returned empty token")
	}

	// If inner returned an expired token that has a refresh token, try refreshing.
	if !newTok.Valid() && newTok.RefreshToken != "" && p.canRefresh() {
		if refreshed, refreshErr := p.refreshToken(ctx, newTok.RefreshToken); refreshErr == nil {
			p.setCached(refreshed)
			p.persistToken(refreshed)
			cp := *refreshed
			return &cp, nil
		}
	}

	p.setCached(newTok)
	p.persistToken(newTok)
	cp := *newTok
	return &cp, nil
}

// refreshToken uses the refresh grant to obtain a new access token.
func (p *PersistentTokenProvider) refreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	if p.refresh == nil {
		return nil, fmt.Errorf("refresh not configured")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", p.refresh.ClientID)

	if p.refresh.Resource != "" {
		data.Set("resource", p.refresh.Resource)
	}

	authMethod := p.refresh.ClientAuthMethod
	if authMethod == "" {
		authMethod = AuthMethodClientSecretBasic
	}

	// For confidential clients (basic/post), a client_secret is required.
	// Public clients (none) skip authentication entirely.
	if authMethod != AuthMethodNone && p.refresh.ClientSecret == "" {
		return nil, fmt.Errorf("client_secret is required when using %q authentication method for refresh", authMethod)
	}

	useBasic := authMethod == AuthMethodClientSecretBasic && p.refresh.ClientSecret != ""
	usePost := authMethod == AuthMethodClientSecretPost && p.refresh.ClientSecret != ""

	if usePost {
		data.Set("client_secret", p.refresh.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.refresh.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}

	if useBasic {
		auth := base64.StdEncoding.EncodeToString([]byte(p.refresh.ClientID + ":" + p.refresh.ClientSecret))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	token, err := p.doRefreshRequest(req, refreshToken)
	if err != nil {
		return nil, err
	}
	// Preserve the client auth method so it survives across refreshes.
	token.ClientAuthMethod = p.refresh.ClientAuthMethod
	return token, nil
}

// doRefreshRequest performs the HTTP request and parses the refresh token response.
func (p *PersistentTokenProvider) doRefreshRequest(req *http.Request, oldRefreshToken string) (*Token, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d on refresh: %s",
			resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w (body: %s)", err, string(body))
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("refresh returned no access_token (body: %s)", string(body))
	}

	token := &Token{
		AccessToken: tokenResp.AccessToken,
		TokenType:   tokenResp.TokenType,
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if tokenResp.RefreshToken != "" {
		token.RefreshToken = tokenResp.RefreshToken
	} else {
		// Keep the old refresh token if server didn't rotate it.
		token.RefreshToken = oldRefreshToken
	}
	if tokenResp.Scope != "" {
		token.Scopes = strings.Split(tokenResp.Scope, " ")
	}

	return token, nil
}

// InvalidateToken clears the cached token and optionally deletes the
// persisted token from disk. This forces the next Token() call to
// fall back to the inner provider.
func (p *PersistentTokenProvider) InvalidateToken(_ context.Context) error {
	p.mu.Lock()
	p.cached = nil
	p.mu.Unlock()

	if p.store != nil {
		if err := p.store.DeleteToken(p.serverID); err != nil {
			return err
		}
	}
	return nil
}

// persistToken saves the token to disk via the store.
// Errors are silently ignored — persistence is best-effort.
func (p *PersistentTokenProvider) persistToken(tok *Token) {
	if p.store == nil {
		return
	}
	_ = p.store.SaveToken(p.serverID, tok) // best-effort
}
