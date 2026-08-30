package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/debug"
)

func TestNewHTTPClient(t *testing.T) {
	client, err := NewHTTPClient("", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClient returned nil")
		return
	}

	transport, ok := client.Transport.(*DebugTransport)
	if !ok {
		t.Fatalf("expected *DebugTransport, got %T", client.Transport)
	}

	if transport.Writer == nil {
		t.Error("expected non-nil Writer")
	} else {
		t.Cleanup(func() { transport.Writer.(io.Closer).Close() })
	}

	if transport.Transport == nil {
		t.Error("expected non-nil underlying Transport")
	}
}

func TestNewHTTPClientWithProxyAndDebug(t *testing.T) {
	client, err := NewHTTPClient("http://127.0.0.1:7890", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxyAndDebug returned nil")
		return
	}

	transport, ok := client.Transport.(*DebugTransport)
	if !ok {
		t.Fatalf("expected *DebugTransport, got %T", client.Transport)
	}

	if transport.Writer == nil {
		t.Error("expected non-nil Writer on debug transport")
	} else {
		t.Cleanup(func() { transport.Writer.(io.Closer).Close() })
	}

	innerTransport, ok := transport.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected inner *http.Transport, got %T", transport.Transport)
	}
	if innerTransport.Proxy == nil {
		t.Error("expected Proxy func to be set on inner transport")
	}
}

func TestNewHTTPClientWithProxyAndDebug_InvalidProxy(t *testing.T) {
	_, err := NewHTTPClient("://invalid", "")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL, got nil")
	}
}

func TestDebugTransport_RoundTrip_WithoutBody(t *testing.T) {
	var logBuf strings.Builder
	transport := &DebugTransport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.com/test", http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
		return
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
		return
	}
	resp.Body.Close()

	log := logBuf.String()
	if !strings.Contains(log, ">>> Request") {
		t.Error("expected request log header")
	}
	if !strings.Contains(log, "GET") {
		t.Error("expected GET method in log")
	}
	if !strings.Contains(log, "/test") {
		t.Error("expected /test path in log")
	}
}

func TestDebugTransport_RoundTrip_WithBody(t *testing.T) {
	var logBuf strings.Builder
	transport := &DebugTransport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	body := strings.NewReader(`{"key":"value"}`)
	req, err := http.NewRequestWithContext(context.Background(), "POST", "http://example.com/api", body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
		return
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
		return
	}
	resp.Body.Close()

	log := logBuf.String()
	if !strings.Contains(log, ">>> Request") {
		t.Error("expected request log header")
	}
	if !strings.Contains(log, `"key"`) {
		t.Error("expected body content in log")
	}
	if !strings.Contains(log, "<<< Response") {
		t.Error("expected response log header")
	}
}

func TestDebugTransport_RoundTrip_AuthorizationHeader(t *testing.T) {
	var logBuf strings.Builder
	transport := &DebugTransport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", "http://example.com/api", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-secret-key-12345")

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
		return
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
		return
	}
	resp.Body.Close()

	log := logBuf.String()
	if strings.Contains(log, "sk-secret-key-12345") {
		t.Error("Authorization header value should be redacted in log")
	}
	if !strings.Contains(log, "***") {
		t.Error("expected redacted Authorization header (***)")
	}
}

func TestDebugTransport_RoundTrip_RequestError(t *testing.T) {
	var logBuf strings.Builder
	transport := &DebugTransport{
		Transport: &mockRoundTripper{err: errMockFailed},
		Writer:    &logBuf,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.com/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error from failing round tripper")
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	log := logBuf.String()
	if !strings.Contains(log, "Request failed") {
		t.Error("expected failure log entry, got:", log)
	}
}

// mockRoundTripper implements http.RoundTripper for testing.
type mockRoundTripper struct {
	err error
}

var errMockFailed = errors.New("mock failure")

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func TestNewDebugWriter_NotNil(t *testing.T) {
	w, err := debug.NewDebugWriter(t.TempDir(), "alayacore-debug-api-test")
	if err != nil {
		t.Fatalf("NewDebugWriter: %v", err)
	}
	if w == nil {
		t.Fatal("NewDebugWriter returned nil")
	}
	w.Close()
}
