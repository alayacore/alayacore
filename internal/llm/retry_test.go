package llm

import (
	"context"
	"errors"
	"fmt"
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
		{"529 overloaded", &APIError{StatusCode: 529}, true},
		{"501 permanent", &APIError{StatusCode: 501}, false},
		{"505 permanent", &APIError{StatusCode: 505}, false},
		{"400 permanent", &APIError{StatusCode: 400}, false},
		{"401 permanent", &APIError{StatusCode: 401}, false},
		{"403 permanent", &APIError{StatusCode: 403}, false},
		{"canceled (wrapped)", fmt.Errorf("provider stream failed: %w", context.Canceled), false},
		{"deadline", context.DeadlineExceeded, false},
		{"net timeout", timeoutErr{}, true},
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
