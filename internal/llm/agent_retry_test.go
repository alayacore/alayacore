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
// recovered; stepStarts counts the step boundaries the agent announced, and
// requests holds the history each call was sent on, so a test can see what a
// resumed request actually carried.
type streamScriptProvider struct {
	script     []streamScript
	calls      int
	stepStarts int
	notices    []RetryNotice
	requests   [][]ContentPart
}

func (p *streamScriptProvider) StreamMessages(_ context.Context, contents []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	call := p.calls
	p.calls++
	p.requests = append(p.requests, append([]ContentPart(nil), contents...))
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

// countText returns how many TextParts carry exactly the given text.
func countText(parts []ContentPart, want string) int {
	n := 0
	for _, p := range parts {
		if tp, ok := p.(*TextPart); ok && tp.Text == want {
			n++
		}
	}
	return n
}

// textsOf renders a history's text parts, for a failure message.
func textsOf(parts []ContentPart) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if tp, ok := p.(*TextPart); ok {
			out = append(out, string(p.GetRole())+":"+tp.Text)
		}
	}
	return out
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

// resumeCaller stands in for the session's step boundary: it records the history
// each step is built on and appends the synthetic "Continue" a caller appends when
// that history ends on an assistant message — the rule Session.stepUserParts
// applies in production, and the reason a continuation is a well-formed request
// rather than prefill.
type resumeCaller struct {
	requests [][]ContentPart
}

func (c *resumeCaller) onBeforeSend(contents []ContentPart) ([]ContentPart, error) {
	c.requests = append(c.requests, append([]ContentPart(nil), contents...))
	if len(contents) == 0 || contents[len(contents)-1].GetRole() != RoleAssistant {
		return nil, nil
	}
	return append(contents, &TextPart{Text: "Continue", ContentPartMeta: ContentPartMeta{Role: RoleUser}}), nil
}

// A stream that dies after delivering content cannot be re-sent — what streamed
// is on the adapter's screen and in history — so it is resumed instead: the
// salvage stays, the caller's boundary ends the next request on a user part, and
// the model is asked to carry on. That is the user's own `:continue` performed for
// them, which is why it needs no classification of the error: a stream failure
// carries no status and never arrives inside a *url.Error.
func TestAgentResumesStreamThatDiedAfterContent(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-answer")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
		err:    died,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	caller := &resumeCaller{}

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnStepStart:  p.onStepStart,
		OnBeforeSend: caller.onBeforeSend,
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the cut step, then its continuation)", p.calls)
	}
	if p.stepStarts != 2 {
		t.Errorf("step starts = %d, want 2: a continuation is a step of its own", p.stepStarts)
	}
	if len(p.notices) != 1 {
		t.Fatalf("retry notices = %d, want 1", len(p.notices))
	}
	if got := p.notices[0].Reason; got != "provider stream was cut off" {
		t.Errorf("notice Reason = %q, want %q", got, "provider stream was cut off")
	}
	if got := p.notices[0].Retry; got != 1 {
		t.Errorf("notice retry number = %d, want 1", got)
	}

	// The continuation was sent on the salvage, with the boundary's user part
	// last — the shape that keeps the request from being read as prefill.
	second := p.requests[1]
	if len(second) != 3 {
		t.Fatalf("continuation carried %d parts, want 3 (prompt, salvage, Continue)", len(second))
	}
	if tp, ok := second[len(second)-1].(*TextPart); !ok || tp.Text != "Continue" {
		t.Errorf("last part of the continuation = %#v, want the boundary's Continue", second[len(second)-1])
	}

	// Nothing was duplicated: the partial answer is in the history once.
	var partials int
	for _, part := range result.Contents {
		if tp, ok := part.(*TextPart); ok && tp.Text == "partial" {
			partials++
		}
	}
	if partials != 1 {
		t.Errorf("the partial answer appears %d times in the result, want 1", partials)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected a non-empty result after the continuation")
	}
}

// The continuation needs a caller that can make the next request well-formed, and
// that is what OnBeforeSend is: the hook that owns the history the next request is
// built from. A caller without one (a summarize call) is not resumed — it gets the
// provider's error in the provider's own words, with no notice and no second
// request.
func TestAgentSurfacesCutStreamWhenTheCallerCannotResume(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-answer")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
		err:    died,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{OnRetry: p.onRetry})
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
		t.Errorf("provider calls = %d, want 1 (no caller to make a continuation well-formed)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Errorf("retry notices = %d, want 0", len(p.notices))
	}
}

// A tool call whose block was opened and whose arguments never completed: the
// adapter has already drawn the call, so the attempt delivered something and must
// not be re-sent — a second attempt would show it a second call under a new
// history ID. The resume keeps it out of history instead, because an unfinished
// call has no part and a tool_use without its tool_result is a request the next
// step cannot build.
func TestAgentResumeLeavesAnUnfinishedToolCallOut(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-call")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ToolInputStartEvent{ID: "call-1", Name: "read_file", Key: "call-1", Position: 0},
			ToolInputDeltaEvent{ID: "call-1", Delta: `{"pa`, Key: "call-1", Position: 0},
		},
		err: died,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	caller := &resumeCaller{}

	starts := 0
	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:          p.onRetry,
		OnToolInputStart: func(string, string, uint64) error { starts++; return nil },
		OnBeforeSend:     caller.onBeforeSend,
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}
	if starts == 0 {
		t.Fatal("the tool start never reached the adapter, so the case did not run")
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the cut step, then its continuation)", p.calls)
	}
	for _, part := range p.requests[1] {
		if tc, ok := part.(*ToolInputPart); ok {
			t.Errorf("the continuation re-sent the unfinished call %q", tc.ID)
		}
	}
	for _, part := range result.Contents {
		if tc, ok := part.(*ToolInputPart); ok {
			t.Errorf("the unfinished call %q reached history", tc.ID)
		}
	}
}

// A step that keeps being cut spends the resume budget and then reports the
// provider's error exactly as it arrived — the marker prints as what it wraps.
func TestAgentResumeStopsWhenTheBudgetIsSpent(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-answer")
	script := make([]streamScript, maxRetries+2)
	for i := range script {
		script[i] = streamScript{
			events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
			err:    died,
		}
	}
	p := &streamScriptProvider{script: script}
	// Steps are not the bound here; the resume budget is.
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 10})
	caller := &resumeCaller{}

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: caller.onBeforeSend,
	})
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

// A continuation is a request, so it spends the step budget as one. A task on its
// last step has none left to spend, and it reports the stream's own error — not a
// step maximum it never ran out of by looping.
func TestAgentResumeCannotOutrunTheStepBudget(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-answer")
	cut := streamScript{
		events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
		err:    died,
	}
	p := &streamScriptProvider{script: []streamScript{cut, cut, cut}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 2})
	caller := &resumeCaller{}

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: caller.onBeforeSend,
	})
	if err == nil || !strings.Contains(err.Error(), died.Error()) {
		t.Fatalf("error = %v, want the provider's own wording, not a step maximum", err)
	}
	if errors.Is(err, ErrMaxStepsExceeded) {
		t.Errorf("error = %v, want the stream failure that ended the task", err)
	}
	if p.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (the step budget)", p.calls)
	}
	if len(p.notices) != 1 {
		t.Errorf("retry notices = %d, want 1", len(p.notices))
	}
}

// The resume's wait is interruptible, like the retry's: a :cancel during it stops
// the loop before the next request rather than after it.
func TestAgentResumeCanceledDuringBackoff(t *testing.T) {
	old := retryBaseDelay
	retryBaseDelay = time.Hour // long enough that only cancellation ends the wait
	t.Cleanup(func() { retryBaseDelay = old })

	ctx, cancel := context.WithCancel(context.Background())
	cut := streamScript{
		events: []StreamEvent{TextDeltaEvent{Delta: "partial", Key: "text"}},
		err:    errors.New("stream vanished mid-answer"),
	}
	p := &streamScriptProvider{script: []streamScript{cut, cut}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	caller := &resumeCaller{}

	_, err := agent.Stream(ctx, userText(), StreamCallbacks{
		OnRetry: func(RetryNotice) error {
			cancel()
			return nil
		},
		OnBeforeSend: caller.onBeforeSend,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if p.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (no continuation after cancel)", p.calls)
	}
}

// The gate is the marker, not "content was delivered": an adapter whose write
// failed has not lost the provider, and asking the model again would only produce
// content that adapter still cannot take.
func TestAgentDoesNotResumeAfterACallbackFailure(t *testing.T) {
	fastRetries(t)
	broken := errors.New("adapter write failed")
	p := &streamScriptProvider{}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	caller := &resumeCaller{}

	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnTextDelta:  func(string, uint64) error { return broken },
		OnBeforeSend: caller.onBeforeSend,
	})
	if err == nil || !strings.Contains(err.Error(), broken.Error()) {
		t.Fatalf("error = %v, want the callback's failure", err)
	}
	if p.calls != 1 {
		t.Errorf("provider calls = %d, want 1", p.calls)
	}
	if len(p.notices) != 0 {
		t.Errorf("retry notices = %d, want 0", len(p.notices))
	}
}

// The agent never invents the trailing part a continuation may need: the caller
// owns that (see resumeStep). When the salvage already ends on the tool result the
// model asked for, the boundary appends nothing and the continuation is sent as it
// stands — the ordinary shape of a step, which is why no request in this tree ends
// on an assistant message by the agent's own doing.
func TestAgentResumeSendsWhatTheCallerAllows(t *testing.T) {
	fastRetries(t)
	executed := make(chan struct{})
	died := errors.New("stream vanished after the tool result")
	calls := 0
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		calls++
		if calls > 1 {
			yield(TextDeltaEvent{Delta: "done", Key: "block:0"}, nil)
			yield(TextCompleteEvent{Key: "block:0"}, nil)
			yield(StepCompleteEvent{Usage: Usage{InputTokens: 10, OutputTokens: 5}}, nil)
			return
		}
		yield(ToolInputStartEvent{ID: "c1", Name: "fast_tool", Key: "block:0"}, nil)
		yield(ToolInputDeltaEvent{ID: "c1", Delta: "{}", Key: "block:0"}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Key: "block:0"}, nil)
		<-executed // the result is collected before the stream dies
		yield(nil, died)
	}}
	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools:    []Tool{fastTool(executed)},
		MaxSteps: 3,
	})
	caller := &resumeCaller{}

	if _, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnBeforeSend: caller.onBeforeSend,
	}); err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the cut step, then its continuation)", calls)
	}
	if len(caller.requests) != 2 {
		t.Fatalf("OnBeforeSend saw %d steps, want 2", len(caller.requests))
	}
	second := caller.requests[1]
	if len(second) == 0 {
		t.Fatal("the continuation was sent on an empty history")
	}
	if _, ok := second[len(second)-1].(*ToolOutputPart); !ok {
		t.Errorf("continuation ends on %#v, want the tool result the model asked for", second[len(second)-1])
	}
}

// A step whose terminal event arrived is over, whatever the socket does next: the
// error after it is the stream's epilogue, not its verdict. So the step is not
// resumed — resuming it would append the boundary's `Continue` after a finished
// answer and ask the model to answer again. This is the one shape where "content
// was delivered" does *not* mean "cut".
func TestAgentDoesNotResumeAStepThatCompleted(t *testing.T) {
	fastRetries(t)
	epilogue := errors.New("stream vanished after the step completed")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			TextDeltaEvent{Delta: "the answer", Key: "text"},
			TextCompleteEvent{Key: "text"},
			StepCompleteEvent{Usage: Usage{InputTokens: 10, OutputTokens: 5}},
		},
		err: epilogue,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	caller := &resumeCaller{}

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: caller.onBeforeSend,
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the completed step to land", err)
	}
	if p.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (a completed step needs no continuation)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Errorf("retry notices = %v, want none", p.notices)
	}
	if countText(result.Contents, "the answer") != 1 {
		t.Errorf("contents = %v, want the answer exactly once", textsOf(result.Contents))
	}
	if countText(result.Contents, "Continue") != 0 {
		t.Errorf("contents = %v, want no Continue after a finished answer", textsOf(result.Contents))
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

// A tool call whose block was opened and whose arguments never completed: the
// adapter has already drawn the call, so the attempt delivered something and must
// not be re-sent — a second attempt would show it a second call under a new
// history ID. This is why the gate asks whether a block was opened rather than
// whether a *part* exists: an unfinished call yields no part, and asking parts
// would call this attempt delivered-nothing and re-send it. (The deliver-something
// answer leads to a resume, which this test gives no caller for; see
// TestAgentResumeLeavesAnUnfinishedToolCallOut for that side.)
func TestAgentDoesNotRetryStreamThatDiedWithAnOpenToolCall(t *testing.T) {
	fastRetries(t)
	died := errors.New("stream vanished mid-call")
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ToolInputStartEvent{ID: "call-1", Name: "read_file", Key: "call-1", Position: 0},
			ToolInputDeltaEvent{ID: "call-1", Delta: `{"pa`, Key: "call-1", Position: 0},
		},
		err: died,
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})

	starts := 0
	_, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:          p.onRetry,
		OnToolInputStart: func(string, string, uint64) error { starts++; return nil },
	})
	if err == nil {
		t.Fatal("Stream error = nil, want the stream failure surfaced")
	}
	if !strings.Contains(err.Error(), died.Error()) {
		t.Errorf("error = %q, want the provider's own wording", err)
	}
	if p.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (the adapter saw a tool call)", p.calls)
	}
	if len(p.notices) != 0 {
		t.Errorf("retry notices = %d, want 0", len(p.notices))
	}
	if starts == 0 {
		t.Fatal("the tool start never reached the adapter, so the case did not run")
	}
}
