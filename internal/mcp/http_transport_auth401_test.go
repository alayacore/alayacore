package mcp

// The 401 branch, which is the only place a token is ever replaced. It used to
// re-send the request with whatever Token() returned — the same credential, for
// as long as the local clock called it valid — so a revoked token was refused
// twice and the retry bought nothing. These tests pin what it does now: a
// refresh, then one more request carrying the new credential; or, when the
// authorization server refuses, an error that says what the user must do.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// tokenSwapProvider is a TokenProvider that can also refresh, standing in for
// auth.PersistentTokenProvider: it hands out one token until ForceRefresh
// replaces it (or fails).
type tokenSwapProvider struct {
	mu         sync.Mutex
	token      string
	refreshErr error

	refreshCalls int32
}

func (p *tokenSwapProvider) Token(context.Context) (*auth.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &auth.Token{AccessToken: p.token, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (p *tokenSwapProvider) ForceRefresh(context.Context) error {
	atomic.AddInt32(&p.refreshCalls, 1)
	if p.refreshErr != nil {
		return p.refreshErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.token = "fresh-token"
	return nil
}

// authRecordingServer answers 401 to every credential but the fresh one, and
// remembers the Authorization header of each request it saw.
type authRecordingServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newAuthRecordingServer(t *testing.T) *authRecordingServer {
	t.Helper()
	s := &authRecordingServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Header.Get("Authorization"))
		s.mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *authRecordingServer) authorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// TestDoPOSTRefreshesOnceOn401: the retry carries a different credential,
// because the first one was refused. Two requests, one refresh.
func TestDoPOSTRefreshesOnceOn401(t *testing.T) {
	srv := newAuthRecordingServer(t)
	tr, err := NewHTTPTransport(srv.URL, "")
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	provider := &tokenSwapProvider{token: "revoked-token"}
	tr.SetAuthProvider(provider)

	resp, err := tr.doPOST(context.Background(), jsonrpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	if err != nil {
		t.Fatalf("doPOST() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — the retry did not use the refreshed token", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&provider.refreshCalls); n != 1 {
		t.Errorf("ForceRefresh called %d times, want 1", n)
	}
	want := []string{"Bearer revoked-token", "Bearer fresh-token"}
	got := srv.authorizations()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d carried %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestDoPOST_RefusedRefreshStops: a credential that cannot be renewed is not
// re-sent. The error names the action, and it wraps the sentinel so a caller can
// recognize it without matching prose.
func TestDoPOST_RefusedRefreshStops(t *testing.T) {
	srv := newAuthRecordingServer(t)
	tr, err := NewHTTPTransport(srv.URL, "")
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	provider := &tokenSwapProvider{token: "revoked-token", refreshErr: auth.ErrReauthorizationRequired}
	tr.SetAuthProvider(provider)

	resp, err := tr.doPOST(context.Background(), jsonrpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	if resp != nil {
		resp.Body.Close()
		t.Fatalf("doPOST() returned a response (%d) alongside the error", resp.StatusCode)
	}
	if !errors.Is(err, auth.ErrReauthorizationRequired) {
		t.Fatalf("doPOST() error = %v, want it to wrap ErrReauthorizationRequired", err)
	}
	if got := srv.authorizations(); len(got) != 1 {
		t.Errorf("requests = %v, want one — a refused refresh must not re-send the same token", got)
	}
}

// TestDoPOST_StaticTokenStillRetriesOnce: a provider with no refresh grant has
// nothing newer to send, so the branch keeps the plain retry it always had.
// Nothing new is claimed for it — the second attempt is all a transient refusal
// leaves room for.
func TestDoPOST_StaticTokenStillRetriesOnce(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tr, err := NewHTTPTransport(srv.URL, "")
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	tr.SetAuthProvider(auth.NewCached(&auth.StaticProvider{
		TokenValue: &auth.Token{AccessToken: "static", TokenType: "Bearer"},
	}))

	resp, err := tr.doPOST(context.Background(), jsonrpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	if err != nil {
		t.Fatalf("doPOST() error = %v", err)
	}
	defer resp.Body.Close()

	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Errorf("requests = %d, want 2 (one retry)", n)
	}
}
