package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProtectedResourceMetadata represents the OAuth 2.0 Protected Resource
// Metadata (RFC 9728) returned by an MCP server.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
	ResourceName           string   `json:"resource_name,omitempty"`
}

// DiscoverProtectedResource discovers the OAuth 2.0 Protected Resource
// Metadata for an MCP server by:
//  1. Sending an unauthenticated request and parsing the 401 WWW-Authenticate header
//  2. Falling back to probing well-known URLs
func DiscoverProtectedResource(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Try well-known URL first (simpler, no 401 needed).
	meta, err := fetchProtectedResourceWellKnown(ctx, client, serverURL)
	if err == nil {
		return meta, nil
	}

	// Fallback: send an unauthenticated request to trigger 401.
	meta, err = fetchProtectedResourceFrom401(ctx, client, serverURL)
	if err == nil {
		return meta, nil
	}

	return nil, fmt.Errorf("could not discover protected resource metadata for %s", serverURL)
}

// fetchProtectedResourceWellKnown probes the well-known path
// for the Protected Resource Metadata document.
func fetchProtectedResourceWellKnown(ctx context.Context, client *http.Client, serverURL string) (*ProtectedResourceMetadata, error) {
	// Try both root-level and path-level well-known URIs per RFC 9728.
	urls := protectedResourceWellKnownURLs(serverURL)

	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		body, err := readCapped(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		var meta ProtectedResourceMetadata
		if err := json.Unmarshal(body, &meta); err != nil {
			continue
		}
		if len(meta.AuthorizationServers) == 0 {
			continue
		}
		return &meta, nil
	}

	return nil, fmt.Errorf("no protected resource metadata at well-known URIs")
}

// fetchProtectedResourceFrom401 sends an unauthenticated request to the
// MCP server and parses the WWW-Authenticate header from the 401 response.
func fetchProtectedResourceFrom401(ctx context.Context, client *http.Client, serverURL string) (*ProtectedResourceMetadata, error) {
	// Send an empty JSON-RPC request to trigger 401.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req, err := http.NewRequestWithContext(ctx, "POST", serverURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("expected 401, got %d", resp.StatusCode)
	}

	// Parse WWW-Authenticate header for resource_metadata.
	authHeader := resp.Header.Get("WWW-Authenticate")
	if authHeader == "" {
		return nil, fmt.Errorf("no WWW-Authenticate header in 401 response")
	}

	resourceMetaURL := extractResourceMetadataURL(authHeader)
	if resourceMetaURL == "" {
		return nil, fmt.Errorf("no resource_metadata in WWW-Authenticate header")
	}

	// Fetch the resource metadata document.
	metaReq, err := http.NewRequestWithContext(ctx, "GET", resourceMetaURL, nil)
	if err != nil {
		return nil, err
	}
	metaResp, err := client.Do(metaReq)
	if err != nil {
		return nil, err
	}
	defer metaResp.Body.Close()

	if metaResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resource metadata endpoint returned %d", metaResp.StatusCode)
	}

	metaBody, err := readCapped(metaResp.Body)
	if err != nil {
		return nil, err
	}

	var prm ProtectedResourceMetadata
	if err := json.Unmarshal(metaBody, &prm); err != nil {
		return nil, err
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("resource metadata has no authorization_servers")
	}

	return &prm, nil
}

// extractResourceMetadataURL extracts the resource_metadata URL from a
// WWW-Authenticate header value.
// Example:
//
//	WWW-Authenticate: Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource/mcp"
func extractResourceMetadataURL(authHeader string) string {
	// Look for resource_metadata="..." in the header.
	marker := `resource_metadata=`
	idx := strings.Index(authHeader, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	if start >= len(authHeader) {
		return ""
	}
	// Expect opening quote.
	if authHeader[start] != '"' {
		return ""
	}
	start++ // skip opening quote
	end := strings.IndexByte(authHeader[start:], '"')
	if end < 0 {
		return ""
	}
	return authHeader[start : start+end]
}

// protectedResourceWellKnownURLs generates well-known URLs to try
// for the Protected Resource Metadata document, per RFC 9728.
func protectedResourceWellKnownURLs(serverURL string) []string {
	// Strip trailing slash for consistent URL construction.
	serverURL = strings.TrimRight(serverURL, "/")

	urls := make([]string, 0, 2)

	// Root-level well-known.
	urls = append(urls, serverURL+"/.well-known/oauth-protected-resource")

	// If the server URL has a path, also try the path-specific well-known.
	// e.g. https://example.com/mcp → /.well-known/oauth-protected-resource/mcp
	if strings.Contains(serverURL, "://") {
		// Extract the path component.
		parts := strings.SplitN(serverURL, "://", 2)
		if len(parts) == 2 {
			hostAndPath := parts[1]
			if slashIdx := strings.IndexByte(hostAndPath, '/'); slashIdx >= 0 {
				path := hostAndPath[slashIdx:]
				baseURL := parts[0] + "://" + hostAndPath[:slashIdx]
				urls = append(urls, baseURL+"/.well-known/oauth-protected-resource"+path)
			}
		}
	}

	return urls
}
