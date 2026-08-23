package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"
)

// salvageProvider emits a scripted event sequence via the seq callback.
type salvageProvider struct {
	seq func(yield func(StreamEvent, error) bool)
}

func (p *salvageProvider) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) { p.seq(yield) }, nil
}

func (p *salvageProvider) SetReasoningLevel(_ int)                       {}
func (p *salvageProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (p *salvageProvider) SetVideoConfig(_ int, _ int)                   {}

// fastTool returns a tool that executes immediately, closes executed to
// signal completion, and returns a success result.
func fastTool(executed chan struct{}) Tool {
	return Tool{
		Definition: ToolDefinition{Name: "fast_tool"},
		Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
			defer close(executed)
			return []ContentPart{&TextPart{Text: "done"}}, nil
		},
	}
}

// TestStreamSalvagesExecutedToolsOnCancel verifies that when the task is
// canceled while results are being collected, the tools that already
// executed keep their [tool_use, tool_result] pairs in history — their
// side effects happened and must stay visible. A second tool still
// awaiting user confirmation never executed, so it must not appear (it
// would look as if it ran and failed).
func TestStreamSalvagesExecutedToolsOnCancel(t *testing.T) {
	fastExecuted := make(chan struct{})
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ToolInputStartEvent{ID: "c1", Name: "fast_tool", Index: 0}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 0}, nil)
		// c2 requires confirmation; the user never responds.
		yield(ToolInputStartEvent{ID: "c2", Name: "confirm_tool", Index: 1}, nil)
		yield(ToolInputCompleteEvent{ID: "c2", Input: json.RawMessage(`{}`), Index: 1}, nil)
		<-fastExecuted // end the stream only after the fast tool ran
	}}

	confirmRequested := make(chan struct{})
	var gotContents []ContentPart
	finishCalled := 0
	// outputEvents records the display frames fired via OnToolOutput: the
	// canceled confirmation tool (c2) must still settle its UI window
	// with a UF error frame, even though it never ran and stays out of
	// the salvaged history.
	var outputEvents []struct {
		id  string
		err error
	}
	var outputMu sync.Mutex
	callbacks := StreamCallbacks{
		ToolNeedsConfirm: func(name string) bool { return name == "confirm_tool" },
		OnToolConfirm: func(_ ToolConfirmRequest) <-chan bool {
			close(confirmRequested)
			return make(chan bool) // never answered
		},
		OnToolOutput: func(id string, _ []ContentPart, err error, _ uint64) error {
			outputMu.Lock()
			defer outputMu.Unlock()
			outputEvents = append(outputEvents, struct {
				id  string
				err error
			}{id: id, err: err})
			return nil
		},
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			finishCalled++
			gotContents = contents
			return nil
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools:    []Tool{fastTool(fastExecuted)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := agent.Stream(ctx, nil, callbacks)
		done <- err
	}()

	select {
	case <-fastExecuted:
	case <-time.After(5 * time.Second):
		t.Fatal("fast tool never executed")
	}
	select {
	case <-confirmRequested:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation was never requested")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stream() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}

	if finishCalled != 1 {
		t.Fatalf("OnStepFinish called %d times, want 1 (the salvage fold)", finishCalled)
	}
	if len(gotContents) != 2 {
		t.Fatalf("salvaged contents = %d parts, want 2 (c1 pair only): %#v", len(gotContents), gotContents)
	}
	tc, ok := gotContents[0].(*ToolInputPart)
	if !ok || tc.ID != "c1" || tc.GetRole() != RoleAssistant {
		t.Errorf("gotContents[0] = %#v, want ToolInputPart c1 with assistant role", gotContents[0])
	}
	tr, ok := gotContents[1].(*ToolOutputPart)
	if !ok || tr.ID != "c1" || tr.GetRole() != RoleTool {
		t.Errorf("gotContents[1] = %#v, want ToolOutputPart c1 with tool role", gotContents[1])
	}

	// The canceled confirmation tool must fire a display-only UF error
	// frame (so its UI window settles instead of spinning forever), while
	// staying out of the salvaged history (asserted above: 2 parts, c1
	// pair only). c1's normal result frame is also recorded.
	if len(outputEvents) != 2 {
		t.Fatalf("OnToolOutput fired %d times, want 2 (c1 result + c2 cancel frame)", len(outputEvents))
	}
	var c2Event *struct {
		id  string
		err error
	}
	for i := range outputEvents {
		e := &outputEvents[i]
		if e.id == "c1" && e.err != nil {
			t.Errorf("c1 result frame must carry no error, got %v", e.err)
		}
		if e.id == "c2" {
			c2Event = e
		}
	}
	if c2Event == nil {
		t.Fatal("no OnToolOutput frame for the canceled confirmation tool c2")
	}
	if c2Event.err == nil || !strings.Contains(c2Event.err.Error(), "canceled") {
		t.Errorf("c2 OnToolOutput error = %v, want a cancellation error", c2Event.err)
	}
}

// TestStreamSalvageOmitsAmbiguousToolIDs verifies that a provider reusing
// a tool-call ID does not produce guessed pairings in the salvaged
// history: when two tool calls share an ID, the result-to-call assignment
// is unknowable, so both calls are omitted entirely (the normal path
// already rejects duplicate IDs via reorderToolResults; the salvage must
// not paper over the ambiguity with a wrong pairing).
func TestStreamSalvageOmitsAmbiguousToolIDs(t *testing.T) {
	executed := make(chan struct{}, 2)
	mkTool := func(name string) Tool {
		return Tool{
			Definition: ToolDefinition{Name: name},
			Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
				executed <- struct{}{}
				return []ContentPart{&TextPart{Text: "done"}}, nil
			},
		}
	}

	providerErr := errors.New("provider stream failed")
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		// Both tool calls reuse ID "c1" (protocol violation).
		yield(ToolInputStartEvent{ID: "c1", Name: "tool_a", Index: 0}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 0}, nil)
		yield(ToolInputStartEvent{ID: "c1", Name: "tool_b", Index: 1}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 1}, nil)
		// Wait for both tools to execute, then fail the stream.
		<-executed
		<-executed
		yield(nil, providerErr)
	}}

	finishCalled := 0
	callbacks := StreamCallbacks{
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			finishCalled++
			return nil
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools:    []Tool{mkTool("tool_a"), mkTool("tool_b")},
	})

	done := make(chan error, 1)
	go func() {
		_, err := agent.Stream(context.Background(), nil, callbacks)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, providerErr) {
			t.Fatalf("Stream() error = %v, want provider stream failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after provider error")
	}

	if finishCalled != 0 {
		t.Fatalf("OnStepFinish called %d times, want 0 (ambiguous tools must not be salvaged)", finishCalled)
	}
}

// TestStreamSalvageAfterStepComplete verifies the complete-then-error
// edge: the provider finishes a step (StepCompleteEvent with text + tool
// call) and THEN the stream errors. The salvage keeps the executed
// tool's pair — never leaving an orphaned tool_use — while the step's
// text is dropped (the step is treated as failed; consistent with cancel
// semantics). This locks in a SAFE history: no tool_use without its
// tool_result, no duplicated parts.
func TestStreamSalvageAfterStepComplete(t *testing.T) {
	fastExecuted := make(chan struct{})
	providerErr := errors.New("provider stream failed after step complete")
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(TextDeltaEvent{Delta: "partial text", Index: 0}, nil)
		yield(TextCompleteEvent{Text: "partial text", Index: 0}, nil)
		yield(ToolInputStartEvent{ID: "c1", Name: "fast_tool", Index: 1}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 1}, nil)
		<-fastExecuted // tool ran and its result is in flight
		yield(StepCompleteEvent{
			Contents: []ContentPart{
				&TextPart{Text: "partial text", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
				&ToolInputPart{ID: "c1", Name: "fast_tool", Input: json.RawMessage(`{}`), ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
			},
			Usage: Usage{InputTokens: 10, OutputTokens: 5},
		}, nil)
		yield(nil, providerErr)
	}}

	var gotContents []ContentPart
	finishCalled := 0
	callbacks := StreamCallbacks{
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			finishCalled++
			gotContents = contents
			return nil
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools:    []Tool{fastTool(fastExecuted)},
	})

	_, err := agent.Stream(context.Background(), nil, callbacks)
	if !errors.Is(err, providerErr) {
		t.Fatalf("Stream() error = %v, want provider stream failure", err)
	}
	if finishCalled != 1 {
		t.Fatalf("OnStepFinish called %d times, want 1 (the salvage fold)", finishCalled)
	}
	// Safe history: exactly one [tool_use, tool_result] pair, no text, no
	// orphaned tool_use. (The step's text is dropped — see the salvage
	// defer note in streamEvents.)
	if len(gotContents) != 2 {
		t.Fatalf("salvaged contents = %d parts, want 2 (c1 pair only): %#v", len(gotContents), gotContents)
	}
	if _, ok := gotContents[0].(*ToolInputPart); !ok {
		t.Errorf("gotContents[0] = %#v, want ToolInputPart", gotContents[0])
	}
	if _, ok := gotContents[1].(*ToolOutputPart); !ok {
		t.Errorf("gotContents[1] = %#v, want ToolOutputPart", gotContents[1])
	}
}

// TestStreamSalvagesExecutedToolsOnStreamError verifies the same salvage
// on a provider stream failure (not just cancel): the tool already
// executed before the stream died, so its pair must survive in history.
func TestStreamSalvagesExecutedToolsOnStreamError(t *testing.T) {
	fastExecuted := make(chan struct{})
	providerErr := errors.New("provider stream failed mid-tool")
	provider := &salvageProvider{seq: func(yield func(StreamEvent, error) bool) {
		yield(ToolInputStartEvent{ID: "c1", Name: "fast_tool", Index: 0}, nil)
		yield(ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Index: 0}, nil)
		<-fastExecuted // let the tool finish before failing the stream
		yield(nil, providerErr)
	}}

	var gotContents []ContentPart
	finishCalled := 0
	callbacks := StreamCallbacks{
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			finishCalled++
			gotContents = contents
			return nil
		},
	}

	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools:    []Tool{fastTool(fastExecuted)},
	})

	_, err := agent.Stream(context.Background(), nil, callbacks)
	if !errors.Is(err, providerErr) {
		t.Fatalf("Stream() error = %v, want provider stream failure", err)
	}

	if finishCalled != 1 {
		t.Fatalf("OnStepFinish called %d times, want 1 (the salvage fold)", finishCalled)
	}
	if len(gotContents) != 2 {
		t.Fatalf("salvaged contents = %d parts, want 2 (c1 pair): %#v", len(gotContents), gotContents)
	}
	tc, ok := gotContents[0].(*ToolInputPart)
	if !ok || tc.ID != "c1" || tc.GetRole() != RoleAssistant {
		t.Errorf("gotContents[0] = %#v, want ToolInputPart c1 with assistant role", gotContents[0])
	}
	tr, ok := gotContents[1].(*ToolOutputPart)
	if !ok || tr.ID != "c1" || tr.GetRole() != RoleTool {
		t.Errorf("gotContents[1] = %#v, want ToolOutputPart c1 with tool role", gotContents[1])
	}
}
