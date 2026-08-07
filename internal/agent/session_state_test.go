package agent

// Session lifecycle state tests — SessionState transitions and the
// prepareTask gate.
//
// The lifecycle is: Starting (construction; load + replay complete by
// definition) → Initializing (run() started, MCP init pending) → Ready
// (MCP init settled: done / canceled / aborted, or never configured).
// Agent/provider creation is lazy and NOT part of this state.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
)

// waitForState polls s.State() until it reaches want or the deadline passes.
func waitForState(t *testing.T, s *Session, want SessionState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state = %v, want %v", s.State(), want)
}

// countSessionReadyFrames returns how many SM "session" frames with
// state "ready" appear in the captured output.
func countSessionReadyFrames(output *MockOutput) int {
	count := 0
	for _, m := range output.Messages {
		if strings.Contains(m, `"type":"session"`) && strings.Contains(m, `"state":"ready"`) {
			count++
		}
	}
	return count
}

func TestSessionState_String(t *testing.T) {
	cases := []struct {
		state SessionState
		want  string
	}{
		{SessionStarting, "starting"},
		{SessionInitializing, "initializing"},
		{SessionReady, "ready"},
		{SessionState(99), "SessionState(99)"},
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("SessionState(%d).String() = %q, want %q", int(c.state), got, c.want)
		}
	}
}

// A fresh session starts in SessionStarting — session load + replay are
// synchronous and complete by construction (LoadOrNewSession).
func TestSessionState_NewSessionStartsStarting(t *testing.T) {
	s, _, err := LoadOrNewSession(SessionConfig{
		Input:  &nopInput{},
		Output: &nopOutput{},
	})
	if err != nil {
		t.Fatalf("LoadOrNewSession: %v", err)
	}
	if got := s.State(); got != SessionStarting {
		t.Errorf("State() = %v, want starting", got)
	}
	if s.IsInitialized() {
		t.Error("IsInitialized() = true before Start(), want false")
	}
}

// A restored session starts in SessionStarting for the same reason.
func TestSessionState_RestoreStartsStarting(t *testing.T) {
	s := RestoreFromSession(SessionConfig{
		Input:  &nopInput{},
		Output: &nopOutput{},
	}, &sessionData{})
	if got := s.State(); got != SessionStarting {
		t.Errorf("State() = %v, want starting", got)
	}
	if s.IsInitialized() {
		t.Error("IsInitialized() = true before Start(), want false")
	}
}

// Without MCP configured, run() transitions straight to Ready.
func TestSessionState_StartWithoutMCPReady(t *testing.T) {
	output := &MockOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &nopInput{},
				Output: output,
			},
		},
		runState: runState{
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
	waitForState(t, s, SessionReady)
	if !s.IsInitialized() {
		t.Error("IsInitialized() = false after MCP-less Start(), want true")
	}
	// run() writes the ready frame before exiting — wait for Done() so the
	// broadcast is complete before inspecting MockOutput (not thread-safe).
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit")
	}
	// Without MCP, run() broadcasts the ready frame at startup.
	if got := countSessionReadyFrames(output); got != 1 {
		t.Errorf("session-ready frames = %d, want exactly 1", got)
	}
}

// With MCP configured, run() stays Initializing until the init settles;
// the InitDone event transitions the session to Ready.
func TestSessionState_MCPInitDoneTransitionsToReady(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{},
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing)) // simulate run() initial phase

	s.handleMCPEvent(&mcp.InitEvent{Type: mcp.InitDone})

	if got := s.State(); got != SessionReady {
		t.Errorf("State() = %v after InitDone, want ready", got)
	}
	if got := countSessionReadyFrames(output); got != 1 {
		t.Errorf("session-ready frames = %d, want exactly 1", got)
	}
}

// User-canceled MCP init also settles the session to Ready.
func TestSessionState_MCPCanceledTransitionsToReady(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{},
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing)) // simulate run() initial phase

	s.handleMCPEvent(&mcp.InitEvent{Type: mcp.InitCanceled})

	if got := s.State(); got != SessionReady {
		t.Errorf("State() = %v after canceled, want ready", got)
	}
	if got := countSessionReadyFrames(output); got != 1 {
		t.Errorf("session-ready frames = %d, want exactly 1", got)
	}
}

// MarkAborted + syncState mirrors the run() channel-close branch:
// the events channel closed without a clean event — the session still
// settles to Ready so the user can proceed.
func TestSessionState_MarkAbortedTransitionsToReady(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing)) // simulate run() initial phase

	s.mcpService.MarkAborted()
	s.syncState()

	if got := s.State(); got != SessionReady {
		t.Errorf("State() = %v after MarkAborted, want ready", got)
	}
	if got := countSessionReadyFrames(output); got != 1 {
		t.Errorf("session-ready frames = %d, want exactly 1", got)
	}
}

// syncState is idempotent: events that don't settle MCP init leave the
// session in Initializing.
func TestSessionState_SyncStateIgnoresInProgressEvents(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing)) // simulate run() initial phase

	s.syncState()

	if got := s.State(); got != SessionInitializing {
		t.Errorf("State() = %v before init settles, want initializing", got)
	}
	if got := countSessionReadyFrames(output); got != 0 {
		t.Errorf("session-ready frames = %d before init settles, want 0", got)
	}
}

// The ready broadcast fires exactly once per session, no matter how many
// times the state is re-synced or re-set.
func TestSessionState_ReadyBroadcastExactlyOnce(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{},
	}
	s.mcpService = newMCPService(&mcp.Initializer{}, output)
	s.state.Store(int32(SessionInitializing)) // simulate run() initial phase

	s.syncState()            // Initializing → Ready: broadcast #1
	s.syncState()            // idempotent — no second broadcast
	s.setState(SessionReady) // no-op — phase unchanged

	if got := countSessionReadyFrames(output); got != 1 {
		t.Errorf("session-ready frames = %d, want exactly 1", got)
	}
	if !s.IsInitialized() {
		t.Error("IsInitialized() = false after ready broadcast, want true")
	}
}

// prepareTask gates on the lifecycle state, not on MCP internals.
func TestPrepareTask_GatedOnSessionState(t *testing.T) {
	output := &MockOutput{}
	ms := newModelService(newModelManager(""), newRuntimeManager(""))
	ms.agent = &llm.Agent{}
	ms.provider = &mockProviderStepFail{}

	sessionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{
		sessionConfig: sessionConfig{
			modelService: ms,
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
		sharedState: sharedState{sessionCtx: sessionCtx},
	}

	// SessionStarting → rejected with MCP_NOT_READY (wire-stable code).
	_, err := s.prepareTask()
	var ce *cmdErr
	if !errors.As(err, &ce) || ce.Code != "MCP_NOT_READY" {
		t.Fatalf("prepareTask() error = %v, want cmdErr MCP_NOT_READY", err)
	}

	// SessionReady → accepted.
	s.state.Store(int32(SessionReady))
	ctx, err := s.prepareTask()
	if err != nil {
		t.Fatalf("prepareTask() after ready: %v", err)
	}
	if ctx == nil {
		t.Fatal("prepareTask() returned nil context")
	}
	if s.activeTask == nil {
		t.Error("activeTask should be set after successful prepareTask")
	}
}
