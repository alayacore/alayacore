package providers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"with spaces", "  12 ", 12 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"garbage", "soon", 0},
		{"http-date in the past", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	t.Run("http-date in the future", func(t *testing.T) {
		in := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
		if got := parseRetryAfter(in); got <= 0 || got > 10*time.Second {
			t.Errorf("parseRetryAfter(%q) = %v, want a positive duration <= 10s", in, got)
		}
	})
}

// A provider that takes the request and then dies — the `Post "...": EOF` a
// local server leaves behind when it is OOM-killed mid-request — must reach
// llm.IsRetryable as a transient failure. It runs over a real connection
// because the classification depends on what net/http returns, not on an error
// a test invents.
func TestStreamMessagesConnectionClosedBeforeResponseIsRetryable(t *testing.T) {
	var hijacked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		hijacked.Store(true)
		_ = conn.Close() // not one response byte: the connection just ends
	}))
	defer srv.Close()

	p, err := NewOpenAI(BaseConfig{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.StreamMessages(context.Background(),
		[]llm.ContentPart{&llm.TextPart{
			Text:            "hi",
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
		}}, nil, "", "")

	if !hijacked.Load() {
		t.Fatal("the server never closed the connection, so the case did not run")
	}
	if err == nil {
		t.Fatal("StreamMessages error = nil, want a transport failure")
	}
	if !llm.IsRetryable(err) {
		t.Fatalf("IsRetryable(%v) = false, want true: a connection closed before the response is transient", err)
	}
}

// The same peer event as the test above, but with the request body still going
// out: the server stops talking (FIN) and never answers, so net/http's read half
// closes the connection underneath the writer. Which costume that arrives in — a
// bare EOF, or `readfrom ...: write ...: use of closed network connection` — is
// decided by how much of the body the kernel had already buffered, i.e. by the
// size of the request, so it is not a contract this test may assert. The
// invariant is: whichever shape a peer that leaves mid-request produces, it is
// retryable. The second size is the one that reproduces the field report (a
// summarize's whole-conversation body); the first is the small-prompt shape that
// was already retried.
func TestStreamMessagesPeerGoneWhileSendingBodyIsRetryable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes int
	}{
		{"body fits in the socket buffer", 1 << 10},
		{"body still in flight", 4 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hold := make(chan struct{})
			var hijacked atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hj, ok := w.(http.Hijacker)
				if !ok {
					return
				}
				conn, _, err := hj.Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				hijacked.Store(true)
				// Stop talking, keep the socket open: the client's read side
				// sees EOF while its body is still on the way, and had this
				// been a Close the kernel would answer the unread body with
				// RST — a different error (ECONNRESET) that was already
				// retried.
				if cw, ok := conn.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				}
				<-hold
			}))
			defer srv.Close()
			// LIFO: the handler is released before srv.Close waits for it.
			defer close(hold)

			p, err := NewOpenAI(BaseConfig{APIKey: "k", BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}

			// A deadline so a regression fails loudly instead of hanging: a
			// cancellation is not retryable and would fail the assertion below.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			_, err = p.StreamMessages(ctx,
				[]llm.ContentPart{&llm.TextPart{
					Text:            strings.Repeat("x", tc.bytes),
					ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
				}}, nil, "", "")

			if !hijacked.Load() {
				t.Fatal("the server never hijacked the connection, so the case did not run")
			}
			if err == nil {
				t.Fatal("StreamMessages error = nil, want a transport failure")
			}
			t.Logf("shape: %v", err)
			if !llm.IsRetryable(err) {
				t.Fatalf("IsRetryable(%v) = false, want true: a peer that leaves mid-request is transient", err)
			}
		})
	}
}

// A provider that is not listening yet — a local model server mid-restart — is
// the case the next second's retry answers, so a refused connect is transient
// too. Real socket: the listener is closed before the request is made, so this
// is the kernel's refusal, not one a test invented.
func TestStreamMessagesConnectionRefusedIsRetryable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // from here on, nothing is listening there

	p, err := NewOpenAI(BaseConfig{APIKey: "k", BaseURL: "http://" + addr})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.StreamMessages(context.Background(),
		[]llm.ContentPart{&llm.TextPart{
			Text:            "hi",
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
		}}, nil, "", "")

	if err == nil {
		t.Fatal("StreamMessages error = nil, want a refused connect")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("error = %v, want a refused connect", err)
	}
	if !llm.IsRetryable(err) {
		t.Fatalf("IsRetryable(%v) = false, want true: nothing listening yet is transient", err)
	}
}

// A non-200 response must arrive as a typed *llm.APIError carrying the status
// and the Retry-After hint, so the agent can decide structurally whether to
// re-send.
func TestDoRequestReturnsTypedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()

	p, err := NewOpenAI(BaseConfig{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.StreamMessages(context.Background(),
		[]llm.ContentPart{&llm.TextPart{
			Text:            "hi",
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
		}}, nil, "", "")

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *llm.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusTooManyRequests)
	}
	if apiErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", apiErr.RetryAfter)
	}
	if !strings.Contains(apiErr.Body, "slow down") {
		t.Errorf("Body = %q, want it to contain the server message", apiErr.Body)
	}
}
