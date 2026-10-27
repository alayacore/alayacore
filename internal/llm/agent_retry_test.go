package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/url"
	"testing"
	"time"
)

// retryScriptProvider fails the first len(failures) calls with the given errors,
// then succeeds with a minimal text answer. It records every call and every
// RetryNotice it was handed.
type retryScriptProvider struct {
	failures []error
	calls    int
	notices  []RetryNotice

	// cancelOnCall, when set, cancels the request's context at the start of
	// that 1-based call — a user giving up while the attempt is in flight.
	cancelOnCall int
	cancel       context.CancelFunc
}

func (p *retryScriptProvider) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	call := p.calls
	p.calls++
	if p.cancelOnCall > 0 && call+1 == p.cancelOnCall {
		p.cancel()
	}
	if call < len(p.failures) {
		return nil, p.failures[call]
	}
	return func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "hello", Key: "text"}, nil)
		yield(TextCompleteEvent{Key: "text"}, nil)
		yield(StepCompleteEvent{}, nil)
	}, nil
}

func (p *retryScriptProvider) SetReasoningLevel(_ int)                       {}
func (p *retryScriptProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

func (p *retryScriptProvider) onRetry(n RetryNotice) error {
	p.notices = append(p.notices, n)
	return nil
}

// fastRetries shrinks the retry delays for the duration of a test.
func fastRetries(t *testing.T) {
	t.Helper()
	base, max := retryBaseDelay, maxRetries
	retryBaseDelay = time.Millisecond
	maxRetries = 3
	t.Cleanup(func() { retryBaseDelay = base; maxRetries = max })
}

func userText() []ContentPart {
	return []ContentPart{&TextPart{Text: "hi", ContentPartMeta: ContentPartMeta{Role: RoleUser}}}
}

func TestAgentRetriesTransientThenSucceeds(t *testing.T) {
	fastRetries(t)
	p := &retryScriptProvider{failures: []error{
		&APIError{StatusCode: 429},
		&APIError{StatusCode: 503},
	}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
	if err != nil {
		t.Fatalf("Stream error = %v, want nil", err)
	}
	if p.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", p.calls)
	}
	if len(p.notices) != 2 {
		t.Fatalf("retry notices = %d, want 2", len(p.notices))
	}
	if p.notices[0].Retry != 1 || p.notices[1].Retry != 2 {
		t.Errorf("notice retry numbers = %d,%d, want 1,2", p.notices[0].Retry, p.notices[1].Retry)
	}
	if p.notices[0].MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", p.notices[0].MaxRetries)
	}
	if p.notices[0].Reason == "" || p.notices[0].Wait <= 0 {
		t.Errorf("notice = %+v, want non-empty Reason and positive Wait", p.notices[0])
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected a non-empty result after retries")
	}
}

func TestAgentRetriesExhausted(t *testing.T) {
	fastRetries(t)
	always := &APIError{StatusCode: 429}
	p := &retryScriptProvider{failures: []error{always, always, always, always, always, always}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Fatalf("error = %v, want *APIError 429", err)
	}
	if p.calls != maxRetries+1 {
		t.Fatalf("provider calls = %d, want %d (1 + %d retries)", p.calls, maxRetries+1, maxRetries)
	}
	if len(p.notices) != maxRetries {
		t.Fatalf("retry notices = %d, want %d", len(p.notices), maxRetries)
	}
}

func TestAgentDoesNotRetryPermanentError(t *testing.T) {
	fastRetries(t)
	p := &retryScriptProvider{failures: []error{&APIError{StatusCode: 401}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
	if err == nil {
		t.Fatal("expected an error")
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (permanent errors are not retried)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Fatalf("retry notices = %d, want 0", len(p.notices))
	}
}

// A connection that died before any response is retried like a 5xx. Nothing
// streamed, so re-sending is safe, and withholding it would only move the same
// request to the user's own `:continue`.
//
// Both costumes of that one event are exercised: the bare EOF a request whose
// body fit in the socket leaves behind, and the local close net/http reports
// when the body was still going out and its read half noticed the peer first
// (`readfrom ...: write ...: use of closed network connection`, reported from
// the field on a summarize).
func TestAgentRetriesConnectionFailure(t *testing.T) {
	t.Run("eof", func(t *testing.T) {
		testAgentRetriesConnectionFailure(t, &url.Error{
			Op:  "Post",
			URL: "http://127.0.0.1:9999/v1/chat/completions",
			Err: io.EOF,
		})
	})
	t.Run("closed under the write", func(t *testing.T) {
		testAgentRetriesConnectionFailure(t, &url.Error{
			Op:  "Post",
			URL: "http://127.0.0.1:9999/v1/chat/completions",
			Err: &net.OpError{Op: "readfrom", Net: "tcp", Err: &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}},
		})
	})
}

func testAgentRetriesConnectionFailure(t *testing.T, shape error) {
	t.Helper()
	fastRetries(t)
	failure := fmt.Errorf("failed to send request: %w", shape)
	p := &retryScriptProvider{failures: []error{failure}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
	if err != nil {
		t.Fatalf("Stream error = %v, want nil", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (1 + 1 retry)", p.calls)
	}
	if len(p.notices) != 1 {
		t.Fatalf("retry notices = %d, want 1", len(p.notices))
	}
	if got := p.notices[0].Reason; got != "provider connection failed" {
		t.Errorf("notice Reason = %q, want %q", got, "provider connection failed")
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected a non-empty result after the retry")
	}
}

// A cancel that beats its own connection error must not announce a retry: the
// next request would refuse it, and the user is owed the cancellation, not a
// notice about a backoff that will never run.
func TestAgentCanceledConnectionFailureIsNotRetried(t *testing.T) {
	fastRetries(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eof := &url.Error{Op: "Post", URL: "http://x/v1", Err: io.EOF}
	p := &retryScriptProvider{failures: []error{eof, eof}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(ctx, userText(), StreamCallbacks{OnRetry: p.onRetry})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (no re-send after cancel)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Fatalf("retry notices = %d, want 0", len(p.notices))
	}
}

// A cancel that lands on the last attempt is still the cancellation: the budget
// being spent does not turn the user's decision into a connection failure. The
// loop used to check the budget first, so this exact case reported whatever the
// transport said when the context died.
func TestAgentCancelOnLastAttemptReportsCancellation(t *testing.T) {
	fastRetries(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closed := &url.Error{Op: "Post", URL: "http://x/v1", Err: net.ErrClosed}
	p := &retryScriptProvider{
		failures:     []error{closed, closed, closed, closed},
		cancelOnCall: maxRetries + 1,
		cancel:       cancel,
	}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(ctx, userText(), StreamCallbacks{OnRetry: p.onRetry})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if p.calls != maxRetries+1 {
		t.Fatalf("provider calls = %d, want %d (the last attempt)", p.calls, maxRetries+1)
	}
	if len(p.notices) != maxRetries {
		t.Fatalf("retry notices = %d, want %d (every attempt before the last)", len(p.notices), maxRetries)
	}
}

// A cancel during the backoff wait must stop the loop before the next request.
func TestAgentRetryCanceledDuringBackoff(t *testing.T) {
	old := retryBaseDelay
	retryBaseDelay = time.Hour // long enough that only cancellation ends the wait
	t.Cleanup(func() { retryBaseDelay = old })

	ctx, cancel := context.WithCancel(context.Background())
	p := &retryScriptProvider{failures: []error{&APIError{StatusCode: 429}, &APIError{StatusCode: 429}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(ctx, userText(), StreamCallbacks{
		OnRetry: func(RetryNotice) error {
			cancel()
			return nil
		},
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (no re-send after cancel)", p.calls)
	}
}
