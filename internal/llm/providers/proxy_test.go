package providers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestNewHTTPClientWithProxy_HTTP(t *testing.T) {
	client, err := NewHTTPClientWithProxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.Proxy == nil {
		t.Error("expected Proxy func to be set for HTTP proxy")
	}
}

func TestNewHTTPClientWithProxy_SOCKS5(t *testing.T) {
	client, err := NewHTTPClientWithProxy("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.DialContext == nil {
		t.Error("expected DialContext to be set for SOCKS5 proxy")
	}
}

func TestNewHTTPClientWithProxy_SOCKS5WithAuth(t *testing.T) {
	client, err := NewHTTPClientWithProxy("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}
}

func TestNewHTTPClientWithProxy_InvalidURL(t *testing.T) {
	_, err := NewHTTPClientWithProxy("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL, got nil")
	}
}

func TestNewHTTPClientWithProxy_EmptyURL(t *testing.T) {
	client, err := NewHTTPClientWithProxy("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
	}
}

// A proxy says how to reach the endpoint and nothing else, so the client it
// builds must carry everything the no-proxy path carries. The comparison is
// against http.DefaultTransport rather than against numbers, because the claim
// is "unchanged", not "10 seconds" — and a fresh http.Transport has none of
// these, which is the state this pins: an endpoint that accepts the connection
// and goes quiet used to hang here forever instead of failing in 30s or 10s.
func TestNewHTTPClientWithProxy_KeepsTheDefaultTransportRails(t *testing.T) {
	want := http.DefaultTransport.(*http.Transport)

	for _, tc := range []struct {
		name     string
		proxyURL string
		socks    bool
	}{
		{"http proxy", "http://127.0.0.1:7890", false},
		{"https proxy", "https://127.0.0.1:7890", false},
		{"socks5 proxy", "socks5://127.0.0.1:1080", true},
		{"socks5h proxy", "socks5h://127.0.0.1:1080", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewHTTPClientWithProxy(tc.proxyURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("expected *http.Transport, got %T", client.Transport)
			}

			// The one field the proxy owns: a SOCKS5 proxy is the dialer, so the
			// environment's proxy must not chain behind it; an http(s) proxy is
			// the URL, which replaces whatever the environment said.
			if tc.socks && got.Proxy != nil {
				t.Error("Proxy is still set for a SOCKS5 proxy: the environment's proxy would chain behind it")
			}
			if !tc.socks && got.Proxy == nil {
				t.Error("Proxy = nil for an http(s) proxy")
			}

			if got.DialContext == nil {
				t.Error("DialContext = nil: the connect deadline DefaultTransport carries is gone")
			}
			if got.TLSHandshakeTimeout != want.TLSHandshakeTimeout {
				t.Errorf("TLSHandshakeTimeout = %v, want %v", got.TLSHandshakeTimeout, want.TLSHandshakeTimeout)
			}
			if got.IdleConnTimeout != want.IdleConnTimeout {
				t.Errorf("IdleConnTimeout = %v, want %v", got.IdleConnTimeout, want.IdleConnTimeout)
			}
			if got.ExpectContinueTimeout != want.ExpectContinueTimeout {
				t.Errorf("ExpectContinueTimeout = %v, want %v", got.ExpectContinueTimeout, want.ExpectContinueTimeout)
			}
			if got.MaxIdleConns != want.MaxIdleConns || got.MaxIdleConnsPerHost != want.MaxIdleConnsPerHost {
				t.Errorf("idle connection limits = %d/%d, want %d/%d",
					got.MaxIdleConns, got.MaxIdleConnsPerHost, want.MaxIdleConns, want.MaxIdleConnsPerHost)
			}
			if got.ForceAttemptHTTP2 != want.ForceAttemptHTTP2 {
				t.Errorf("ForceAttemptHTTP2 = %v, want %v", got.ForceAttemptHTTP2, want.ForceAttemptHTTP2)
			}
		})
	}
}

// The SOCKS5 branch is the one place the dial function is replaced, so it is the
// one place the request's context can be lost — and the handshake it must reach
// happens after the connection is up, which is exactly the part a silent proxy
// can hold open. A dialer without a context would leave this request waiting
// with nothing to interrupt it.
func TestNewHTTPClientWithProxy_SOCKS5DialHonorsTheContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// The proxy accepts and then says nothing, so the connect succeeds and the
	// SOCKS handshake cannot finish — the part a context-blind dialer would wait
	// out forever. The connection is held open for the duration and closed at the
	// end, so a repeated run does not leak it.
	var (
		mu   sync.Mutex
		conn net.Conn
	)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		conn = c
		mu.Unlock()
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	})

	client, err := NewHTTPClientWithProxy("socks5://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://192.168.3.233:9999/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}

	// The request is bounded twice over: by its context, and by this deadline,
	// so a dialer that ignored the context fails the test instead of hanging it.
	done := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want the context's deadline: the SOCKS handshake must be interruptible", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request never returned: the SOCKS handshake ignores the context")
	}
}
