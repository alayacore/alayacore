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
	"strings"
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

// streamScript is what one call's stream delivers before the error it ends with.
type streamScript struct {
	events []StreamEvent
	err    error
}

// streamScriptProvider scripts each call's stream. A call past the end of the
// script succeeds with a minimal text answer, so a test can assert that a retry
// recovered; stepStarts counts the step boundaries the agent announced.
type streamScriptProvider struct {
	script     []streamScript
	calls      int
	stepStarts int
	notices    []RetryNotice
}

func (p *streamScriptProvider) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	call := p.calls
	p.calls++
	sc := streamScript{events: []StreamEvent{
		TextDeltaEvent{Delta: "hello", Key: "text"},
		TextCompleteEvent{Key: "text"},
		StepCompleteEvent{},
	}}
	if call < len(p.script) {
		sc = p.script[call]
	}
	return func(yield func(StreamEvent, error) bool) {
		for _, e := range sc.events {
			if !yield(e, nil) {
				return
			}
		}
		if sc.err != nil {
			yield(nil, sc.err)
		}
	}, nil
}

func (p *streamScriptProvider) SetReasoningLevel(_ int)                       {}
func (p *streamScriptProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

func (p *streamScriptProvider) onRetry(n RetryNotice) error {
	p.notices = append(p.notices, n)
	return nil
}

func (p *streamScriptProvider) onStepStart(int) error {
	p.stepStarts++
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

// A stream that ends with an error before recording anything is the same event as
// a request that never got a response: the step's record is empty and the adapter
// saw nothing, so the attempt is re-sent. The error here is a bare io.EOF, which
// the *classifier* rejects (a stream failure never arrives inside a *url.Error) —
// the retry comes from the marker, not from the shape.
func TestAgentRetriesStreamThatRecordedNothing(t *testing.T) {
	fastRetries(t)
	p := &streamScriptProvider{script: []streamScript{{err: io.EOF}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:     p.onRetry,
		OnStepStart: p.onStepStart,
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the retry to recover", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (1 + 1 retry)", p.calls)
	}
	if len(p.notices) != 1 {
		t.Fatalf("retry notices = %d, want 1", len(p.notices))
	}
	if got := p.notices[0].Reason; got != "provider stream ended early" {
		t.Errorf("notice Reason = %q, want %q", got, "provider stream ended early")
	}
	if p.stepStarts != 1 {
		t.Errorf("step starts = %d, want 1: a retry is not a new step", p.stepStarts)
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected a non-empty result after the retry")
	}
}

// A provider may close the blocks it never opened before reporting the error —
// OpenAI closes all three, because a body that stops must not be mistaken for a
// step that finished. Those boundaries open no block, mint no history ID and
// reach no callback, so the attempt recorded nothing and is still re-sent. The
// gate therefore asks the record, not whether an event arrived; counting events
// looked right and failed on this exact shape.
func TestAgentRetriesStreamWithBoundariesForUnopenedBlocks(t *testing.T) {
	fastRetries(t)
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ReasoningCompleteEvent{Key: "reasoning", Position: 0},
			TextCompleteEvent{Key: "text", Position: 1},
		},
		err: io.EOF,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
	if err != nil {
		t.Fatalf("Stream error = %v, want the retry to recover", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (1 + 1 retry)", p.calls)
	}
	if len(p.notices) != 1 || p.notices[0].Reason != "provider stream ended early" {
		t.Fatalf("notices = %+v, want one %q", p.notices, "provider stream ended early")
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected a non-empty result after the retry")
	}
}

// The other side of the same gate: once an event has been delivered, the record
// is no longer empty and re-sending would duplicate what the user watched. The
// error is surfaced in the provider's own words, without the pre-stream prefix.
func TestAgentDoesNotRetryStreamThatDiedAfterContent(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-answer")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
		err:    died,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	delivered := 0
	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:     p.onRetry,
		OnTextDelta: func(string, uint64) error { delivered++; return nil },
	})
	if err == nil {
		t.Fatal("Stream error = nil, want the stream failure surfaced")
	}
	if !strings.Contains(err.Error(), died.Error()) {
		t.Errorf("error = %q, want the provider's own wording", err)
	}
	if strings.Contains(err.Error(), "provider stream failed:") {
		t.Errorf("error = %q, want no pre-stream prefix on a stream failure", err)
	}
	if p.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (content was delivered)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Errorf("retry notices = %d, want 0", len(p.notices))
	}
	if delivered == 0 {
		t.Error("the partial content never reached the adapter, so the case did not run")
	}
}

// A stream that keeps dying before its first event spends the budget and then
// reports the provider's error as it arrived — the marker prints as what it
// wraps, so no internal type name reaches the user.
func TestAgentRetriesStreamBreakUntilTheBudgetIsSpent(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished before any content")
	script := make([]streamScript, maxRetries+1)
	for i := range script {
		script[i] = streamScript{err: died}
	}
	p := &streamScriptProvider{script: script}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
	if err == nil {
		t.Fatal("Stream error = nil, want the stream failure surfaced")
	}
	if got := err.Error(); got != died.Error() {
		t.Errorf("error = %q, want exactly %q", got, died.Error())
	}
	if p.calls != maxRetries+1 {
		t.Errorf("provider calls = %d, want %d", p.calls, maxRetries+1)
	}
	if len(p.notices) != maxRetries {
		t.Errorf("retry notices = %d, want %d", len(p.notices), maxRetries)
	}
}
