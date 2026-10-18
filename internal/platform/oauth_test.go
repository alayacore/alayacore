package platform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// callbackGet performs a GET on the callback URL with a context.
func callbackGet(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// TestStartCallbackServer_ReturnsIss verifies that the RFC 9207 `iss`
// parameter from the authorization response is captured and returned.
func TestStartCallbackServer_ReturnsIss(t *testing.T) {
	state := RandomState()
	resultCh, redirectURI, cleanup := StartCallbackServer("127.0.0.1:0", state, "test-server")
	defer cleanup()

	callbackGet(t, redirectURI+"?code=auth-code-123&state="+state+"&iss=https%3A%2F%2Fauth.example.com")

	select {
	case res := <-resultCh:
		if res.Err != nil {
			t.Fatalf("callback error: %v", res.Err)
		}
		if res.Code != "auth-code-123" {
			t.Errorf("Code = %q, want %q", res.Code, "auth-code-123")
		}
		if res.Iss != "https://auth.example.com" {
			t.Errorf("Iss = %q, want %q", res.Iss, "https://auth.example.com")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

// TestStartCallbackServer_NoIss verifies that the Iss field is empty when
// the authorization response carries no iss parameter.
func TestStartCallbackServer_NoIss(t *testing.T) {
	state := RandomState()
	resultCh, redirectURI, cleanup := StartCallbackServer("127.0.0.1:0", state, "test-server")
	defer cleanup()

	callbackGet(t, redirectURI+"?code=auth-code-123&state="+state)

	select {
	case res := <-resultCh:
		if res.Err != nil {
			t.Fatalf("callback error: %v", res.Err)
		}
		if res.Iss != "" {
			t.Errorf("Iss = %q, want empty", res.Iss)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

// TestStartCallbackServer_StrayRequestsDoNotAbortFlow verifies that
// requests that are not this flow's callback — no parameters, wrong state,
// spoofed provider error without state — are ignored: they must not consume
// the result slot, so a subsequent valid callback still completes the flow.
func TestStartCallbackServer_StrayRequestsDoNotAbortFlow(t *testing.T) {
	state := RandomState()
	resultCh, redirectURI, cleanup := StartCallbackServer("127.0.0.1:0", state, "test-server")
	defer cleanup()

	// Stray request: no parameters at all.
	callbackGet(t, redirectURI)
	// Stale callback: valid-looking code but wrong state.
	callbackGet(t, fmt.Sprintf("%s?code=stale-code&state=wrong-state", redirectURI))
	// Spoofed provider error without the flow's state.
	callbackGet(t, fmt.Sprintf("%s?error=access_denied", redirectURI))

	// The real callback still arrives and wins.
	callbackGet(t, redirectURI+"?code=auth-code-123&state="+state)

	select {
	case res := <-resultCh:
		if res.Err != nil {
			t.Fatalf("callback error: %v", res.Err)
		}
		if res.Code != "auth-code-123" {
			t.Errorf("Code = %q, want %q", res.Code, "auth-code-123")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

// TestStartCallbackServer_ProviderError verifies that a provider-reported
// error carrying the flow's state is delivered as an error result.
func TestStartCallbackServer_ProviderError(t *testing.T) {
	state := RandomState()
	resultCh, redirectURI, cleanup := StartCallbackServer("127.0.0.1:0", state, "test-server")
	defer cleanup()

	callbackGet(t, fmt.Sprintf("%s?error=access_denied&state=%s", redirectURI, state))

	select {
	case res := <-resultCh:
		if res.Err == nil {
			t.Fatal("expected authorization error, got nil")
		}
		if !strings.Contains(res.Err.Error(), "access_denied") {
			t.Errorf("Err = %v, want mention of access_denied", res.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

// FillAuthURL is the one place the session's placeholders are resolved, and it
// refuses a template it cannot finish: a browser sent a literal
// "{{redirect_uri}}" would come back to nothing, and the user would see a
// provider error page rather than the adapter's.
func TestFillAuthURL(t *testing.T) {
	const template = "https://as.example/authorize?client_id=x&redirect_uri={{redirect_uri}}&state={{state}}"

	filled, err := FillAuthURL(template, "http://127.0.0.1:8080/cb?x=1", "abc123")
	if err != nil {
		t.Fatalf("FillAuthURL: %v", err)
	}
	want := "https://as.example/authorize?client_id=x&redirect_uri=" +
		url.QueryEscape("http://127.0.0.1:8080/cb?x=1") + "&state=abc123"
	if filled != want {
		t.Errorf("FillAuthURL() = %q, want %q", filled, want)
	}

	// A third placeholder the session learned to send is not one this build
	// knows how to fill; say so instead of opening the URL.
	_, err = FillAuthURL(template+"&code_challenge={{code_challenge}}", "http://127.0.0.1:8080/cb", "abc")
	if err == nil {
		t.Fatal("FillAuthURL accepted a template with an unknown placeholder")
	}
	if !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("error = %v, want it to name the leftovers", err)
	}
}
