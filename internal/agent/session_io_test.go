package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/wiretest"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// newInputFrameSession returns a minimal Session whose inputMsgCh is
// buffered so handleInputFrame can be tested without a running run().
func newInputFrameSession() *Session {
	return &Session{
		runState: runState{
			inputMsgCh: make(chan inputMsg, 16),
		},
	}
}

// readInputMsg drains at most one inputMsg from the channel, failing the
// test if none is available.
func readInputMsg(t *testing.T, s *Session) inputMsg {
	t.Helper()
	select {
	case msg := <-s.inputMsgCh:
		return msg
	default:
		t.Fatal("expected an inputMsg on inputMsgCh, got none")
		return inputMsg{}
	}
}

func TestInputFrame_CommandIn_Valid(t *testing.T) {
	s := newInputFrameSession()

	cmd := protocol.CmdMsg{CommandID: "a1b2", Name: "save", Args: "/tmp/x.alaya"}
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	staged := s.handleInputFrame(wire.TagCommandIn, string(payload), nil)
	if staged != nil {
		t.Errorf("CI frame should not stage content, got %d parts", len(staged))
	}

	msg := readInputMsg(t, s)
	if !msg.isCmd {
		t.Error("expected isCmd=true")
	}
	if msg.cmd != "save" {
		t.Errorf("cmd = %q, want %q", msg.cmd, "save")
	}
	if msg.args != "/tmp/x.alaya" {
		t.Errorf("args = %q, want %q", msg.args, "/tmp/x.alaya")
	}
	if msg.commandID != "a1b2" {
		t.Errorf("commandID = %q, want %q", msg.commandID, "a1b2")
	}
	if msg.contentParts != nil {
		t.Errorf("command should have no contentParts, got %d", len(msg.contentParts))
	}
}

func TestInputFrame_CommandIn_NoInput(t *testing.T) {
	s := newInputFrameSession()

	cmd := protocol.CmdMsg{CommandID: "c3d4", Name: "cancel"}
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	s.handleInputFrame(wire.TagCommandIn, string(payload), nil)

	msg := readInputMsg(t, s)
	if msg.cmd != "cancel" || msg.args != "" {
		t.Errorf("unexpected command fields: cmd=%q args=%q", msg.cmd, msg.args)
	}
}

func TestInputFrame_CommandIn_InvalidJSON(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(wire.TagCommandIn, "{not json", nil)
	if staged != nil {
		t.Errorf("invalid CI should not stage content")
	}

	msg := readInputMsg(t, s)
	if msg.err == nil {
		t.Fatal("expected err for invalid CI JSON")
	}
	if !strings.Contains(msg.err.Error(), "invalid command frame") {
		t.Errorf("unexpected error: %v", msg.err)
	}
}

// A CI that arrives with content still staged is a readable request that is
// refused before dispatch: the agent has an id to echo, so the refusal travels
// as a CO (the request exists — the answer is no) rather than as a session error.
func TestInputFrame_CommandIn_WithStagedContent(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(wire.TagUserX, wiretest.UserText("", "hello"), nil)
	if len(staged) != 1 {
		t.Fatalf("expected 1 staged part, got %d", len(staged))
	}

	cmd := protocol.CmdMsg{CommandID: "x", Name: "save"}
	payload, _ := json.Marshal(cmd)
	staged = s.handleInputFrame(wire.TagCommandIn, string(payload), staged)
	if staged != nil {
		t.Errorf("CI with staged content should return nil staged")
	}

	msg := readInputMsg(t, s)
	if msg.err != nil {
		t.Fatalf("a readable CI must not be a frame error: %v", msg.err)
	}
	if msg.reject == nil {
		t.Fatal("expected the request to be refused before dispatch")
	}
	if msg.commandID != "x" {
		t.Errorf("commandID = %q, want the id the CI carried", msg.commandID)
	}
	if msg.reject.Code != "REQUEST_INVALID_ARGS" {
		t.Errorf("reject code = %q, want REQUEST_INVALID_ARGS", msg.reject.Code)
	}
}

// An empty command_id is not a legal request: without it nothing can be
// correlated back, and running the command would produce a side effect the
// adapter cannot match to anything it sent. The frame is refused on the session
// plane, because a CO with no id is the frame this rule exists to prevent.
func TestInputFrame_CommandIn_MissingCommandID(t *testing.T) {
	s := newInputFrameSession()

	payload, _ := json.Marshal(protocol.CmdMsg{Name: "save", Args: "/tmp/x.alaya"})
	staged := s.handleInputFrame(wire.TagCommandIn, string(payload), nil)
	if staged != nil {
		t.Errorf("refused CI should not stage content")
	}

	msg := readInputMsg(t, s)
	if msg.err == nil {
		t.Fatal("expected a frame-level error for a CI without command_id")
	}
	if !strings.Contains(msg.err.Error(), "command_id") {
		t.Errorf("error should name the missing field: %v", msg.err)
	}
	if msg.isCmd || msg.commandID != "" {
		t.Errorf("a refused CI must not be dispatched: %+v", msg)
	}
}

func TestInputFrame_UserText_NoCommandSniffing(t *testing.T) {
	// UT no longer carries commands: ":cancel" is plain user text now.
	s := newInputFrameSession()

	staged := s.handleInputFrame(wire.TagUserX, wiretest.UserText("", ":cancel"), nil)
	if len(staged) != 1 {
		t.Fatalf("expected 1 staged part, got %d", len(staged))
	}
	tp, ok := staged[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", staged[0])
	}
	if tp.Text != ":cancel" {
		t.Errorf("text = %q, want %q", tp.Text, ":cancel")
	}

	// No command message should be emitted.
	select {
	case msg := <-s.inputMsgCh:
		t.Errorf("unexpected inputMsg: %+v", msg)
	default:
	}
}

func TestInputFrame_UserText_ThenEnd(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(wire.TagUserX, wiretest.UserText("", "hello"), nil)
	staged = s.handleInputFrame(wire.TagUserEnd, "", staged)
	if staged != nil {
		t.Errorf("UE should clear staged content, got %d parts", len(staged))
	}

	msg := readInputMsg(t, s)
	if msg.isCmd {
		t.Error("expected prompt, not command")
	}
	if len(msg.contentParts) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(msg.contentParts))
	}
}

// ============================================================================
// Command output (CO) tests
// ============================================================================

// newCmdOutputSession returns a Session whose Output captures raw frame bytes.
func newCmdOutputSession() (*Session, *MockOutput) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}
	return s, output
}

func TestWriteCmdResult_Success(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("a1b2", map[string]any{"path": "/tmp/x.alaya"}, nil)

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"command_id":"a1b2"`) {
		t.Errorf("missing command id in CO: %s", joined)
	}
	if !strings.Contains(joined, `"path":"/tmp/x.alaya"`) {
		t.Errorf("missing result in CO: %s", joined)
	}
	if strings.Contains(joined, "is_error") {
		t.Errorf("success CO should not carry is_error: %s", joined)
	}
}

func TestWriteCmdResult_SuccessNoResult(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("zzz", nil, nil)

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"result":null`) {
		t.Errorf("fire-and-forget success should have result:null: %s", joined)
	}
	if strings.Contains(joined, "is_error") {
		t.Errorf("success CO should not carry is_error: %s", joined)
	}
}

func TestWriteCmdResult_CmdErr(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("c3d4", nil, &cmdErr{Code: "REQUEST_MODEL_UNKNOWN", Message: "model_set: model not found: 99"})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"is_error":true`) {
		t.Errorf("missing is_error in CO: %s", joined)
	}
	if !strings.Contains(joined, `"code":"REQUEST_MODEL_UNKNOWN"`) {
		t.Errorf("missing error code in CO: %s", joined)
	}
	if !strings.Contains(joined, `"message":"model_set: model not found: 99"`) {
		t.Errorf("missing error message in CO: %s", joined)
	}
}

// A handler that returns a bare error is a bug — every refusal in this tree
// names its cause — so the fallback says exactly that much and no more.
func TestWriteCmdResult_PlainErrorDefaultsToUnclassified(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("e5f6", nil, fmt.Errorf("boom"))

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"INTERNAL_UNCLASSIFIED"`) {
		t.Errorf("plain error should default to code INTERNAL_UNCLASSIFIED: %s", joined)
	}
	if !strings.Contains(joined, `"is_error":true`) {
		t.Errorf("missing is_error in CO: %s", joined)
	}
}

func TestHandleInputMsg_UnknownCommand(t *testing.T) {
	s, output := newCmdOutputSession()
	s.inputMsgCh = make(chan inputMsg, 1) // not used here, keeps Session consistent

	s.handleInputMsg(inputMsg{isCmd: true, cmd: "nope", commandID: "x1"})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"REQUEST_UNKNOWN_COMMAND"`) {
		t.Errorf("expected UNKNOWN_COMMAND in CO: %s", joined)
	}
	if !strings.Contains(joined, `"command_id":"x1"`) {
		t.Errorf("expected echoed id in CO: %s", joined)
	}
}

// A frame-level failure is not a command result. The command plane answers one
// CI each, and a malformed frame is not one — least of all a CI whose id is the
// part that could not be read. So the error goes out on the session plane, and
// no CO is written at all.
func TestHandleInputMsg_FrameErrorGoesToSessionPlane(t *testing.T) {
	s, output := newCmdOutputSession()

	s.handleInputMsg(inputMsg{err: fmt.Errorf("invalid command frame: boom")})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `SM{"type":"error","data":{"text":"invalid command frame: boom"}}`) {
		t.Errorf("expected an SM error: %s", joined)
	}
	if strings.Contains(joined, "CO{") {
		t.Errorf("a frame-level failure must not be answered with a CO: %s", joined)
	}
}

// The refusal path: a readable request that is not dispatched still gets its
// CO, carrying the id and the reason, and the command does not run.
func TestHandleInputMsg_RejectAnswersWithCOAndDoesNotRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.alaya")
	output := &MockOutput{}
	s := &Session{sessionConfig: sessionConfig{
		SessionConfig: SessionConfig{Output: output, SessionFile: path},
	}}
	s.inputMsgCh = make(chan inputMsg, 1)

	s.handleInputMsg(inputMsg{
		isCmd:     true,
		cmd:       "save",
		args:      path,
		commandID: "z1",
		reject:    &cmdErr{Code: "REQUEST_INVALID_ARGS", Message: "command can not be sent with staged content"},
	})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"command_id":"z1"`) || !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
		t.Errorf("expected a CO refusal echoing the id: %s", joined)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused command must not run (the session file was written)")
	}
}

func TestHandleModelLoad_ValidationErrorFails(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")
	badConfig := `name: "Bad Model"
protocol_type: "unknown_type"
base_url: ":://invalid"
model_name: ""
`
	if err := os.WriteFile(configPath, []byte(badConfig), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: newModelService(newModelManager(configPath), newRuntimeManager("")),
			SessionConfig: SessionConfig{
				Output: &MockOutput{},
			},
		},
	}

	_, err := s.handleModelLoad()
	if err == nil {
		t.Fatal("model_load with rejected model blocks must fail")
	}
	var ce *cmdErr
	if !errors.As(err, &ce) || ce.Code != "CONFIG_MODEL_INVALID" {
		t.Errorf("expected CONFIG_MODEL_INVALID, got %+v", err)
	}
	if !strings.Contains(ce.Message, "unknown protocol_type") {
		t.Errorf("validation error should mention the rejected model: %s", ce.Message)
	}
}

// ============================================================================
// Async task commands (continue/summarize) — CO started + taskMsg command_id
// ============================================================================

func TestStartTaskCommand_Success(t *testing.T) {
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
		sharedState: sharedState{
			sessionCtx: sessionCtx,
		},
	}
	// startTaskCommand goes through beginTask, which gates on the
	// lifecycle state — simulate a session whose init has completed.
	s.state.Store(int32(SessionReady))

	started := make(chan struct{})
	s.startTaskCommand("x1", func(context.Context) { close(started) })

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"status":"started"`) {
		t.Errorf("expected CO started response: %s", joined)
	}
	if s.activeTask == nil || s.activeTask.commandID != "x1" {
		t.Errorf("activeTask should carry commandID x1, got %+v", s.activeTask)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Error("task goroutine should have started")
	}
}

// TestStartTaskCommand_BroadcastsTaskStart pins the task-acceptance broadcast
// on the command path: the running frame is written, carries the command id,
// and is written before the task goroutine runs. (Why the frame must exist is
// beginTask's comment; the end-to-end behavior is covered by the entry-point
// tests in summarize_status_test.go.)
func TestStartTaskCommand_BroadcastsTaskStart(t *testing.T) {
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
				Output:   output,
				MaxSteps: 5,
			},
		},
		sharedState: sharedState{sessionCtx: sessionCtx},
	}
	s.state.Store(int32(SessionReady))
	// A prior task left its final speed behind; accepting the new task must
	// clear it so the in-progress frame carries no stale telemetry.
	s.lastStepTPS = 42
	s.lastTTFTMS = 100

	// Hold the task goroutine until the output has been inspected: the
	// start frame is written synchronously, before the goroutine starts.
	release := make(chan struct{})
	done := make(chan struct{})
	s.startTaskCommand("x9", func(context.Context) {
		<-release
		close(done)
	})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"in_progress":true`) {
		t.Fatalf("task start must broadcast in_progress:true: %s", joined)
	}
	if !strings.Contains(joined, `"command_id":"x9"`) {
		t.Fatalf("task start frame must carry the command id: %s", joined)
	}
	if strings.Contains(joined, `"step_tps":`) || strings.Contains(joined, `"ttft_ms":`) {
		t.Errorf("task start frame must not carry the previous task's speed: %s", joined)
	}
	// The CO ack and the running-state SM are projections of the same
	// acceptance onto two independent planes; their relative order is not
	// asserted because it is not part of the contract.
	close(release)
	<-done
}

// TestStartTaskCommand_RefusedDoesNotBroadcast: every early exit of beginTask
// is a refusal, and a refusal must be inert — answered with a CO error, no task
// frame, and no disturbance to the running/idle state (the task slot is not
// allocated, the previous task's speed is not cleared). It enumerates all four
// exits: quitting, not-ready, busy, and agent initialization failure.
func TestStartTaskCommand_RefusedDoesNotBroadcast(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*Session)
		wantCode string
	}{
		{
			name: "quitting",
			setup: func(s *Session) {
				s.state.Store(int32(SessionReady))
				s.quitting = true
			},
			wantCode: "STATE_SHUTTING_DOWN",
		},
		{
			name:     "not_ready",
			setup:    func(s *Session) { s.state.Store(int32(SessionStarting)) },
			wantCode: "STATE_MCP_NOT_READY",
		},
		{
			name: "busy",
			setup: func(s *Session) {
				s.state.Store(int32(SessionReady))
				s.activeTask = &taskHandle{}
			},
			wantCode: "STATE_BUSY",
		},
		{
			name: "agent_init_failed",
			setup: func(s *Session) {
				s.state.Store(int32(SessionReady))
				// No active model: ensureAgentInitialized cannot build an agent.
				s.modelService = newModelService(newModelManager(""), newRuntimeManager(""))
			},
			wantCode: "CONFIG_MISSING",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := &MockOutput{}
			s := &Session{
				sessionConfig: sessionConfig{
					SessionConfig: SessionConfig{Output: output},
				},
				// A live task's telemetry: a refusal must leave it alone.
				runState: runState{lastStepTPS: 42, lastTTFTMS: 7},
			}
			tc.setup(s)
			slotBefore := s.activeTask

			s.startTaskCommand("x2", func(context.Context) {})

			joined := strings.Join(output.Messages, "")
			if !strings.Contains(joined, `"code":"`+tc.wantCode+`"`) {
				t.Errorf("expected %s error in CO: %s", tc.wantCode, joined)
			}
			if !strings.Contains(joined, `"command_id":"x2"`) {
				t.Errorf("expected echoed id in CO: %s", joined)
			}
			// No task frame at all: neither in_progress:true (the indicator must
			// not task for work that is not running) nor a spurious idle frame.
			if frames := taskFrames(t, joined); len(frames) != 0 {
				t.Errorf("a refused task must not emit a task frame: %v (%s)", frames, joined)
			}
			if s.activeTask != slotBefore {
				t.Errorf("a refused command must not allocate the task slot: %v -> %v", slotBefore, s.activeTask)
			}
			if s.lastStepTPS != 42 || s.lastTTFTMS != 7 {
				t.Errorf("a refused command must not clear a live task's speed: tps=%v ttft=%v", s.lastStepTPS, s.lastTTFTMS)
			}
		})
	}
}

func TestSendTaskMsg_RunningTaskCarriesCommandID(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		runState: runState{
			activeTask: &taskHandle{commandID: "x1", step: 2},
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
	}

	s.sendTaskMsg()

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"command_id":"x1"`) {
		t.Errorf("running taskMsg should carry command_id: %s", joined)
	}
	if !strings.Contains(joined, `"in_progress":true`) {
		t.Errorf("running taskMsg should show in_progress:true: %s", joined)
	}
}

func TestHandleTaskDone_CompletionTaskMsgCarriesCommandID(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		runState: runState{
			activeTask: &taskHandle{commandID: "x1"},
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
	}

	s.handleTaskDone(nil)

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"command_id":"x1"`) {
		t.Errorf("completion taskMsg should carry command_id: %s", joined)
	}
	if !strings.Contains(joined, `"in_progress":false`) {
		t.Errorf("completion taskMsg should show in_progress:false: %s", joined)
	}
	if s.activeTask != nil {
		t.Error("activeTask should be cleared after task done")
	}
}

// newSessionWithConfirmChannels returns a Session with a pre-populated
// confirmChs map, simulating pending tool confirmations from a task.
func newSessionWithConfirmChannels() *Session {
	s := &Session{
		sharedState: sharedState{
			confirmChs: make(map[string]chan bool),
		},
	}
	s.confirmMu.Lock()
	s.confirmChs["call_1"] = make(chan bool, 1)
	s.confirmChs["call_2"] = make(chan bool, 1)
	s.confirmMu.Unlock()
	return s
}

// TestCleanupConfirmChannels_RemovesAll verifies that leftover
// confirmation channels (from a canceled task the user never answered)
// are removed after the task finishes.
func TestCleanupConfirmChannels_RemovesAll(t *testing.T) {
	s := newSessionWithConfirmChannels()

	s.cleanupConfirmChannels()

	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if len(s.confirmChs) != 0 {
		t.Fatalf("confirmChs has %d entries after cleanup, want 0", len(s.confirmChs))
	}
}

// TestCleanupConfirmChannels_AfterResolved verifies cleanup only removes
// leftovers: channels already answered via :tool_confirm/:tool_decline
// are gone before cleanup runs, and nothing is double-deleted.
func TestCleanupConfirmChannels_AfterResolved(t *testing.T) {
	s := newSessionWithConfirmChannels()

	// User responds to one confirmation — resolveToolConfirm removes it.
	if _, err := s.resolveToolConfirm("call_1", true); err != nil {
		t.Fatalf("resolveToolConfirm() error = %v", err)
	}

	s.cleanupConfirmChannels()

	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if len(s.confirmChs) != 0 {
		t.Fatalf("confirmChs has %d entries after cleanup, want 0", len(s.confirmChs))
	}
}

// TestCleanupConfirmChannels_EmptyMapSafe verifies cleanup is a no-op
// when there are no pending confirmations.
func TestCleanupConfirmChannels_EmptyMapSafe(t *testing.T) {
	s := &Session{
		sharedState: sharedState{
			confirmChs: make(map[string]chan bool),
		},
	}

	s.cleanupConfirmChannels() // must not panic

	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if len(s.confirmChs) != 0 {
		t.Fatalf("confirmChs has %d entries, want 0", len(s.confirmChs))
	}
}

// TestHandleTaskDone_CleansUpConfirmChannels verifies the integration
// path: handleTaskDone drops leftover confirmation channels.
func TestHandleTaskDone_CleansUpConfirmChannels(t *testing.T) {
	s := newSessionWithConfirmChannels()
	s.sessionConfig = sessionConfig{
		SessionConfig: SessionConfig{
			Output: &MockOutput{},
		},
	}

	s.handleTaskDone(nil)

	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if len(s.confirmChs) != 0 {
		t.Fatalf("confirmChs has %d entries after handleTaskDone, want 0", len(s.confirmChs))
	}
}

// ":save <filename>" takes the whole tail as the path, trimmed of surrounding
// whitespace: a trailing space would otherwise write a file whose name the
// user can neither see nor retype.
func TestSaveSessionTrimsThePath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "my report.alaya")
	s := newSaveSession()

	res, err := s.saveSession("  " + want + "  ")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := res.(map[string]any)["path"]; got != want {
		t.Errorf("path = %v, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no file at the path the command named: %v", err)
	}
	if _, err := os.Stat(want + " "); err == nil {
		t.Error("a file whose name ends in a space was written")
	}
}

// Whitespace is not a filename. An argument of spaces only means "no argument",
// so the configured session file is used — the same choice a typed ":save "
// makes, where the separator is trimmed before the handler sees it.
func TestSaveSessionWhitespaceOnlyUsesTheSessionFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.alaya")
	s := newSaveSession()
	s.SessionFile = path

	res, err := s.saveSession("   ")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := res.(map[string]any)["path"]; got != path {
		t.Errorf("path = %v, want the session file %q", got, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the session file was not written: %v", err)
	}
}

// A command whose shape declares an argument this test does not supply takes no
// argument at all: a stray word is a typo, and ignoring it hides the mistake.
// `:quit` has always refused one; the rest used to accept and discard it.
// `:cancel` is deliberately absent — its one declared word is covered by
// TestCancelRejectsAnUndeclaredSteeringFate, where a wrong value and a wrong
// count are told apart.
func TestZeroArgumentCommandsRejectArguments(t *testing.T) {
	names := []string{
		commands.CommandNameQuit,
		commands.CommandNameSummarize,
		commands.CommandNameContinue,
		commands.CommandNameModelLoad,
		commands.CommandNameMCPSkip,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			output := &MockOutput{}
			s := &Session{sessionConfig: sessionConfig{
				SessionConfig: SessionConfig{Output: output},
			}}
			s.inputMsgCh = make(chan inputMsg, 1)

			s.handleInputMsg(inputMsg{isCmd: true, cmd: name, args: "now", commandID: "z1"})

			joined := strings.Join(output.Messages, "")
			if !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
				t.Errorf(":%s now should be REQUEST_INVALID_ARGS, got: %s", name, joined)
			}
			if !strings.Contains(joined, `"command_id":"z1"`) {
				t.Errorf("the refusal must answer the request it refuses: %s", joined)
			}
			if s.activeTask != nil {
				t.Error("a refused command must not start a task")
			}
			if s.quitting {
				t.Error("a refused :quit must not end the session")
			}
		})
	}
}

// runCI drives one CI frame through the dispatcher — the path a command
// actually takes — and returns the frames it wrote. The argument shape is the
// dispatcher's business, so a test of a refusal belongs here, not on a handler
// the dispatcher has already vetted.
func runCI(t *testing.T, s *Session, name, args string) string {
	t.Helper()
	output := &MockOutput{}
	s.Output = output
	s.inputMsgCh = make(chan inputMsg, 1)
	s.handleInputMsg(inputMsg{isCmd: true, cmd: name, args: args, commandID: "c1"})
	return strings.Join(output.Messages, "")
}

// A command that declares arguments keeps its exact count: a third token on
// :video_config used to be ignored, which turns a typo into a setting.
func TestVideoConfigRejectsExtraTokens(t *testing.T) {
	s := &Session{sessionConfig: sessionConfig{
		modelService:  newModelService(newModelManager(""), newRuntimeManager("")),
		SessionConfig: SessionConfig{Input: &nopInput{}},
	}}

	if joined := runCI(t, s, commands.CommandNameVideoConfig, "5 1 junk"); !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
		t.Errorf("a third token should be REFUSED, got: %s", joined)
	}

	// The declared shape still works.
	if joined := runCI(t, s, commands.CommandNameVideoConfig, "5 1"); !strings.Contains(joined, `"fps":5`) {
		t.Errorf("the declared shape should apply the setting, got: %s", joined)
	}
}

// :cancel's argument is a value, not free text, so both halves of the check are
// exercised here: the shape (a second token) and the handler (a word the command
// does not declare). Both answer with the usage line. The two accepted forms
// reach the command itself — STATE_NOTHING_TO_CANCEL, since no task is running —
// which is what makes the refusals a statement about the argument and not about
// the command.
func TestCancelRejectsAnUndeclaredSteeringFate(t *testing.T) {
	s, _ := newCmdOutputSession()

	cases := []struct {
		name string
		args string
		code string
	}{
		{"bare", "", protocol.ErrCodeStateNothingToCancel},
		{"keep_steering", commands.CancelArgKeepSteering, protocol.ErrCodeStateNothingToCancel},
		{"an undeclared word", "keep", protocol.ErrCodeRequestInvalidArgs},
		{"too many tokens", commands.CancelArgKeepSteering + " " + commands.CancelArgKeepSteering, protocol.ErrCodeRequestInvalidArgs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := runCI(t, s, commands.CommandNameCancel, tc.args)
			if !strings.Contains(joined, `"code":"`+tc.code+`"`) {
				t.Errorf(":cancel %q should answer %s, got: %s", tc.args, tc.code, joined)
			}
		})
	}
}

// The word reaches the behavior, not just the parser: a cancel CI carrying
// keep_steering leaves the queue alone, and the bare form — what every adapter
// written before the argument sends — still drains it. The CO is the same null
// either way, so the queue is what tells the two apart.
func TestCancelSteeringArgumentReachesTheQueue(t *testing.T) {
	cases := []struct {
		name string
		args string
		kept int
	}{
		{"bare drains the queue", "", 0},
		{"keep_steering keeps it", commands.CancelArgKeepSteering, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newCmdOutputSession()
			s.activeTask = &taskHandle{cancel: func() {}}
			if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: "steered words"}}) {
				t.Fatal("queueSteering refused a prompt that fits")
			}

			runCI(t, s, commands.CommandNameCancel, tc.args)

			if n := queuedParts(s); n != tc.kept {
				t.Fatalf("%d parts queued after :cancel %q, want %d", n, tc.args, tc.kept)
			}
		})
	}
}

// The MCP commands take whitespace-separated tokens, so a server name holding a
// space arrives as two tokens. Rejecting the count names the problem; parsing
// on used to answer NOT_FOUND for a server the user never typed, and the real
// authorization was never confirmed or declined.
func TestMCPCommandsRejectTheWrongNumberOfTokens(t *testing.T) {
	s, _ := newCmdOutputSession()

	cases := []struct {
		name string
		cmd  string
		args string
	}{
		{"confirm: two tokens", commands.CommandNameMCPConfirm, "srv code"},
		{"confirm: five tokens", commands.CommandNameMCPConfirm, "srv code uri iss extra"},
		{"decline: a name with a space", commands.CommandNameMCPDecline, "my db"},
		{"decline: nothing", commands.CommandNameMCPDecline, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := runCI(t, s, tc.cmd, tc.args)
			if !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
				t.Errorf("want REQUEST_INVALID_ARGS (not a lookup miss), got: %s", joined)
			}
		})
	}
}

// TestEveryCommandRejectsAnUndeclaredArgumentCount is the check the declaration
// made possible: it walks commands.Defs and drives the real dispatcher with a
// count the shape does not allow, so a handler that invents a different arity —
// the class the :fork truncation lived in — fails here instead of in the field.
// A command whose last argument takes the rest of the line has no "too many
// words" case: the extra words are that argument.
func TestEveryCommandRejectsAnUndeclaredArgumentCount(t *testing.T) {
	s, _ := newCmdOutputSession()

	words := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strings.Repeat("x ", n-1) + "x"
	}

	for _, d := range commands.Defs {
		t.Run(d.Name, func(t *testing.T) {
			if d.Min() > 0 {
				if joined := runCI(t, s, d.Name, words(d.Min()-1)); !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
					t.Errorf("%d arguments for a shape needing %d should be REFUSED, got: %s",
						d.Min()-1, d.Min(), joined)
				}
			}
			if !d.EndsWithRest() {
				if joined := runCI(t, s, d.Name, words(d.Max()+1)); !strings.Contains(joined, `"code":"REQUEST_INVALID_ARGS"`) {
					t.Errorf("%d arguments for a shape allowing %d should be REFUSED, got: %s",
						d.Max()+1, d.Max(), joined)
				}
			}
		})
	}
}
