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

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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

	cmd := protocol.CmdMsg{ID: "a1b2", Name: "save", Input: "/tmp/x.alaya"}
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	staged := s.handleInputFrame(tlv.TagCommandIn, string(payload), nil)
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
	if msg.cmdInput != "/tmp/x.alaya" {
		t.Errorf("cmdInput = %q, want %q", msg.cmdInput, "/tmp/x.alaya")
	}
	if msg.cmdID != "a1b2" {
		t.Errorf("cmdID = %q, want %q", msg.cmdID, "a1b2")
	}
	if msg.contentParts != nil {
		t.Errorf("command should have no contentParts, got %d", len(msg.contentParts))
	}
}

func TestInputFrame_CommandIn_NoInput(t *testing.T) {
	s := newInputFrameSession()

	cmd := protocol.CmdMsg{ID: "c3d4", Name: "cancel"}
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	s.handleInputFrame(tlv.TagCommandIn, string(payload), nil)

	msg := readInputMsg(t, s)
	if msg.cmd != "cancel" || msg.cmdInput != "" {
		t.Errorf("unexpected command fields: cmd=%q input=%q", msg.cmd, msg.cmdInput)
	}
}

func TestInputFrame_CommandIn_InvalidJSON(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(tlv.TagCommandIn, "{not json", nil)
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

func TestInputFrame_CommandIn_WithStagedContent(t *testing.T) {
	s := newInputFrameSession()

	staged := s.handleInputFrame(tlv.TagUserT, "hello", nil)
	if len(staged) != 1 {
		t.Fatalf("expected 1 staged part, got %d", len(staged))
	}

	cmd := protocol.CmdMsg{ID: "x", Name: "save"}
	payload, _ := json.Marshal(cmd)
	staged = s.handleInputFrame(tlv.TagCommandIn, string(payload), staged)
	if staged != nil {
		t.Errorf("CI with staged content should return nil staged")
	}

	msg := readInputMsg(t, s)
	if msg.err == nil {
		t.Fatal("expected err for CI sent with staged content")
	}
}

func TestInputFrame_UserText_NoCommandSniffing(t *testing.T) {
	// UT no longer carries commands: ":cancel" is plain user text now.
	s := newInputFrameSession()

	staged := s.handleInputFrame(tlv.TagUserT, ":cancel", nil)
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

	staged := s.handleInputFrame(tlv.TagUserT, "hello", nil)
	staged = s.handleInputFrame(tlv.TagUserEnd, "", staged)
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

// newCmdOutputSession returns a Session whose Output captures raw TLV bytes.
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
	if !strings.Contains(joined, `"id":"a1b2"`) {
		t.Errorf("missing id in CO: %s", joined)
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
	if !strings.Contains(joined, `"output":null`) {
		t.Errorf("fire-and-forget success should have output:null: %s", joined)
	}
	if strings.Contains(joined, "is_error") {
		t.Errorf("success CO should not carry is_error: %s", joined)
	}
}

func TestWriteCmdResult_CmdErr(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("c3d4", nil, &cmdErr{Code: "MODEL_NOT_FOUND", Message: "model_set: model not found: 99"})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"is_error":true`) {
		t.Errorf("missing is_error in CO: %s", joined)
	}
	if !strings.Contains(joined, `"code":"MODEL_NOT_FOUND"`) {
		t.Errorf("missing error code in CO: %s", joined)
	}
	if !strings.Contains(joined, `"message":"model_set: model not found: 99"`) {
		t.Errorf("missing error message in CO: %s", joined)
	}
}

func TestWriteCmdResult_PlainErrorDefaultsToERROR(t *testing.T) {
	s, output := newCmdOutputSession()

	s.writeCmdResult("e5f6", nil, fmt.Errorf("boom"))

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"ERROR"`) {
		t.Errorf("plain error should default to code ERROR: %s", joined)
	}
	if !strings.Contains(joined, `"is_error":true`) {
		t.Errorf("missing is_error in CO: %s", joined)
	}
}

func TestHandleInputMsg_UnknownCommand(t *testing.T) {
	s, output := newCmdOutputSession()
	s.inputMsgCh = make(chan inputMsg, 1) // not used here, keeps Session consistent

	s.handleInputMsg(inputMsg{isCmd: true, cmd: "nope", cmdID: "x1"})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"UNKNOWN_COMMAND"`) {
		t.Errorf("expected UNKNOWN_COMMAND in CO: %s", joined)
	}
	if !strings.Contains(joined, `"id":"x1"`) {
		t.Errorf("expected echoed id in CO: %s", joined)
	}
}

func TestHandleInputMsg_InputErrorUncorrelated(t *testing.T) {
	s, output := newCmdOutputSession()

	s.handleInputMsg(inputMsg{err: fmt.Errorf("invalid command frame: boom")})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"BAD_FRAME"`) {
		t.Errorf("expected BAD_FRAME in CO: %s", joined)
	}
	if !strings.Contains(joined, `"id":""`) {
		t.Errorf("input errors should carry empty id: %s", joined)
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
	if !errors.As(err, &ce) || ce.Code != "MODEL_VALIDATION" {
		t.Errorf("expected MODEL_VALIDATION, got %+v", err)
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
		sharedState: sharedState{sessionCtx: sessionCtx},
	}

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

func TestStartTaskCommand_Busy(t *testing.T) {
	output := &MockOutput{}
	s := &Session{
		runState: runState{
			activeTask: &taskHandle{},
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output: output,
			},
		},
	}

	s.startTaskCommand("x2", func(context.Context) {})

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"code":"BUSY"`) {
		t.Errorf("expected BUSY error in CO: %s", joined)
	}
	if !strings.Contains(joined, `"id":"x2"`) {
		t.Errorf("expected echoed id in CO: %s", joined)
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
