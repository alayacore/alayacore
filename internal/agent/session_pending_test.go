package agent

// A prompt that arrives before the session is ready is held, not refused, and a
// prompt that arrives while something is already in flight is refused. These
// pin the split: "not ready yet" is a stage the session passes through (the
// client cannot see it, and a piped client has no way to retry), while "busy"
// means the session would need a queue to accept it.
//
// The second half of the file covers CE: the adapter's "no more input" frame,
// which is EOF's fact on a stream that stays open for commands.

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/tlv"
)

// readyProbeProvider records whether the session's ready frame was already on
// the wire when its first call arrives — the precise claim about ordering: a
// held prompt is started only after the frame it was waiting for has been
// written. It then fails the step, which ends the task; what the task produced
// is not what this test is about.
type readyProbeProvider struct {
	output     *syncOutput
	readyFirst bool
	started    chan struct{}
	once       sync.Once
}

func (p *readyProbeProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.once.Do(func() {
		p.readyFirst = strings.Contains(p.output.String(), `"state":"ready"`)
		close(p.started)
	})
	return nil, errors.New("probe: stop")
}

func (p *readyProbeProvider) SetReasoningLevel(_ int)                       {}
func (p *readyProbeProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (p *readyProbeProvider) SetVideoConfig(_ int, _ int)                   {}

// newDeferredPromptSession returns a session that is Initializing (MCP
// configured, init not settled), with the given provider behind an initialized
// agent so a prompt that is accepted really runs.
func newDeferredPromptSession(t *testing.T, output *syncOutput, provider llm.Provider) *Session {
	t.Helper()

	ms := newModelService(newModelManager(""), newRuntimeManager(""))
	ms.agent = llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 5})
	ms.provider = provider

	sessionCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: ms,
			SessionConfig: SessionConfig{
				Output:   output,
				NoDelta:  true,
				MaxSteps: 5,
			},
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    sessionCtx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing))
	return s
}

// A prompt submitted before readiness is held, then run once init settles — and
// the ready frame is already written by the time that run starts, so a client
// watching the stream never sees a task begin on a session it was told is not
// ready.
func TestDeferredPromptStartsAfterReady(t *testing.T) {
	output := &syncOutput{}
	probe := &readyProbeProvider{output: output, started: make(chan struct{})}
	s := newDeferredPromptSession(t, output, probe)

	s.submitPrompt([]llm.ContentPart{&llm.TextPart{Text: "hello"}})

	if s.pending == nil {
		t.Fatal("a prompt submitted before readiness should be held")
	}
	if s.activeTask != nil {
		t.Fatal("holding a prompt must not start a task")
	}

	// MCP init settles, and the session advances past it. HandleEvent is the
	// mcpService's own state change; going through handleMCPEvent would also
	// apply the init result, which resets the agent — throwing away the stub
	// provider the held prompt is about to run on.
	s.mcpService.HandleEvent(&mcp.InitEvent{Type: mcp.InitDone})
	s.syncState()

	if s.State() != SessionReady {
		t.Fatalf("State() = %v after init settled, want ready", s.State())
	}
	if s.pending != nil {
		t.Error("the held prompt should have been taken out of the slot")
	}
	if s.activeTask == nil {
		t.Fatal("the held prompt should have started a task")
	}

	select {
	case <-probe.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("the held prompt never reached the provider: %q", output.String())
	}
	if !probe.readyFirst {
		t.Errorf("the task started before the ready frame was written: %q", output.String())
	}
}

// The slot holds one prompt. A second one, submitted while the session is still
// initializing, is refused with the same code a task-in-flight prompt gets:
// accepting it would need a queue.
func TestSecondPromptWhileHoldingIsRefused(t *testing.T) {
	output := &syncOutput{}
	s := newDeferredPromptSession(t, output, &mockProviderStepFail{})

	s.submitPrompt([]llm.ContentPart{&llm.TextPart{Text: "first"}})
	s.submitPrompt([]llm.ContentPart{&llm.TextPart{Text: "second"}})

	got := output.String()
	if !strings.Contains(got, "MCP servers are still initializing") {
		t.Errorf("the second prompt should be refused as MCP_NOT_READY, got: %q", got)
	}
	if !strings.Contains(got, "prompt deferred") {
		t.Errorf("the first prompt should have been accepted, got: %q", got)
	}
	if len(s.pending) == 0 {
		t.Error("the slot should still hold the first prompt")
	}
}

// A prompt that arrives with a task already running is a BUSY error, not
// something to hold: it is the queue case, and this session has no queue.
func TestPromptWhileTaskRunningIsRefused(t *testing.T) {
	output := &syncOutput{}
	s := newDeferredPromptSession(t, output, &mockProviderStepFail{})

	s.state.Store(int32(SessionReady))
	s.activeTask = &taskHandle{}

	s.submitPrompt([]llm.ContentPart{&llm.TextPart{Text: "hello"}})

	got := output.String()
	if !strings.Contains(got, "A task is already running") {
		t.Errorf("expected BUSY, got: %q", got)
	}
	if s.pending != nil {
		t.Error("a BUSY prompt must not be held")
	}
}

// :cancel drops a prompt that was accepted but never started, and reports that
// it did something.
func TestCancelDropsHeldPrompt(t *testing.T) {
	s := &Session{runState: runState{pending: []llm.ContentPart{&llm.TextPart{Text: "held"}}}}

	if _, err := s.cancelTask(); err != nil {
		t.Fatalf("cancelTask() on a held prompt = %v, want success", err)
	}
	if s.pending != nil {
		t.Error("cancelTask() should clear the held prompt")
	}
}

// :quit drops the held prompt too: the session is leaving, and the exit
// condition waits for the slot to be empty.
func TestQuitDropsHeldPrompt(t *testing.T) {
	s := &Session{runState: runState{pending: []llm.ContentPart{&llm.TextPart{Text: "held"}}}}

	if _, err := s.handleQuit(""); err != nil {
		t.Fatalf("handleQuit() = %v, want success", err)
	}
	if s.pending != nil {
		t.Error(":quit should clear the held prompt")
	}
	if !s.quitting {
		t.Error(":quit should mark the session as quitting")
	}
}

// The exit condition in one place, since run() cannot be driven into every
// combination: an accepted prompt is in flight.
func TestShouldExit(t *testing.T) {
	tests := []struct {
		name   string
		state  runState
		noCtx  bool
		expect bool
	}{
		{"idle, input open", runState{}, false, false},
		{"input ended, nothing in flight", runState{inputEnded: true}, false, true},
		{"input ended, task running", runState{inputEnded: true, activeTask: &taskHandle{}}, false, false},
		{"input ended, prompt held", runState{inputEnded: true, pending: []llm.ContentPart{&llm.TextPart{Text: "x"}}}, false, false},
		{"quit accepted, nothing in flight", runState{quitting: true}, false, true},
		{"quit accepted, task running", runState{quitting: true, activeTask: &taskHandle{}}, false, false},
		{"quit accepted, prompt held", runState{quitting: true, pending: []llm.ContentPart{&llm.TextPart{Text: "x"}}}, false, false},
		{"task running, input open", runState{activeTask: &taskHandle{}}, false, false},
		{"context done", runState{quitting: false}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.noCtx {
				cancel()
			}
			s := &Session{
				runState:    tt.state,
				sharedState: sharedState{sessionCtx: ctx},
			}
			if got := s.shouldExit(); got != tt.expect {
				t.Errorf("shouldExit() = %v, want %v", got, tt.expect)
			}
		})
	}
}

// ============================================================================
// CE — the adapter has no more input
// ============================================================================

// CE flushes whatever is staged before it ends the input: a prompt sent as
// content frames and then ended is one message, not a lost one.
func TestInputFrame_InputEndFlushesStagedFirst(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(tlv.TagUserT, "hello", nil)
	staged = s.handleInputFrame(tlv.TagInputEnd, "", staged)
	if staged != nil {
		t.Errorf("CE should clear staged content, got %d parts", len(staged))
	}

	// The prompt first, then the end — one goroutine on one channel is FIFO.
	first := readInputMsg(t, s)
	if first.inputEnd {
		t.Fatal("CE must not be seen before the content it flushed")
	}
	if len(first.contentParts) != 1 {
		t.Fatalf("flushed message has %d parts, want 1", len(first.contentParts))
	}
	second := readInputMsg(t, s)
	if !second.inputEnd {
		t.Error("CE should report the end of input")
	}
}

// CE with nothing staged is just the end.
func TestInputFrame_InputEndAlone(t *testing.T) {
	s := newInputFrameSession()

	s.handleInputFrame(tlv.TagInputEnd, "", nil)

	msg := readInputMsg(t, s)
	if !msg.inputEnd || msg.isCmd || len(msg.contentParts) != 0 {
		t.Errorf("CE produced %+v, want an input-end marker alone", msg)
	}
}

// A CE frame ends the run the way EOF does — and, like EOF, it does not cut
// short a task that is still running.
func TestInputEndFrame_EndsTheRun(t *testing.T) {
	s, _, w, cleanup := newQuitTestSession(t, true)
	defer cleanup()

	// The session is running a task; CE says the input is over.
	if err := tlv.WriteTLV(w, tlv.TagInputEnd, ""); err != nil {
		t.Fatalf("write CE: %v", err)
	}
	select {
	case <-s.Done():
		t.Fatal("run() exited with a task still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	// The task finishes; there is nothing else to wait for.
	s.taskResultCh <- nil
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit after the task finished")
	}
}

// After CE the stream is still usable for commands — that is what makes it a
// half-close rather than a closed pipe, and it is what an OAuth callback needs
// (it submits its :mcp_confirm on the pipe that just ended).
func TestInputEndFrame_CommandsStillFlow(t *testing.T) {
	// A running task keeps the session in its loop, so the conversation is
	// still going when the input ends.
	_, output, w, cleanup := newQuitTestSession(t, true)
	defer cleanup()

	if err := tlv.WriteTLV(w, tlv.TagInputEnd, ""); err != nil {
		t.Fatalf("write CE: %v", err)
	}

	path := t.TempDir() + "/after-ce.alaya"
	if err := tlv.WriteTLV(w, tlv.TagCommandIn,
		`{"id":"c1","name":"save","input":"`+path+`"}`); err != nil {
		t.Fatalf("write CI after CE: %v", err)
	}

	waitForOutput(t, output, `"id":"c1"`)
	if !strings.Contains(output.String(), "after-ce.alaya") {
		t.Errorf("the command after CE was not answered with its result: %q", output.String())
	}
}

// EOF and CE are the same fact, so a reader that ends is enough to end the run:
// nothing has to close a writer for the session to know.
func TestInputEOF_EndsTheRun(t *testing.T) {
	s, _, w, cleanup := newQuitTestSession(t, false)
	defer cleanup()

	if err := w.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit after EOF")
	}
}
