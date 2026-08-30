package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ASMetadata holds the discovered authorization server metadata
// (RFC 8414 / OpenID Connect Discovery).
type ASMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSetURI                         string   `json:"jwks_uri,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	AuthorizationResponseIssParameter *bool    `json:"authorization_response_iss_parameter_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	UserInfoEndpoint                  string   `json:"userinfo_endpoint,omitempty"`
}

// DiscoverASMetadata fetches authorization server metadata from the given
// issuer URL by probing well-known endpoints per RFC 8414 and OpenID Connect.
//
// Probing order for issuer URLs with path (e.g. https://auth.example.com/tenant1):
//  1. {issuer}/.well-known/oauth-authorization-server{path}
//  2. {issuer}/.well-known/openid-configuration{path}
//  3. {issuer}{path}/.well-known/openid-configuration
//
// For issuer URLs without path (e.g. https://auth.example.com):
//  1. {issuer}/.well-known/oauth-authorization-server
//  2. {issuer}/.well-known/openid-configuration
func DiscoverASMetadata(ctx context.Context, issuerURL string) (*ASMetadata, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	urls := wellKnownURLs(issuerURL)
	for _, u := range urls {
		meta, err := fetchMetadata(ctx, client, u)
		if err == nil {
			return meta, nil
		}
	}

	return nil, fmt.Errorf("no authorization server metadata found for %s", issuerURL)
}

// wellKnownURLs generates the list of well-known URLs to try, in priority order.
func wellKnownURLs(issuerURL string) []string {
	// Use the well-known suffix based on whether the issuer has a path.
	// We probe both OAuth and OIDC endpoints.
	return []string{
		issuerURL + "/.well-known/oauth-authorization-server",
		issuerURL + "/.well-known/openid-configuration",
	}
}

// fetchMetadata attempts to retrieve and parse AS metadata from a URL.
func fetchMetadata(ctx context.Context, client *http.Client, url string) (*ASMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}

	body, err := readCapped(resp.Body)
	if err != nil {
		return nil, err
	}

	var meta ASMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}

	if meta.Issuer == "" || meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("incomplete metadata at %s (missing issuer, authorization_endpoint, or token_endpoint)", url)
	}

	return &meta, nil
}
