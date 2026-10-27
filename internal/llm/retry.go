package llm

// Retry policy for transient provider failures.
//
// An attempt is a step's request AND its stream, and it is re-sent only while it
// has delivered nothing: no event reached the adapter, no content or tool call
// was published, no history ID was minted. That is what makes re-sending
// idempotent — it cannot duplicate content, re-run a tool whose side effects
// already happened, or pollute history. Two failures meet the condition: a
// request that failed inside Provider.StreamMessages, and a stream that ended
// with an error before it recorded anything (streamBreakError). An error that arrives
// after an event has been delivered may follow content the user already watched
// stream in, so it is surfaced as-is rather than retried.
//
// The agent drives the loop (see sendWithRetry in agent.go) because it is the
// layer that can tell the adapter what is happening, through
// StreamCallbacks.OnRetry. A non-200 response contributes a typed *APIError;
// a transport failure is decided by whether the request reached the transport
// at all and whether a retry could change the answer. Either way the decision is
// structural, never a string match.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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

// streamBreakError marks a provider stream that ended with an error before it
// recorded anything: a 200 whose body stopped, a gateway that answered with
// nothing usable, an in-band error as the opening message. No block was opened,
// so nothing reached the adapter, no history ID was minted and nothing entered
// history — which is why the attempt may be re-sent exactly like a request that
// never got a response, and why the type carries that fact to the retry loop
// instead of the error having to look transient: a stream failure never arrives
// inside a *url.Error.
//
// It prints as the error it wraps, so a broken stream that outlives the retry
// budget is reported in the provider's own words.
type streamBreakError struct{ err error }

func (e *streamBreakError) Error() string { return e.err.Error() }
func (e *streamBreakError) Unwrap() error { return e.err }

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
// A status is an allowlist, and it has to be: it says what the server decided,
// and most decisions are final. 501 (Not Implemented) and 505 (HTTP Version Not
// Supported) are permanent, so retrying them only delays the error, and 400/
// 401/403/404/413 describe a request the server will reject identically next
// time. 529 is Anthropic's non-standard "overloaded".
//
// A transport failure carries no decision at all — only the absence of one — so
// it is decided by the one question that matters: could a second attempt get a
// different answer? There the default is yes (transportFailed, below). Retrying
// is not free of ambiguity — the provider may have generated, and billed, the
// answer before the response was lost — but withholding it does not buy those
// tokens back: the user's only alternative is `:continue`, which re-sends the
// identical request, the same tokens plus a human. See docs/error-handling.md,
// "Automatic Retry of Transient Failures".
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
			http.StatusTooEarly,            // 425 — the server asking to be asked again later
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout,      // 504
			529:                            // Anthropic "overloaded" (non-standard)
			return true
		case 520, 521, 522, 523, 524:
			// Cloudflare's origin-unreachable family, which a provider behind
			// that gateway returns where 502/504 would be if the origin could be
			// reached at all. 525/526 are a TLS misconfiguration and are absent
			// on purpose: no retry turns a bad certificate into a good one.
			return true
		}
		return false
	}
	return transportFailed(err)
}

// transportFailed reports whether a request that reached the transport came back
// without an answer: the connection died mid-request, or was never made.
//
// The default is to retry, deliberately. The vocabulary for "the answer is not
// here" is open — kernels, resolvers, proxies and gateways each have their own,
// and HTTP/2 another — and the last three fixes each added a shape the previous
// classification had not anticipated: io.EOF, then net.ErrClosed, then
// ECONNREFUSED. Enumerating that set is a treadmill. What a retry cannot
// change is closed and small, so that is the list that gets written down
// (unchangedByRetry), and everything else is retried.
//
// "Reached the transport" is structural: http.Client.Do wraps everything it
// returns in *url.Error, so an error without one never left this process — a
// body that could not be marshaled, a URL that could not be parsed — and is
// reported at once.
func transportFailed(err error) bool {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return false
	}
	return !unchangedByRetry(err)
}

// unchangedByRetry reports whether err is one of the failures a second attempt
// answers identically. Each is decided before a byte of the response could
// arrive: the certificate, the protocol, or the name.
func unchangedByRetry(err error) bool {
	// An https:// URL answered by a plain-HTTP server, or by something whose
	// first bytes are not a TLS record at all.
	if errors.Is(err, http.ErrSchemeMismatch) {
		return true
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	// A certificate that does not verify, whatever reason tls reports.
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		return true
	}
	// A name that does not resolve — as opposed to a resolver that could not
	// answer *this second*, which is retried like any other transport answer.
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
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

// reasonStreamEndedEarly is the notice wording for an attempt that recorded
// nothing before its stream broke. Like every notice reason it is user-facing
// text quoted in docs/error-handling.md, and pinned there by TestRetryReason.
const reasonStreamEndedEarly = "provider stream ended early"

// retryReason renders a short, user-facing reason for a retry notice.
func retryReason(err error) string {
	// A stream that died before delivering anything. Not "connection failed":
	// what ended it may be an error the provider sent as its opening message,
	// and no wording about the transport would be true of that.
	var broke *streamBreakError
	if errors.As(err, &broke) {
		return reasonStreamEndedEarly
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooEarly:
			return "provider was not ready for the request (status 425)"
		case http.StatusTooManyRequests:
			return "provider rate limited the request (status 429)"
		case 520, 521, 522, 523, 524:
			return fmt.Sprintf("provider's gateway could not reach the origin (status %d)", apiErr.StatusCode)
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
