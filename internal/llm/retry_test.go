package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"
)

// timeoutErr satisfies net.Error with Timeout() true, standing in for a
// transport read/connect deadline.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", &APIError{StatusCode: 429}, true},
		{"408", &APIError{StatusCode: 408}, true},
		{"500", &APIError{StatusCode: 500}, true},
		{"502", &APIError{StatusCode: 502}, true},
		{"503", &APIError{StatusCode: 503}, true},
		{"504", &APIError{StatusCode: 504}, true},
		{"425 too early", &APIError{StatusCode: 425}, true},
		{"529 overloaded", &APIError{StatusCode: 529}, true},
		// A provider behind Cloudflare gets these where 502/504 would be. The
		// TLS pair (525/526) is a misconfiguration no retry fixes, which is why
		// it is pinned beside them.
		{"520 cloudflare unknown", &APIError{StatusCode: 520}, true},
		{"521 origin down", &APIError{StatusCode: 521}, true},
		{"522 origin timed out", &APIError{StatusCode: 522}, true},
		{"523 origin unreachable", &APIError{StatusCode: 523}, true},
		{"524 timeout occurred", &APIError{StatusCode: 524}, true},
		{"525 ssl handshake failed", &APIError{StatusCode: 525}, false},
		{"526 invalid certificate", &APIError{StatusCode: 526}, false},
		{"501 permanent", &APIError{StatusCode: 501}, false},
		{"505 permanent", &APIError{StatusCode: 505}, false},
		{"400 permanent", &APIError{StatusCode: 400}, false},
		{"401 permanent", &APIError{StatusCode: 401}, false},
		{"403 permanent", &APIError{StatusCode: 403}, false},
		{"canceled (wrapped)", fmt.Errorf("provider stream failed: %w", context.Canceled), false},
		{"deadline", context.DeadlineExceeded, false},
		// A transport failure is retried by default, so the shapes below are
		// samples with field history rather than the set: the vocabulary for
		// "the answer is not here" is open, and "unlisted errno" pins that a
		// shape nobody wrote down is retried too.
		{"net timeout", &url.Error{Op: "Post", URL: "http://x/v1", Err: timeoutErr{}}, true},
		{"eof before response", &url.Error{Op: "Post", URL: "http://127.0.0.1:9999/v1/chat/completions", Err: io.EOF}, true},
		{"closed under the write", &url.Error{Op: "Post", URL: "http://192.168.3.234:9999/v1/chat/completions", Err: &net.OpError{Op: "readfrom", Net: "tcp", Err: &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}}}, true},
		{"nothing listening", &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}, true},
		{"unlisted errno", &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENOBUFS}}, true},
		// The closed list: a second attempt answers the same, so the budget is
		// not spent on it.
		{"untrusted certificate", &url.Error{Op: "Post", URL: "https://x/v1", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, false},
		{"not a tls server", &url.Error{Op: "Post", URL: "https://x/v1", Err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}}, false},
		{"https answered by plain http", &url.Error{Op: "Post", URL: "https://x/v1", Err: http.ErrSchemeMismatch}, false},
		{"no such host", &url.Error{Op: "Post", URL: "http://model.lan/v1", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "model.lan", IsNotFound: true}}}, false},
		// Never reached the transport, so no transport failure: a request that
		// could not be built is reported at once. http.Client wraps everything
		// it returns in *url.Error, which is what makes this structural.
		{"request not built", fmt.Errorf("failed to create request: %w", errors.New(`parse "http://[::1]: named port"`)), false},
		{"transport error the provider did not wrap", io.EOF, false},
		// Cancellation wins over the transport shape, so a request the caller
		// gave up on is never re-sent as a "connection failure".
		{"canceled inside url.Error", &url.Error{Op: "Post", URL: "http://x/v1", Err: context.Canceled}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryable(tc.err); got != tc.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The reason a retry notice carries is user-facing text that
// docs/error-handling.md quotes, so it is pinned here — including the fact
// that every transport-level failure shares one wording.
func TestRetryReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&APIError{StatusCode: 425}, "provider was not ready for the request (status 425)"},
		{&APIError{StatusCode: 429}, "provider rate limited the request (status 429)"},
		{&APIError{StatusCode: 521}, "provider's gateway could not reach the origin (status 521)"},
		{&APIError{StatusCode: 524}, "provider's gateway could not reach the origin (status 524)"},
		{&APIError{StatusCode: 529}, "provider is overloaded (status 529)"},
		{&APIError{StatusCode: 503}, "provider returned a transient error (status 503)"},
		{&url.Error{Op: "Post", URL: "http://x/v1", Err: io.EOF}, "provider connection failed"},
		{timeoutErr{}, "provider connection failed"},
	}
	for _, tc := range cases {
		if got := retryReason(tc.err); got != tc.want {
			t.Errorf("retryReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	old := retryBaseDelay
	retryBaseDelay = time.Second
	t.Cleanup(func() { retryBaseDelay = old })

	// Exponential when the server gave no Retry-After.
	for _, tc := range []struct {
		retry int
		want  time.Duration
	}{{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}} {
		if got := retryDelay(tc.retry, &APIError{StatusCode: 429}); got != tc.want {
			t.Errorf("retry %d backoff = %v, want %v", tc.retry, got, tc.want)
		}
	}

	// Retry-After wins over backoff.
	withHeader := &APIError{StatusCode: 429, RetryAfter: 7 * time.Second}
	if got := retryDelay(1, withHeader); got != 7*time.Second {
		t.Errorf("Retry-After backoff = %v, want 7s", got)
	}

	// A hostile/huge Retry-After is capped.
	if got := retryDelay(1, &APIError{StatusCode: 503, RetryAfter: time.Hour}); got != retryMaxDelay {
		t.Errorf("huge Retry-After = %v, want cap %v", got, retryMaxDelay)
	}

	// Deep backoff is capped too (no overflow).
	if got := retryDelay(40, &APIError{StatusCode: 500}); got != retryMaxDelay {
		t.Errorf("deep backoff = %v, want cap %v", got, retryMaxDelay)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Fatalf("sleepCtx(0) = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepCtx(canceled) = %v, want context.Canceled", err)
	}
}
