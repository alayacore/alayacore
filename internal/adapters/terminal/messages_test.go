package terminal

// Message markers for the test-only message types. Msg is sealed (messages.go),
// so a test model's own message has to declare itself one, exactly as a
// production message type does.

import (
	"os"
	"testing"
)

// TestMain turns the unknown-dispatch defaults fatal for the whole package, so a
// message or result added to a sealed set without a handler fails here.
func TestMain(m *testing.M) {
	failOnUnknownDispatch = true
	os.Exit(m.Run())
}

func (backlogMsg) isMsg()   {}
func (cmdTrigger) isMsg()   {}
func (cmdResultMsg) isMsg() {}
func (batchResult) isMsg()  {}
func (seqResult) isMsg()    {}
func (seqTrigger) isMsg()   {}
func (tickTrigger) isMsg()  {}
func (tickDone) isMsg()     {}
func (execTrigger) isMsg()  {}
func (execResult) isMsg()   {}
func (panicMsg) isMsg()     {}
func (unheardMsg) isMsg()   {}

// panicMsg drives TestProgramPanicRecovery: a message whose handling panics.
type panicMsg struct{}

// unheardMsg is deliberately not handled by Terminal.Update. It exists to prove
// the sealed set plus the test-fatal default catch a message added without a
// handler, instead of dropping it.
type unheardMsg struct{}

func TestUpdateFailsOnUnhandledMessage(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Update did not fail on a sealed but unhandled message")
		}
	}()
	newTestTerminal().Update(unheardMsg{})
}
