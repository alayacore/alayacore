package llm

// Retry policy for transient provider failures.
//
// A step's request is issued synchronously inside Provider.StreamMessages, so a
// failure there means nothing has streamed yet: no content, no tool call, no
// history ID. Re-sending the request is therefore idempotent — it cannot
// duplicate content, re-run a tool whose side effects already happened, or
// pollute history. That is the ONLY place the agent retries. An error that
// arrives after the stream has started (e.g. an Anthropic `error` event) may
// follow content the user already watched stream in, so it is surfaced as-is
// rather than retried.
//
// The agent drives the loop (see sendWithRetry in agent.go) because it is the
// layer that can tell the adapter what is happening, through
// StreamCallbacks.OnRetry. The provider contributes a typed *APIError for a
// non-200 response, and for a transport failure the shape of the error
// net/http returned; either way the decision is structural, never a string
// match.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// APIError is a provider response that was not 200 OK. It carries the HTTP
// status and the parsed Retry-After hint so a caller can decide structurally
// whether the request may be re-sent (see IsRetryable). Body is a bounded
// excerpt, so a proxy returning an HTML page cannot flood the context.
type APIError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration // from the Retry-After header; 0 = not given
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Body)
}

// RetryNotice describes one re-send that is about to happen. It exists so the
// adapter can show that a rate limit (or another transient failure) is being
// handled, instead of the task looking like a hang. It is informational: the
// task has not failed, and if every retry is exhausted the ordinary error path
// reports the final failure.
type RetryNotice struct {
	Retry      int           // 1-based retry number — the attempt about to be made
	MaxRetries int           // total retries allowed for this step
	Wait       time.Duration // backoff before this retry
	Reason     string        // short, human-readable
}

// Retry policy. Package-level vars rather than consts so tests can shrink the
// delays; production values keep the total wait small and bounded.
var (
	maxRetries     = 3
	retryBaseDelay = time.Second
	retryMaxDelay  = 30 * time.Second
)

// IsRetryable reports whether err is a transient provider failure that may be
// safely re-sent. Only failures raised before anything streamed reach this
// function, so re-sending cannot duplicate content.
//
// The status codes are listed explicitly rather than ">= 500": 501 (Not
// Implemented) and 505 (HTTP Version Not Supported) are permanent, so retrying
// them only delays the error. 529 is Anthropic's non-standard "overloaded".
//
// A connection that failed before any response (connectionFailed, below, names
// the shapes) is retried like a 5xx. That failure is ambiguous: the provider
// may have generated (and billed) the answer before the response was lost. The
// ambiguity is still not a reason to withhold the retry, because the user's
// only alternative is `:continue`, which re-sends the identical request — the
// same tokens, plus a human. See docs/error-handling.md, "Automatic Retry of
// Transient Failures".
func IsRetryable(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		// The caller gave up or its deadline passed; re-sending would fight
		// the cancellation instead of respecting it.
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusRequestTimeout, // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout,      // 504
			529:                            // Anthropic "overloaded" (non-standard)
			return true
		}
		return false
	}
	return connectionFailed(err)
}

// connectionFailed reports whether err is a transport-level failure that
// arrived before any HTTP response: a timeout, a reset, a broken pipe, a socket
// closed under the write, or a clean EOF where a response should have been.
//
// http.Client wraps all of them in *url.Error, which satisfies net.Error — but
// only a timeout reports Timeout() == true, so a reset or an EOF needs the
// explicit test. A refusal (ECONNREFUSED) is deliberately not here: a retry
// meets the same refusal moments later, and "nothing is listening" is worth
// reporting without a backoff in front of it.
//
// net.ErrClosed ("use of closed network connection") is here because in this
// position it is not "we closed it on purpose". A request whose body is still
// going out when the peer stops talking reports that one event in whichever
// costume won the race: the peer's errno (ECONNRESET/EPIPE), a bare EOF, or —
// when net/http's read half notices the peer first and closes the connection
// underneath the writer — this local close. Which one you see follows the size
// of the body, because that is how long the write stays in flight: a body that
// fits in the socket's buffers is already written when the read half reports
// the missing response as EOF, while the whole-conversation body of a summarize
// is where `readfrom ...: write ...: use of closed network connection` was
// reported. One event, one retry. The other closer on this path is the
// request's own context ending, which net/http reports as the cancellation
// (`context canceled`, ahead of the write error) — and sendWithRetry's ctx
// check covers the race in which the peer's departure is reported instead.
func connectionFailed(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// retryDelay returns how long to wait before the given retry (1-based). A
// server-supplied Retry-After wins; otherwise exponential backoff. Both are
// capped by retryMaxDelay so a hostile or broken value cannot stall the
// process. The doubling stops at the cap, so no shift can overflow.
func retryDelay(retry int, err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return min(apiErr.RetryAfter, retryMaxDelay)
	}
	d := retryBaseDelay
	for i := 1; i < retry && d < retryMaxDelay; i++ {
		d *= 2
	}
	return min(d, retryMaxDelay)
}

// retryReason renders a short, user-facing reason for a retry notice.
func retryReason(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return "provider rate limited the request (status 429)"
		case 529:
			return "provider is overloaded (status 529)"
		default:
			return fmt.Sprintf("provider returned a transient error (status %d)", apiErr.StatusCode)
		}
	}
	// One message for every transport-level failure before the response: they
	// are the same event to the user, and the retry they are about to see is
	// the same retry.
	return "provider connection failed"
}

// sleepCtx waits for d, returning early (with the context error) if ctx is
// canceled — so :cancel interrupts a backoff immediately.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
