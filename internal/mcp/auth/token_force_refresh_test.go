package auth

// ForceRefresh is the answer to a 401, and a 401 is the one thing the local
// clock cannot see: ExpiresAt still says the token is good, so nothing in
// Token() ever asks for another. These tests pin the two outcomes that branch
// exists for — a new credential, or a discarded one and an error that says so.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestForceRefreshIgnoresTheLocalClock: the whole point. The cached token is
// "valid" by ExpiresAt, so Token() would hand it back; ForceRefresh must go to
// the authorization server anyway and come back with a different one.
func TestForceRefreshIgnoresTheLocalClock(t *testing.T) {
	var refreshCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "fresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "fresh-refresh",
		})
	}))
	defer ts.Close()

	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// A token the server would accept by the clock and has already revoked in
	// reality — that is the state this test starts from.
	stale := &Token{
		AccessToken:  "revoked-token",
		TokenType:    "Bearer",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := store.SaveToken("srv", stale); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	pp := NewPersistentTokenProvider(nil, store, "srv", &RefreshConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "client",
		ClientSecret:  "secret",
	})

	if err := pp.ForceRefresh(context.Background()); err != nil {
		t.Fatalf("ForceRefresh() error = %v", err)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Errorf("refresh endpoint called %d times, want 1", n)
	}

	// The next Token() is the new one, from memory (no second refresh).
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "fresh-token" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "fresh-token")
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Errorf("Token() after a successful ForceRefresh refreshed again (%d calls)", n)
	}

	// And it is persisted, so a restart starts from the good one.
	loaded, err := store.LoadToken("srv")
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if loaded == nil || loaded.AccessToken != "fresh-token" {
		t.Errorf("persisted token = %+v, want fresh-token", loaded)
	}
}

// TestForceRefresh_RefusedDiscardsTheCredential: a refresh the authorization
// server refuses means the credential is dead, and the discard is the point —
// leaving it on disk is what makes the next start believe it has nothing to do
// (needsPersistedAuth reads a stored token as "no authorization needed"), so the
// user would meet the same 401 after a restart with no way to be asked again.
func TestForceRefresh_RefusedDiscardsTheCredential(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest) // invalid_grant
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer ts.Close()

	dir := t.TempDir()
	store := NewFileTokenStore(dir)
	if err := store.SaveToken("srv", &Token{
		AccessToken:  "revoked-token",
		RefreshToken: "revoked-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	pp := NewPersistentTokenProvider(nil, store, "srv", &RefreshConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "client",
		ClientSecret:  "secret",
	})

	err := pp.ForceRefresh(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("ForceRefresh() error = %v, want it to wrap ErrReauthorizationRequired", err)
	}
	// The message is what a user reads in the tool error; it has to say what to
	// do, since nothing else in the program will.
	if !strings.Contains(err.Error(), "re-authorize") {
		t.Errorf("error %q does not name the action (%q)", err, "re-authorize")
	}

	loaded, loadErr := store.LoadToken("srv")
	if loadErr != nil {
		t.Fatalf("LoadToken: %v", loadErr)
	}
	if loaded != nil {
		t.Errorf("the refused credential is still on disk (%+v) — the next start would trust it", loaded)
	}
	if tok := pp.getCached(); tok != nil {
		t.Errorf("the refused credential is still cached (%+v)", tok)
	}
}

// TestForceRefresh_WithoutRefreshGrantIsNotRetried: a token issued without a
// refresh token (or a provider built without refresh config) cannot produce a
// different credential, so the answer is the same error, not a second attempt.
func TestForceRefresh_WithoutRefreshGrantIsNotRetried(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)
	if err := store.SaveToken("srv", &Token{
		AccessToken: "no-refresh-here",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	pp := NewPersistentTokenProvider(nil, store, "srv", nil) // no refresh config

	err := pp.ForceRefresh(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("ForceRefresh() error = %v, want ErrReauthorizationRequired", err)
	}
	if loaded, _ := store.LoadToken("srv"); loaded != nil {
		t.Errorf("the unusable credential is still on disk (%+v)", loaded)
	}
}

// TestForceRefresh_TransientFailureKeepsTheCredential is the other half of the
// distinction the refusal type exists for: a token endpoint that never answered
// (503 here, a timeout or a dropped connection equally) has said nothing about
// the credential, so nothing is discarded and the user is not sent through
// authorization because a server was briefly unavailable.
func TestForceRefresh_TransientFailureKeepsTheCredential(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	dir := t.TempDir()
	store := NewFileTokenStore(dir)
	if err := store.SaveToken("srv", &Token{
		AccessToken:  "unrefused-token",
		RefreshToken: "still-good-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	pp := NewPersistentTokenProvider(nil, store, "srv", &RefreshConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "client",
		ClientSecret:  "secret",
	})

	err := pp.ForceRefresh(context.Background())
	if err == nil {
		t.Fatal("ForceRefresh() succeeded against a 503")
	}
	if errors.Is(err, ErrReauthorizationRequired) {
		t.Errorf("ForceRefresh() error = %v, want a transport error, not a demand to re-authorize", err)
	}
	loaded, loadErr := store.LoadToken("srv")
	if loadErr != nil {
		t.Fatalf("LoadToken: %v", loadErr)
	}
	if loaded == nil || loaded.AccessToken != "unrefused-token" {
		t.Errorf("persisted token = %+v, want it kept — the server never refused it", loaded)
	}
}
