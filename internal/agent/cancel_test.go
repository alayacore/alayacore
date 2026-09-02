package agent

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// newCancelTestSession builds a minimal started Session whose run() loop
// is live, with an optional pre-running task. The task's cancel function
// closes canceled when invoked.
func newCancelTestSession(t *testing.T, withTask bool) (*Session, chan struct{}, func()) {
	t.Helper()

	output := &MockOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()

	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  r,
				Output: output,
			},
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
	}

	var canceled chan struct{}
	if withTask {
		// The task exists before Start; run() owns activeTask from then
		// on, but only reads it (and clears it on completion).
		canceled = make(chan struct{})
		s.activeTask = &taskHandle{cancel: func() { close(canceled) }}
	}

	s.mcpService = newMCPService(nil, output)
	s.Start()

	cleanup := func() {
		_ = w.Close() // unblocks inputPump
		cancel()      // stops run()
	}
	return s, canceled, cleanup
}

func TestCancelTask_RunningTask(t *testing.T) {
	s, canceled, cleanup := newCancelTestSession(t, true)
	defer cleanup()

	if !s.CancelTask() {
		t.Fatal("CancelTask should report that a running task was canceled")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("task cancel function was not invoked")
	}
}

func TestCancelTask_NoTask(t *testing.T) {
	s, _, cleanup := newCancelTestSession(t, false)
	defer cleanup()

	if s.CancelTask() {
		t.Fatal("CancelTask should report false when no task is running")
	}
}

func TestCancelTask_WhileDraining(t *testing.T) {
	// Input EOF (the terseio case: stdin is closed right after the
	// prompt) sends run() into drainUntilTaskDone while the task runs.
	// CancelTask must still be served there.
	s, canceled, cleanup := newCancelTestSession(t, true)
	defer cleanup()

	// Close the input pipe — inputPump reads EOF, run() enters
	// drainUntilTaskDone waiting for the task to finish.
	s.Input.(*io.PipeReader).Close()

	if !s.CancelTask() {
		t.Fatal("CancelTask should cancel a task while the session is draining")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("task cancel function was not invoked during drain")
	}
}

func TestCancelTask_SessionExited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // session already gone
	s := &Session{sharedState: sharedState{sessionCtx: ctx}}

	if s.CancelTask() {
		t.Fatal("CancelTask should report false after the session exited")
	}
}

// blockingProvider blocks until ctx is canceled and then fails with
// ctx.Err — like a real provider whose HTTP stream is aborted when the
// request context is canceled. started is closed when StreamMessages is
// first called, signaling that the task is running.
type blockingProvider struct {
	started chan struct{}
	once    sync.Once
}

func (m *blockingProvider) StreamMessages(ctx context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	m.once.Do(func() { close(m.started) })
	return func(yield func(llm.StreamEvent, error) bool) {
		<-ctx.Done()
		yield(nil, ctx.Err())
	}, nil
}

func (m *blockingProvider) SetReasoningLevel(_ int)                       {}
func (m *blockingProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *blockingProvider) SetVideoConfig(_ int, _ int)                   {}

// syncOutput is a concurrency-safe output capture for tests where the
// session writes from task goroutines while the test reads.
type syncOutput struct {
	mu       sync.Mutex
	messages []string
}

func (m *syncOutput) Write(p []byte) (int, error) {
	m.mu.Lock()
	m.messages = append(m.messages, string(p))
	m.mu.Unlock()
	return len(p), nil
}

func (m *syncOutput) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.messages, "")
}

// TestCancelTask_EndToEnd verifies the full terseio-style path: a prompt
// arrives over the TLV input pipe, the task blocks in the provider,
// CancelTask aborts it, and the session emits an SM error — the signal
// the terseio output uses to discard the buffered answer.
func TestCancelTask_EndToEnd(t *testing.T) {
	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()

	provider := &blockingProvider{started: make(chan struct{})}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		MaxSteps: 10,
	})

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider},
			SessionConfig: SessionConfig{
				Input:   r,
				Output:  output,
				NoDelta: true,
			},
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
	}
	s.mcpService = newMCPService(nil, output)
	s.Start()

	// Deliver a prompt over the TLV input pipe (what terseio does after
	// reading stdin to EOF).
	if err := tlv.WriteTLV(w, tlv.TagUserT, tlv.WrapID("1", "hello")); err != nil {
		t.Fatalf("write UT: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
		t.Fatalf("write UE: %v", err)
	}

	// Wait until the task is running inside the provider.
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("task never started")
	}

	if !s.CancelTask() {
		t.Fatal("CancelTask should report the running task was canceled")
	}

	// The canceled task fails with context.Canceled → SM error. This is
	// what makes terseio discard the buffered answer and what plainio
	// prints as the cancellation feedback.
	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(output.String(), "context canceled") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no SM error emitted after cancel; output: %q", output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Close the input pipe so run() sees EOF and exits.
	_ = w.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish after cancel")
	}
}

// toolThenBlockProvider emits one tool call, then blocks until ctx is
// canceled and fails with ctx.Err — like a provider whose stream is
// aborted after the model emitted a tool call. started closes when
// StreamMessages is called; toolExecuted closes when the tool's Execute
// runs.
type toolThenBlockProvider struct {
	started      chan struct{}
	toolExecuted chan struct{}
	once         sync.Once
}

func (m *toolThenBlockProvider) StreamMessages(ctx context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	m.once.Do(func() { close(m.started) })
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.ToolInputStartEvent{ID: "c1", Name: "exec", Key: "block:0"}, nil)
		yield(llm.ToolInputCompleteEvent{ID: "c1", Input: json.RawMessage(`{}`), Key: "block:0"}, nil)
		<-ctx.Done()
		yield(nil, ctx.Err())
	}, nil
}

func (m *toolThenBlockProvider) SetReasoningLevel(_ int)                       {}
func (m *toolThenBlockProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *toolThenBlockProvider) SetVideoConfig(_ int, _ int)                   {}

// TestCancelTask_KeepsExecutedToolResult verifies the end-to-end salvage
// chain: a tool executes before the task is canceled, and the session
// history keeps the [tool_use, tool_result] pair — so a later :continue
// resubmits a history consistent with the side effects that already
// happened. No "Canceled" marker is appended: the tool-result tail is a
// complete request shape (:continue resends it as-is, exactly like the
// agent's own step loop sends tool-result tails to the provider).
func TestCancelTask_KeepsExecutedToolResult(t *testing.T) {
	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()

	provider := &toolThenBlockProvider{
		started:      make(chan struct{}),
		toolExecuted: make(chan struct{}),
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		MaxSteps: 10,
		Tools: []llm.Tool{
			{
				Definition: llm.ToolDefinition{Name: "exec"},
				Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
					close(provider.toolExecuted)
					return []llm.ContentPart{&llm.TextPart{Text: "done"}}, nil
				},
			},
		},
	})

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider},
			SessionConfig: SessionConfig{
				Input:   r,
				Output:  output,
				NoDelta: true,
			},
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
	}
	s.mcpService = newMCPService(nil, output)
	s.Start()

	// Deliver a prompt.
	if err := tlv.WriteTLV(w, tlv.TagUserT, tlv.WrapID("1", "hello")); err != nil {
		t.Fatalf("write UT: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
		t.Fatalf("write UE: %v", err)
	}

	// Wait until the tool has executed (its result is in flight), then
	// cancel the task.
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("task never started")
	}
	select {
	case <-provider.toolExecuted:
	case <-time.After(time.Second):
		t.Fatal("tool never executed")
	}
	if !s.CancelTask() {
		t.Fatal("CancelTask should report the running task was canceled")
	}

	// Wait for the SM error, then EOF so run() exits.
	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(output.String(), "context canceled") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no SM error emitted after cancel; output: %q", output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	_ = w.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish after cancel")
	}

	// After run() exits, s.Contents is stable: user prompt + salvaged
	// [tool_use, tool_result] pair — no "Canceled" marker.
	if len(s.Contents) != 3 {
		t.Fatalf("Contents has %d parts, want 3 (prompt, tool_use, tool_result): %#v", len(s.Contents), s.Contents)
	}
	tc, ok := s.Contents[1].(*llm.ToolInputPart)
	if !ok || tc.ID != "c1" || tc.GetRole() != llm.RoleAssistant {
		t.Errorf("Contents[1] = %#v, want ToolInputPart c1 with assistant role", s.Contents[1])
	}
	tr, ok := s.Contents[2].(*llm.ToolOutputPart)
	if !ok || tr.ID != "c1" || tr.GetRole() != llm.RoleTool {
		t.Errorf("Contents[2] = %#v, want ToolOutputPart c1 with tool role", s.Contents[2])
	}
}
