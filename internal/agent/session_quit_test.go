package agent

// :quit tests — the session-owned shutdown path (C2a).
//
// :quit is a session command, not an adapter-local string comparison: it
// sets runState.quitting, prepareTask refuses new work with SHUTTING_DOWN,
// and run() returns once nothing is in flight — a task that is already
// running still finishes.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// newQuitTestSession builds a started Session whose run() loop is live and
// whose output is captured thread-safely (the test writes input frames while
// run() writes output). The input pipe stays open unless the caller closes it,
// so EOF can never be what ends the run. withTask pre-installs a running task,
// as newCancelTestSession does.
func newQuitTestSession(t *testing.T, withTask bool) (*Session, *syncOutput, *io.PipeWriter, func()) {
	t.Helper()

	output := &syncOutput{}
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
	if withTask {
		// run() only reads activeTask while the task is in flight; the test
		// owns it until the simulated completion below.
		s.activeTask = &taskHandle{}
	}

	s.mcpService = newMCPService(nil, output)
	s.Start()

	cleanup := func() {
		_ = w.Close() // unblocks inputPump
		cancel()      // stops run()
	}
	return s, output, w, cleanup
}

// sendQuit writes the CI frame an adapter sends for ":quit".
func sendQuit(t *testing.T, w io.Writer, id string) {
	t.Helper()
	payload, err := json.Marshal(protocol.CmdMsg{ID: id, Name: commands.CommandNameQuit})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagCommandIn, string(payload)); err != nil {
		t.Fatalf("write CI: %v", err)
	}
}

// waitForOutput polls the thread-safe output until substr appears.
func waitForOutput(t *testing.T, output *syncOutput, substr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), substr) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output never contained %q; got %q", substr, output.String())
}

// An idle :quit ends the run, and the command is answered like any other
// immediate command instead of being reported as an error.
func TestQuit_IdleEndsTheRun(t *testing.T) {
	s, output, w, cleanup := newQuitTestSession(t, false)
	defer cleanup()

	sendQuit(t, w, "q1")

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit after :quit")
	}

	joined := output.String()
	if !strings.Contains(joined, `"id":"q1"`) || !strings.Contains(joined, `"output":null`) {
		t.Errorf("quit CO missing: %q", joined)
	}
	if strings.Contains(joined, "is_error") {
		t.Errorf(":quit should succeed, got error CO: %q", joined)
	}
}

// :quit while a task is in flight is accepted (immediate policy — the
// semantic is "wait for it"), answers its CO, and leaves run() alive until
// the task finishes: exiting early would drop the work the user asked for.
func TestQuit_WaitsForRunningTask(t *testing.T) {
	s, output, w, cleanup := newQuitTestSession(t, true)
	defer cleanup()

	sendQuit(t, w, "q2")
	waitForOutput(t, output, `"id":"q2"`)

	if strings.Contains(output.String(), "BUSY") {
		t.Fatalf(":quit must be allowed while a task runs: %q", output.String())
	}
	select {
	case <-s.Done():
		t.Fatal("run() exited with a task still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	// The task finishes — run() now sees nothing in flight and exits.
	s.taskResultCh <- nil
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit after the last task finished")
	}
}

// A prompt that arrives after :quit is refused: accepting it would start
// work the session is about to drop.
func TestHandleInputMsg_PromptAfterQuitIsRejected(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		runState: runState{quitting: true},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	s.handleInputMsg(inputMsg{contentParts: []llm.ContentPart{&llm.TextPart{Text: "hi"}}})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, "the session is shutting down") {
		t.Errorf("expected SHUTTING_DOWN error, got: %q", joined)
	}
	if s.activeTask != nil {
		t.Error("no task should have started after :quit")
	}
}

// :quit takes no arguments; a stray one is a usage error, and must not end
// the session.
func TestHandleInputMsg_QuitWithArguments(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	s.handleInputMsg(inputMsg{isCmd: true, cmd: commands.CommandNameQuit, cmdInput: "now", cmdID: "q3"})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"INVALID_ARGS"`) {
		t.Errorf("expected INVALID_ARGS for ':quit now', got: %q", joined)
	}
	if s.quitting {
		t.Error("a rejected :quit must not end the session")
	}
}

// The :q alias reaches the same handler — the alias table lives with the
// command names (commands.Canonical), not in the dispatcher.
func TestHandleInputMsg_QuitAlias(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	s.handleInputMsg(inputMsg{isCmd: true, cmd: "q", cmdID: "q4"})

	if !s.quitting {
		t.Error(":q should have ended the session")
	}
	joined := strings.Join(output.Messages, "")
	if strings.Contains(joined, "UNKNOWN_COMMAND") {
		t.Errorf(":q should resolve to quit, got: %q", joined)
	}
}
