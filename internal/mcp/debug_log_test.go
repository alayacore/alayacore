package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// failingTokenStore is a TokenStore whose read and write both fail, the shape of
// a cache directory whose permissions are wrong.
type failingTokenStore struct{ err error }

func (s failingTokenStore) LoadToken(string) (*auth.Token, error) { return nil, s.err }
func (s failingTokenStore) SaveToken(string, *auth.Token) error   { return s.err }
func (s failingTokenStore) DeleteToken(string) error              { return s.err }

// A mock transport that reports failure on Send: what the client must record
// without failing.
func transportWithFailingSend(t *testing.T, responses ...json.RawMessage) *mockTransport {
	t.Helper()
	mt := newMockTransport(responses)
	mt.sendErr = errors.New("broken pipe")
	return mt
}

// initializeResponse builds the one canned reply a handshake needs.
func initializeResponse(t *testing.T, version string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result: mustMarshal(InitializeResult{
			ProtocolVersion: version,
			ServerInfo:      ImplementationInfo{Name: "test-server", Version: "1.0.0"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The handshake's last step is a notification, and its failure is not fatal: a
// server that answered the initialize and then closed is still a server whose
// tools/list will report the real break. It must not be silent either — that is
// the whole reason this log line exists.
func TestHandshakeRecordsUndeliveredInitializedNotification(t *testing.T) {
	const version = "2025-06-18"
	client := NewClient(ServerConfig{Name: "test", ProtoVersion: version})
	client.adapter = NewAdapterV20250618()
	mt := transportWithFailingSend(t, initializeResponse(t, version))
	client.storeTransport(mt)

	if _, err := client.adapter.Handshake(context.Background(), client); err != nil {
		t.Fatalf("Handshake: %v (a failed notification must not fail the handshake)", err)
	}

	log := mt.debugLog()
	if !strings.Contains(log, "initialized notification not delivered") {
		t.Errorf("debug log = %q, want the undelivered notification recorded", log)
	}
	if !strings.Contains(log, "broken pipe") {
		t.Errorf("debug log = %q, want the transport's own error in it", log)
	}
}

// An unreadable token cache is treated as "no token", which is what sends the
// user through authorization again — every start, if the file stays unreadable.
// The fallback stays; the reason is recorded at the point that decides it.
func TestNeedsPersistedAuthRecordsUnreadableTokenCache(t *testing.T) {
	storeErr := errors.New("read token file ~/.alayacore/mcp-cache/oauth.conf: permission denied")
	client := NewClient(ServerConfig{
		Name:       "oauth",
		Auth:       &AuthConfig{Type: AuthTypeAuthorizationCode},
		TokenStore: failingTokenStore{err: storeErr},
	})
	mt := newMockTransport(nil)
	client.storeTransport(mt)

	if !client.needsPersistedAuth() {
		t.Error("needsPersistedAuth = false, want true (the fallback is to authorize)")
	}

	log := mt.debugLog()
	if !strings.Contains(log, "token cache unreadable") {
		t.Errorf("debug log = %q, want the unreadable cache recorded", log)
	}
	if !strings.Contains(log, "permission denied") {
		t.Errorf("debug log = %q, want the store's own error in it", log)
	}
}

// A token that cannot be persisted leaves this run authorized and the next one
// asking again; the flow must continue, and the failure must be recorded.
func TestOAuthTokenPersistFailureIsRecorded(t *testing.T) {
	storeErr := errors.New("write token file ~/.alayacore/mcp-cache/oauth.conf: permission denied")
	client := NewClient(ServerConfig{
		Name:       "oauth",
		Auth:       &AuthConfig{Type: AuthTypeAuthorizationCode},
		TokenStore: failingTokenStore{err: storeErr},
	})
	mt := newMockTransport(nil)
	client.storeTransport(mt)

	// The connect that follows cannot succeed without a real transport, and
	// that is the point: the save failure must not be what stops it.
	err := client.connectWithOAuthToken(context.Background(), &auth.Token{AccessToken: "fresh"})
	if err == nil {
		t.Fatal("connectWithOAuthToken = nil, want the connect step's own error")
	}
	if !strings.Contains(err.Error(), "connect after auth") {
		t.Errorf("error = %v, want the flow to have reached the connect step", err)
	}

	log := mt.debugLog()
	if !strings.Contains(log, "could not persist the token") {
		t.Errorf("debug log = %q, want the unpersisted token recorded", log)
	}
	if !strings.Contains(log, "permission denied") {
		t.Errorf("debug log = %q, want the store's own error in it", log)
	}
}

// A reply to the server's own request (ping) that cannot be written leaves an
// unanswered request behind. It is recorded by the dispatcher, which is the one
// place both transports pass through, so the reader goroutine needs no caller of
// its own to report to.
func TestStdioServerReplyFailureIsRecorded(t *testing.T) {
	r, w := io.Pipe()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	log := &writeCloser{}
	tr := &StdioTransport{stdin: w, debugWriter: log}

	request := []byte(`{"jsonrpc":"2.0","id":"srv-1","method":"ping"}`)
	pending := make(map[requestID]chan<- jsonrpcResponse)
	if err := parseAndDispatchJSONRPC(context.Background(), request, pending, &mu, log, tr.handleServerRequest, nil); err != nil {
		t.Fatalf("parseAndDispatchJSONRPC: %v", err)
	}

	got := log.String()
	if !strings.Contains(got, `reply to server request "ping" failed`) {
		t.Errorf("debug log = %q, want the failed reply recorded", got)
	}
	if !strings.Contains(got, "closed pipe") {
		t.Errorf("debug log = %q, want the write error in it", got)
	}
}

// writeCloser is a debug log that a test can read back.
type writeCloser struct{ bytes.Buffer }

func (w *writeCloser) Close() error { return nil }
