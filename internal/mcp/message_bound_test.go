package mcp

import (
	"testing"

	"github.com/alayacore/alayacore/internal/wire"
)

// TestMessageBoundClearsOurLargestFrame pins the floor maxMessageBytes has to stay
// above, which its own comment argues for in prose: a message we read has to be one
// the session can frame, so the bound may not fall under the largest frame we
// produce. wire.MaxFrameSize is an upper bound on that frame, so it is what the
// comparison uses — no arithmetic to keep in step, and a change to either bound
// fails here.
func TestMessageBoundClearsOurLargestFrame(t *testing.T) {
	if maxMessageBytes < wire.MaxFrameSize {
		t.Errorf("maxMessageBytes = %d, below wire.MaxFrameSize = %d: a message we accept could not be framed",
			maxMessageBytes, wire.MaxFrameSize)
	}
}
