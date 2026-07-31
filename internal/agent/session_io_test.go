package agent

import (
	"encoding/json"
	"strings"
	"testing"

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
