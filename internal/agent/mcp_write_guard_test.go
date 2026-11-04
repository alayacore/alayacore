package agent

// MCP progress frames are session frames: the same plane, the same write helper,
// the same consequence when the write fails.

import (
	"context"
	"errors"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
)

// brokenOutput fails every write, standing in for an adapter that has gone away.
type brokenOutput struct{ err error }

func (b brokenOutput) Write([]byte) (int, error) { return 0, b.err }

// An MCP system message that cannot be written must be seen by the session, not
// swallowed. The service used to hold its own io.Writer and discard the write
// error, so a dead adapter during init produced no reaction at all: run() would
// keep going and announce the session ready to a stream nobody was reading.
//
// The consequence is the session's own: the first failed frame marks the output
// broken and cancels the context, which is what stops the run.
//
// The event is a per-server "connecting" status on purpose: it is the one MCP
// event whose only frame is the status itself, so nothing else in the handler can
// be the write that reports the failure.
func TestMCPFramesAreWrittenThroughTheSessionGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Session{
		sessionConfig: sessionConfig{modelService: &modelService{}},
		runState:      runState{Contents: []llm.ContentPart{}},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
	}
	s.Output = brokenOutput{err: errors.New("adapter gone")}
	s.mcpService = newMCPService(&mcp.Initializer{})
	s.state.Store(int32(SessionInitializing))

	s.handleMCPEvent(&mcp.InitEvent{Type: mcp.InitConnecting, Server: "fs"})

	if !s.outputBroken.Load() {
		t.Error("a failed MCP frame left the output marked healthy")
	}
	if ctx.Err() == nil {
		t.Error("a failed MCP frame did not stop the session")
	}
}
