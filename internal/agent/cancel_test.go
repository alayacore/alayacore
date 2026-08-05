package agent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
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
