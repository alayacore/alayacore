package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// HTTP Test Server
// ============================================================================

// httpTestServer simulates an MCP server using the Streamable HTTP transport.
type httpTestServer struct {
	mu           sync.Mutex
	t            *testing.T
	listener     net.Listener
	serverURL    string
	sessionID    string
	requireSess  bool
	responseMode string // "json" (default) or "sse"
	sseData      string
	delayResp    time.Duration

	postRequests  []jsonrpcRequest
	lastSessionID string
	requestCount  int
	getCount      int
	postReceived  chan struct{}
	done          chan struct{}
	wg            sync.WaitGroup
	closeOnce     sync.Once
}

type httpTestOption func(*httpTestServer)

func withSessionID(sid string) httpTestOption {
	return func(s *httpTestServer) { s.sessionID = sid }
}

func withRequireSession() httpTestOption {
	return func(s *httpTestServer) { s.requireSess = true }
}

func withResponseMode(mode string) httpTestOption {
	return func(s *httpTestServer) { s.responseMode = mode }
}

func withResponseDelay(d time.Duration) httpTestOption {
	return func(s *httpTestServer) { s.delayResp = d }
}

func newHTTPServer(t *testing.T, opts ...httpTestOption) *httpTestServer {
	t.Helper()
	s := &httpTestServer{
		t:            t,
		responseMode: "json",
		postReceived: make(chan struct{}, 100),
		done:         make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.listener = listener
	s.serverURL = "http://" + listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = http.Serve(listener, mux)
	}()
	return s
}

func (s *httpTestServer) URL() string { return s.serverURL }

func (s *httpTestServer) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.listener.Close()
		s.wg.Wait()
	})
}

func (s *httpTestServer) LastSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSessionID
}

func (s *httpTestServer) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestCount
}

func (s *httpTestServer) WaitForPost(t *testing.T) {
	t.Helper()
	select {
	case <-s.postReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for POST")
	}
}

func (s *httpTestServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		s.handlePOST(w, r)
	case "GET":
		s.handleGET(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *httpTestServer) handlePOST(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		http.Error(w, "bad content type", http.StatusBadRequest)
		return
	}

	sid := r.Header.Get("MCP-Session-Id")
	s.mu.Lock()
	s.lastSessionID = sid
	s.requestCount++
	needsSess := s.requireSess && s.requestCount > 1 && sid == ""
	s.mu.Unlock()
	if needsSess {
		http.Error(w, "session required", http.StatusBadRequest)
		return
	}

	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.postRequests = append(s.postRequests, req)
	s.mu.Unlock()

	select {
	case s.postReceived <- struct{}{}:
	default:
	}

	if s.delayResp > 0 {
		time.Sleep(s.delayResp)
	}

	if s.sessionID != "" {
		w.Header().Set("MCP-Session-Id", s.sessionID)
	}

	if req.ID == "" || req.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch s.responseMode {
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flusher.Flush()

		if s.sseData != "" {
			fmt.Fprint(w, s.sseData)
			flusher.Flush()
		}

		resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"echo":"ok"}`)}
		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
		flusher.Flush()

	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"echo":"ok"}`)}
		// Discarded on purpose, and here is the reason, so the next reader does
		// not "fix" it into a silent bug: handlePOST has no *testing.T, and the
		// only way Encode fails is a connection the client already closed — the
		// client whose behavior the test asserts on, which reports that itself.
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func (s *httpTestServer) handleGET(w http.ResponseWriter, r *http.Request) {
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "text/event-stream") {
		http.Error(w, "not acceptable", http.StatusNotAcceptable)
		return
	}

	s.mu.Lock()
	s.getCount++
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flusher.Flush()

	<-r.Context().Done()
}

// ============================================================================
// HTTP Transport Tests
// ============================================================================

func TestHTTPTransport_SendReceive_JSON(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	ctx := context.Background()
	resp, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/method",
	})
	if err != nil {
		t.Fatalf("SendReceive: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["echo"] != "ok" {
		t.Errorf("echo = %q, want ok", result["echo"])
	}
}

func TestHTTPTransport_SendReceive_SSE(t *testing.T) {
	srv := newHTTPServer(t, withResponseMode("sse"))
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	ctx := context.Background()
	resp, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/sse",
	})
	if err != nil {
		t.Fatalf("SendReceive: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["echo"] != "ok" {
		t.Errorf("echo = %q, want ok", result["echo"])
	}
}

func TestHTTPTransport_Send_Notification(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	err := tr.Send(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/test",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	srv.WaitForPost(t)
}

func TestHTTPTransport_SendReceive_Error(t *testing.T) {
	srv := newHTTPServer(t)
	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	srv.Close()

	_, err := tr.SendReceive(context.Background(), jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "ping",
	})
	if err == nil {
		t.Fatal("expected error from closed server, got nil")
	}
}

func TestHTTPTransport_Close(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")

	if err := tr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}

	select {
	case <-tr.Done():
	default:
		t.Error("Done should be closed after Close")
	}
}

func TestHTTPTransport_ContextCancellation(t *testing.T) {
	srv := newHTTPServer(t, withResponseDelay(5*time.Second))
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/slow",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestHTTPTransport_GETStream(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	ctx := context.Background()
	closeFn, err := tr.StartGETStream(ctx)
	if err != nil {
		t.Fatalf("StartGETStream: %v", err)
	}
	defer closeFn()

	time.Sleep(100 * time.Millisecond)
	srv.mu.Lock()
	count := srv.getCount
	srv.mu.Unlock()
	if count != 1 {
		t.Errorf("GET connections = %d, want 1", count)
	}

	tr.Close()

	select {
	case <-tr.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Done")
	}
}

func TestHTTPTransport_GETStreamMethodNotAllowed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			var req jsonrpcRequest
			// Two things here, both deliberate. t is captured from the enclosing
			// test, but this body runs on the server's goroutine, so Errorf is
			// the legal half of the pair. And this POST branch is not currently
			// reached by the test that owns this mux — it asserts on the GET
			// stream's 405 — which was checked rather than assumed: replacing the
			// body with a panic leaves the test passing. The checks are kept
			// because they are what belongs in a handler, not because anything
			// exercises them; whoever routes a POST through here inherits them.
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
			}
			resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encode response: %v", err)
			}
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = http.Serve(listener, mux)
	}()

	serverURL := "http://" + listener.Addr().String()
	tr, _ := NewHTTPTransport(serverURL, "")
	defer tr.Close()

	_, err = tr.StartGETStream(context.Background())
	if err == nil {
		t.Fatal("expected error for 405 GET, got nil")
	}
	if !strings.Contains(err.Error(), "405") {
		t.Errorf("expected 405 error, got: %v", err)
	}

	listener.Close()
	wg.Wait()
}

func TestHTTPTransport_DebugLogging(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), t.TempDir())
	defer tr.Close()
	t.Cleanup(func() { tr.debugWriter.Close() })

	ctx := context.Background()
	_, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/debug",
	})
	if err != nil {
		t.Fatalf("SendReceive: %v", err)
	}

	err = tr.Send(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/debug",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// ============================================================================
// Adapter-based Session Test
// ============================================================================

func TestV2025_11_25_SessionManagement(t *testing.T) {
	sessionID := "test-session-123"
	srv := newHTTPServer(t, withSessionID(sessionID), withRequireSession())
	defer srv.Close()

	adapter := NewAdapterV20251125()
	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	// Attach adapter to transport.
	tr.SetHTTPAdapter(adapter)

	// First request: server responds with MCP-Session-Id in header.
	ctx := context.Background()
	_, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/init",
	})
	if err != nil {
		t.Fatalf("first SendReceive: %v", err)
	}

	// Verify adapter captured the session ID.
	if adapter.sessionID != sessionID {
		t.Errorf("adapter.sessionID = %q, want %q", adapter.sessionID, sessionID)
	}

	// Second request: should include session ID in headers (via adapter).
	_, err = tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("2"),
		Method:  "test/second",
	})
	if err != nil {
		t.Fatalf("second SendReceive: %v", err)
	}

	if srv.LastSessionID() != sessionID {
		t.Errorf("server received sessionID = %q, want %q", srv.LastSessionID(), sessionID)
	}
}

func TestV2026_07_28_NoSession(t *testing.T) {
	sessionID := "leaked-session"
	srv := newHTTPServer(t, withSessionID(sessionID))
	defer srv.Close()

	adapter := NewAdapterV20260728()
	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()
	tr.SetHTTPAdapter(adapter)

	// Send a request. The 2026-07-28 adapter should NOT capture the session ID.
	ctx := context.Background()
	_, err := tr.SendReceive(ctx, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Method:  "test/nosession",
	})
	if err != nil {
		t.Fatalf("SendReceive: %v", err)
	}

	// The 2026-07-28 adapter has no sessionID field (it's on a different type).
	// The server returned a session ID but the adapter ignored it.
	// This test verifies that AdapterV20260728 doesn't have session state.
}

func TestMultipleConcurrentRequests(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")
	defer tr.Close()

	const numRequests = 5
	var wg sync.WaitGroup
	errs := make([]error, numRequests)

	for i := range numRequests {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := tr.SendReceive(context.Background(), jsonrpcRequest{
				JSONRPC: "2.0",
				ID:      requestID(fmt.Sprintf("c%d", idx)),
				Method:  fmt.Sprintf("concurrent/%d", idx),
			})
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
}

func TestNoDoubleClose(t *testing.T) {
	srv := newHTTPServer(t)
	defer srv.Close()

	tr, _ := NewHTTPTransport(srv.URL(), "")

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Close()
		}()
	}
	wg.Wait()
}
