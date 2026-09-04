package llm

// Serial tool-calling tests.
//
// The mode exists because a great many models and servers have no notion of
// parallel tool calls: their calls land in an order that has to hold, and two of
// them must never be writing files at the same time. These tests pin the three
// things that make that true and the one thing that must not regress — the
// parallel driver still overlaps, so a serial test passing cannot be the result
// of everything having quietly become serial.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"
)

// traceTools records, in a shared log, when each tool begins and ends. The log
// is the assertion: a serialized run interleaves enter/exit pairs, an overlapped
// run does not.
type traceTools struct {
	mu      sync.Mutex
	events  []string
	inside  int
	maxIn   int
	dur     time.Duration
	onEnter func(id string) // runs while inside the tool, for cancellation tests
}

func newTraceTools(dur time.Duration) *traceTools {
	return &traceTools{dur: dur}
}

func (tt *traceTools) execute(name string) func(context.Context, json.RawMessage) ([]ContentPart, error) {
	return func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
		tt.mu.Lock()
		tt.inside++
		if tt.inside > tt.maxIn {
			tt.maxIn = tt.inside
		}
		tt.events = append(tt.events, "enter:"+name)
		hook := tt.onEnter
		tt.mu.Unlock()

		if hook != nil {
			hook(name)
		}
		time.Sleep(tt.dur)

		tt.mu.Lock()
		tt.events = append(tt.events, "exit:"+name)
		tt.inside--
		tt.mu.Unlock()
		return []ContentPart{&TextPart{Text: "did " + name}}, nil
	}
}

// toolsFor builds one traced tool per name.
func toolsFor(tt *traceTools, names ...string) []Tool {
	tools := make([]Tool, 0, len(names))
	for _, n := range names {
		tools = append(tools, Tool{
			Definition: ToolDefinition{Name: n, Schema: []byte(`{"type":"object"}`)},
			Execute:    tt.execute(n),
		})
	}
	return tools
}

// callsThenText is a provider that asks for the named tools in one step, then
// answers with text so the loop terminates.
func callsThenText(names ...string) *mockProviderWithTextAndTools {
	calls := make([]ToolInputPart, 0, len(names))
	for i, n := range names {
		calls = append(calls, ToolInputPart{ID: fmt.Sprintf("call_%d", i+1), Name: n, Input: []byte("{}")})
	}
	return &mockProviderWithTextAndTools{responses: []mockResponse{
		{toolCalls: calls},
		{text: "done"},
	}}
}

func runStream(t *testing.T, a *Agent) (*StreamResult, error) {
	t.Helper()
	return a.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{})
}

// The core guarantee: the calls run one at a time, in the order the model made
// them.
func TestSerialToolCallsRunOneAtATimeInCallOrder(t *testing.T) {
	tt := newTraceTools(15 * time.Millisecond)
	agent := NewAgent(AgentConfig{
		Provider:        callsThenText("a", "b", "c"),
		Tools:           toolsFor(tt, "a", "b", "c"),
		SerialToolCalls: true,
	})

	if _, err := runStream(t, agent); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	want := "enter:a exit:a enter:b exit:b enter:c exit:c"
	if got := strings.Join(tt.events, " "); got != want {
		t.Errorf("execution trace:\n got %q\nwant %q", got, want)
	}
	if tt.maxIn != 1 {
		t.Errorf("max concurrent tools = %d, want 1 — serial mode must never have two in flight", tt.maxIn)
	}
}

// The control. Without it the test above could be green because everything went
// serial — including the default mode. This one fails if the parallel driver
// ever stops overlapping.
func TestParallelToolCallsStillOverlap(t *testing.T) {
	// Every tool blocks until all three are inside. Under a serial driver the
	// first one would wait forever, so the deadline is the assertion.
	const barrierTimeout = 3 * time.Second
	arrived := make(chan struct{})
	var mu sync.Mutex
	inside := 0

	tracked := func() {
		mu.Lock()
		inside++
		if inside == 3 {
			close(arrived)
		}
		mu.Unlock()
		select {
		case <-arrived:
		case <-time.After(barrierTimeout):
			mu.Lock()
			t.Errorf("only %d tools entered before the deadline; the parallel driver has stopped overlapping", inside)
			mu.Unlock()
		}
	}

	tools := make([]Tool, 0, 3)
	for _, n := range []string{"a", "b", "c"} {
		tools = append(tools, Tool{
			Definition: ToolDefinition{Name: n, Schema: []byte(`{"type":"object"}`)},
			Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
				tracked()
				return []ContentPart{&TextPart{Text: "did " + n}}, nil
			},
		})
	}

	agent := NewAgent(AgentConfig{
		Provider: callsThenText("a", "b", "c"),
		Tools:    tools,
		// SerialToolCalls deliberately unset: this is the default mode.
	})
	res, err := runStream(t, agent)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if got := len(resultsIn(res.Contents)); got != 3 {
		t.Errorf("%d results in history, want 3", got)
	}
}

// Numbering must not depend on the mode: a result's history ID is minted when
// its arguments complete, which is the same moment the parallel driver mints it.
// IDs are arrival-based by design (docs/providers.md), and a session reopened
// after a mode switch must re-lay the same numbers.
func TestSerialToolCallsKeepArrivalOrderHistoryIDs(t *testing.T) {
	var mu sync.Mutex
	var ids []uint64
	next := uint64(100)

	provider := callsThenText("a", "b", "c")
	agent := NewAgent(AgentConfig{
		Provider:        provider,
		Tools:           toolsFor(newTraceTools(0), "a", "b", "c"),
		SerialToolCalls: true,
	})

	_, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{
		IDGen: func() uint64 { next++; return next },
		OnToolOutput: func(_ string, _ []ContentPart, _ error, historyID uint64) error {
			mu.Lock()
			ids = append(ids, historyID)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// The provider streams three call blocks; the IDs issued for results follow
	// that arrival order, not the order the driver happened to run them in.
	if len(ids) != 3 {
		t.Fatalf("got %d tool outputs, want 3", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("result IDs %v are not in arrival order", ids)
			break
		}
	}
}

// Cancellation between two calls: the executed pair stays in history, the never
// -started call is dropped rather than recorded as an unanswered tool_use.
// Returning without an error would instead reach the strict pairing attach and
// fail the step for a call the cancellation legitimately prevented.
func TestSerialCancelKeepsExecutedDropsUnanswered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tt := newTraceTools(0)
	tt.onEnter = func(name string) {
		if name == "b" {
			cancel() // user stops the task while the second tool runs
		}
	}

	provider := callsThenText("a", "b", "c")
	agent := NewAgent(AgentConfig{
		Provider:        provider,
		Tools:           toolsFor(tt, "a", "b", "c"),
		SerialToolCalls: true,
	})

	var finished [][]ContentPart
	_, err := agent.Stream(ctx, []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			finished = append(finished, contents)
			return nil
		},
	})

	if err == nil {
		t.Fatal("expected the canceled step to report an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if len(tt.events) != 4 || tt.events[3] != "exit:b" {
		t.Errorf("tools that ran: %v, want a and b only (c must not start)", tt.events)
	}

	// Salvage ran: a and b are recorded as answered pairs, c is absent.
	var out []ContentPart
	for _, step := range finished {
		out = append(out, step...)
	}
	if got := strings.Join(resultsIn(out), ","); got != "call_1,call_2" {
		t.Errorf("answered calls = %q, want call_1,call_2", got)
	}
	for _, p := range out {
		if tc, ok := p.(*ToolInputPart); ok && tc.ID == "call_3" {
			t.Error("call_3 is recorded without a tool_result; an orphan tool_use must not survive")
		}
	}
}

// Declining one tool answers that call with an error and lets the rest run: a
// refusal is the user's answer to one call, not a stop order for the step.
func TestSerialDeclineAnswersOneCallAndContinues(t *testing.T) {
	tt := newTraceTools(0)
	provider := callsThenText("a", "b", "c")
	agent := NewAgent(AgentConfig{
		Provider:        provider,
		Tools:           toolsFor(tt, "a", "b", "c"),
		SerialToolCalls: true,
	})

	declined := make(chan bool, 1)
	declined <- false

	res, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{
		ToolNeedsConfirm: func(string) bool { return true },
		OnToolConfirm: func(req ToolConfirmRequest) <-chan bool {
			if req.Name == "a" {
				return declined
			}
			ch := make(chan bool, 1)
			ch <- true
			return ch
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// Every call got an answer, in call order — including the declined one.
	if got := strings.Join(resultsIn(res.Contents), ","); got != "call_1,call_2,call_3" {
		t.Errorf("answered calls = %q, want all three", got)
	}
	var declinedPart *ToolOutputPart
	for _, p := range res.Contents {
		if out, ok := p.(*ToolOutputPart); ok && out.ID == "call_1" {
			declinedPart = out
		}
	}
	if declinedPart == nil || !declinedPart.IsError {
		t.Error("declined call has no error result; the model cannot see that the user said no")
	}
	if len(tt.events) != 4 || tt.events[0] != "enter:b" {
		t.Errorf("tools that ran: %v, want b and c only (a was declined, never executed)", tt.events)
	}
}

// mockProviderDyingMidCall streams one complete tool call and then fails the
// stream — the shape of a connection reset that arrives after the model had
// already asked for a tool.
type mockProviderDyingMidCall struct {
	err error
}

func (m *mockProviderDyingMidCall) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		yield(ToolInputStartEvent{ID: "call_1", Name: "a", Key: "block:1"}, nil)
		yield(ToolInputDeltaEvent{ID: "call_1", Delta: "{}", Key: "block:1"}, nil)
		yield(ToolInputCompleteEvent{ID: "call_1", Key: "block:1"}, nil)
		yield(nil, m.err)
	}, nil
}

func (m *mockProviderDyingMidCall) SetReasoningLevel(_ int)                       {}
func (m *mockProviderDyingMidCall) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *mockProviderDyingMidCall) SetVideoConfig(_ int, _ int)                   {}

// A stream that fails after a call has streamed must run nothing at all. Under
// the parallel driver the call was already launched when the stream died, which
// is why side effects could happen on a failed step; serial starts after the
// stream ends, so a dead stream has no consequences.
func TestSerialStreamErrorRunsNoTools(t *testing.T) {
	tt := newTraceTools(0)
	agent := NewAgent(AgentConfig{
		Provider:        &mockProviderDyingMidCall{err: errors.New("connection reset")},
		Tools:           toolsFor(tt, "a"),
		SerialToolCalls: true,
	})

	_, err := runStream(t, agent)
	if err == nil {
		t.Fatal("expected the failed stream to report an error")
	}
	if len(tt.events) != 0 {
		t.Errorf("tools that ran: %v, want none", tt.events)
	}
}
