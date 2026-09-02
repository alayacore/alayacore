package providers_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// historyIDs are handed out by llm.Agent on *first touch* of each content
// index (getOrAssignID), while the session records content in the array order
// getContents() builds: reasoning, text, tools. In delta mode the first touch
// is the first delta, so numbering follows delta arrival. With --no-delta no
// text/reasoning delta callbacks are registered at all (session_task.go — the
// tool callbacks are, in both modes), so for these two blocks the complete
// events are the only touch and their emission order alone decides numbering —
// which is why it has to match the array order.
//
// When parseStream emitted text first, the text block took the lower ID while
// the session listed reasoning first, and --no-delta runs rendered ASSISTANT
// above REASONING: with no deltas pending, flushPendingDeltas is a no-op and
// the authoritative frames create the windows in arrival order.
func TestOpenAIHistoryIDsMonotonicWithContentPositions(t *testing.T) {
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking hard\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:         "test",
		BaseURL:        server.URL,
		ReasoningField: "reasoning",
	})
	if err != nil {
		t.Fatal(err)
	}

	next := uint64(100)
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 2})

	// getOrAssignID is called from inside the OnTextComplete /
	// OnReasoningComplete guards in llm.Agent, so the session's real ID
	// numbering only exists while those callbacks are registered — register
	// them here the way internal/agent/session_task.go does.
	res, err := agent.Stream(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}),
		llm.StreamCallbacks{
			IDGen:               func() uint64 { n := next; next++; return n },
			OnTextComplete:      func(string, uint64) error { return nil },
			OnReasoningComplete: func(string, uint64) error { return nil },
		})
	if err != nil {
		t.Fatalf("agent stream: %v", err)
	}

	var reasonID, textID uint64
	var reasonPos, textPos = -1, -1
	for i, part := range res.Contents {
		if part.GetRole() != llm.RoleAssistant {
			continue // the echoed user prompt carries no assistant meta
		}
		switch part.(type) {
		case *llm.ReasoningPart:
			reasonID, reasonPos = part.GetHistoryID(), i
		case *llm.TextPart:
			textID, textPos = part.GetHistoryID(), i
		}
	}
	if reasonPos < 0 || textPos < 0 {
		t.Fatalf("contents missing a part: reasoning pos %d, text pos %d (%#v)", reasonPos, textPos, res.Contents)
	}
	if reasonID == 0 || textID == 0 {
		t.Fatalf("no historyID assigned (reasoning=%d text=%d) — IDGen path not exercised", reasonID, textID)
	}

	if reasonPos > textPos {
		t.Fatalf("record order: reasoning at %d, text at %d — want reasoning first", reasonPos, textPos)
	}
	if reasonID >= textID {
		t.Errorf("historyIDs invert record order: reasoning=%d text=%d (reasoning is at position %d, text at %d) — "+
			"a consumer cannot recover record order from ID order", reasonID, textID, reasonPos, textPos)
	}
}
