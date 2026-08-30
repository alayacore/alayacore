package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client authentication methods for the OAuth token endpoint.
const (
	AuthMethodClientSecretBasic = "client_secret_basic"
	AuthMethodClientSecretPost  = "client_secret_post"
	AuthMethodNone              = "none"
)

// ErrUnsupportedAuthMethod is returned when the authorization server only
// advertises client authentication methods that this client does not support.
var ErrUnsupportedAuthMethod = errors.New("server requires a client authentication method not supported by this client")

// SelectAuthMethod determines the client authentication method based on the
// authorization server's advertised capabilities.
//
// Returns the method to use, or ErrUnsupportedAuthMethod if the server only
// lists methods this client doesn't implement.
func SelectAuthMethod(meta *ASMetadata) (string, error) {
	if len(meta.TokenEndpointAuthMethodsSupported) == 0 {
		// No methods advertised — use OAuth 2.1 recommended default.
		return AuthMethodClientSecretBasic, nil
	}

	hasBasic := false
	hasPost := false
	hasNone := false
	for _, m := range meta.TokenEndpointAuthMethodsSupported {
		switch m {
		case AuthMethodClientSecretBasic:
			hasBasic = true
		case AuthMethodClientSecretPost:
			hasPost = true
		case AuthMethodNone:
			hasNone = true
		}
	}

	switch {
	case hasBasic:
		return AuthMethodClientSecretBasic, nil
	case hasPost:
		return AuthMethodClientSecretPost, nil
	case hasNone:
		return AuthMethodNone, nil
	default:
		// Server only lists methods we don't implement (e.g. private_key_jwt,
		// tls_client_auth). Don't guess — error out.
		return "", ErrUnsupportedAuthMethod
	}
}

// AuthCodeConfig holds parameters for the authorization code flow.
//
//nolint:revive // stutter is acceptable for clarity
type AuthCodeConfig struct {
	ClientID     string
	ClientSecret string // optional, for confidential clients
	Scopes       []string
	// Resource is the RFC 8707 resource indicator (the canonical MCP
	// server URL). Required by the 2026-07-28 protocol (MUST be included
	// in authorization and token requests); empty for legacy versions.
	Resource string
}

// BuildAuthorizationURL constructs the authorization URL with all required parameters.
func BuildAuthorizationURL(meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, state string) (string, error) {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", cfg.ClientID)
	params.Set("code_challenge", pkce.CodeChallenge)
	params.Set("code_challenge_method", pkce.Method)

	if len(cfg.Scopes) > 0 {
		params.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	if cfg.Resource != "" {
		params.Set("resource", cfg.Resource)
	}

	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization_endpoint: %w", err)
	}
	// Build query string from safe params, then manually append
	// placeholders so they remain raw ({{...}}) in the URL.
	q := params.Encode()
	q += "&redirect_uri=" + redirectURI + "&state=" + state
	u.RawQuery = q
	return u.String(), nil
}

// ExchangeCode exchanges the authorization code for tokens at the token endpoint.
func ExchangeCode(ctx context.Context, meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, code string) (*Token, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("code_verifier", pkce.CodeVerifier)
	data.Set("client_id", cfg.ClientID)

	if cfg.Resource != "" {
		data.Set("resource", cfg.Resource)
	}

	authMethod, err := SelectAuthMethod(meta)
	if err != nil {
		return nil, err
	}
	if authMethod != AuthMethodNone && cfg.ClientSecret == "" {
		return nil, fmt.Errorf("client_secret is required when using %q authentication method", authMethod)
	}
	useBasic := authMethod == AuthMethodClientSecretBasic && cfg.ClientSecret != ""
	usePost := authMethod == AuthMethodClientSecretPost && cfg.ClientSecret != ""
	// If the server only supports "none" (public client), don't send
	// any client authentication regardless of configured client_secret.
	// Sending credentials when the server explicitly says it only supports
	// "none" will be rejected.
	if usePost {
		data.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", meta.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}

	if useBasic {
		auth := base64.StdEncoding.EncodeToString([]byte(cfg.ClientID + ":" + cfg.ClientSecret))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	return doTokenRequest(req)
}

// doTokenRequest performs the HTTP POST and parses the token response.
func doTokenRequest(req *http.Request) (*Token, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := readCapped(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
		Error        string `json:"error,omitempty"`
		ErrorDesc    string `json:"error_description,omitempty"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w (body: %s)", err, snippet(body))
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("token endpoint error: %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access_token (body: %s)", snippet(body))
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
	}
	if tokenResp.Scope != "" {
		token.Scopes = strings.Split(tokenResp.Scope, " ")
	}

	return token, nil
}

// ValidateIssParam validates the RFC 9207 `iss` parameter from an
// authorization response against the recorded authorization server
// issuer, per RFC 9207 Section 2.4. It MUST be called before the
// authorization code is transmitted to any token endpoint.
//
// Rules:
//   - If the authorization server advertises
//     `authorization_response_iss_parameter_supported: true` and `iss` is
//     absent, the response MUST be rejected.
//   - A present `iss` MUST exactly match the recorded issuer. No scheme or
//     host case folding, default-port elision, trailing-slash, or
//     percent-encoding normalization (RFC 3986 Sections 6.2.2-6.2.3) is
//     applied before comparison.
//
// Legacy protocol versions do not define RFC 9207; when the metadata does
// not advertise support and no `iss` is present, validation passes.
func ValidateIssParam(meta *ASMetadata, iss string) error {
	if meta == nil {
		return nil
	}
	if iss == "" {
		if meta.AuthorizationResponseIssParameter != nil && *meta.AuthorizationResponseIssParameter {
			return fmt.Errorf("authorization response missing required iss parameter (RFC 9207)")
		}
		return nil
	}
	if iss != meta.Issuer {
		return fmt.Errorf("authorization response iss %q does not match recorded issuer %q (RFC 9207)",
			iss, meta.Issuer)
	}
	return nil
}
