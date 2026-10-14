package agent

// The complete frames — AT and AR — mean two different things on the two streams
// that carry them, and `NoDelta` is what decides which.
//
// Live, with deltas on, the content arrived in Ax fragments and the complete
// frame is written with empty content, as a terminator. The adapter leans on
// that: `outputWriter.writeColored` flushes the pending fragments before it
// handles a non-delta frame, so by the time a complete frame is processed the
// window it names always exists, and internal/adapters/terminal/output.go skips
// a complete frame whose window exists. Skipping it is sound only while there is
// nothing in it — `TestDeltaFlushOnCompleteFrame` in that package pins the
// adapter's half, and it constructs the shape where it would not be: a complete
// frame carrying different text than the fragments, which is skipped, with the
// fragments left on screen.
//
// Replayed, there are no fragments to lean on — they are ephemeral and are never
// persisted — so the complete frame is the content, finds no window, and creates
// one.
//
// So the invariant is "content present ⟺ fragments absent", and it is a coupling
// between two packages that neither one states on its own. This file pins the
// producer's half: making the complete frames always carry the text — a plausible
// tidy-up, since an empty frame looks like a frame with a field left unset —
// leaves the entire suite green, and what it breaks is a display that silently
// keeps whatever the fragments built instead of the text the model produced.

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// textAndReasoningProvider streams one reasoning block and one text block, each
// opened by a fragment and closed by its complete event: the shape that puts an
// Ar/AR pair and an At/AT pair on the output stream. Positions are 1-based, and
// separate, so the two blocks are two blocks rather than one.
type textAndReasoningProvider struct{}

func (textAndReasoningProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.ReasoningDeltaEvent{Delta: "think", Key: "r0", Position: 1}, nil) {
			return
		}
		if !yield(llm.ReasoningCompleteEvent{Key: "r0", Position: 1}, nil) {
			return
		}
		if !yield(llm.TextDeltaEvent{Delta: "hello", Key: "t0", Position: 2}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Key: "t0", Position: 2}, nil) {
			return
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{OutputTokens: 3}}, nil)
	}, nil
}

func (textAndReasoningProvider) SetReasoningLevel(_ int)                       {}
func (textAndReasoningProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (textAndReasoningProvider) SetVideoConfig(_ int, _ int)                   {}

// TestCompleteFramesCarryContentOnlyWhenDeltasAreOff runs the same turn in both
// modes and reads the frames the session actually writes. Each mode asserts both
// directions, because either one alone can be satisfied by a mistake: "the
// complete frame is empty" by never sending the text at all, and "the complete
// frame has the text" by sending it there as well as in the fragments.
func TestCompleteFramesCarryContentOnlyWhenDeltasAreOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		// noDelta is the flag the whole invariant hangs on.
		noDelta bool
	}{
		{"deltas on: the complete frames are terminators", false},
		{"--no-delta: the complete frames are the content", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Where the text has to be: in the fragments when there are fragments,
			// and in the complete frame when there are not. One value, one place —
			// that is the invariant, and asserting only one of the two places can
			// be satisfied by a mistake.
			textFragments, textComplete := "hello", ""
			reasoningFragments, reasoningComplete := "think", ""
			if tc.noDelta {
				textFragments, textComplete = "", "hello"
				reasoningFragments, reasoningComplete = "", "think"
			}

			out := &syncOutput{}
			agent := llm.NewAgent(llm.AgentConfig{Provider: textAndReasoningProvider{}, MaxSteps: 1})
			s := &Session{
				sessionConfig: sessionConfig{
					modelService:  &modelService{agent: agent},
					SessionConfig: SessionConfig{Output: out, NoDelta: tc.noDelta, MaxSteps: 1},
				},
				sharedState: sharedState{histCounter: 10, outputBroken: atomic.Bool{}},
				runState:    runState{taskEventCh: make(chan taskEvent, 20)},
			}
			s.taskResultCh = make(chan []llm.ContentPart, 1)
			s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "hi"}})

			frames := readContentFrames(out.String())

			if got := strings.Join(contents(frames, axText), ""); got != textFragments {
				t.Errorf("the text delta frames carry %q, want %q", got, textFragments)
			}
			if got := strings.Join(contents(frames, axReasoning), ""); got != reasoningFragments {
				t.Errorf("the reasoning delta frames carry %q, want %q", got, reasoningFragments)
			}
			assertOneCompleteFrame(t, frames, axComplete, textComplete)
			assertOneCompleteFrame(t, frames, arComplete, reasoningComplete)

			// The ids matter as much as the content: the adapter skips a complete
			// frame by looking up the window its id names, so a complete frame
			// numbered differently from its fragments would find no window, create
			// a second one for the same block, and show the turn twice.
			fragIDs, completeIDs := ids(frames, axText), ids(frames, axComplete)
			if len(fragIDs) > 0 && len(completeIDs) > 0 && fragIDs[0] != completeIDs[0] {
				t.Errorf("AT names window %q, its At fragments name %q", completeIDs[0], fragIDs[0])
			}
		})
	}
}

// frame holds the (id, content) pair of one content frame, in the order the
// session wrote it.
type frame struct{ id, content string }

// readContentFrames groups content frames by "tag:kind" — text and reasoning
// deltas share the Ax tag, so the content kind is part of the key.
func readContentFrames(stream string) map[string][]frame {
	out := map[string][]frame{}
	rest := strings.NewReader(stream)
	for {
		tag, value, err := wiretest.NextFrame(rest)
		if err != nil {
			return out
		}
		if tag != wire.TagAssistantXDelta && tag != wire.TagAssistantX {
			continue
		}
		var d protocol.ContentData
		if json.Unmarshal([]byte(value), &d) != nil {
			continue
		}
		key := tag + ":" + d.Type
		out[key] = append(out[key], frame{id: d.ID, content: d.Text})
	}
}

// Keys for readContentFrames, so the test does not spell out "tag:kind".
const (
	axText      = wire.TagAssistantXDelta + ":" + protocol.TypeText
	axReasoning = wire.TagAssistantXDelta + ":" + protocol.TypeReasoning
	axComplete  = wire.TagAssistantX + ":" + protocol.TypeText
	arComplete  = wire.TagAssistantX + ":" + protocol.TypeReasoning
)

func contents(frames map[string][]frame, key string) []string {
	got := make([]string, 0, len(frames[key]))
	for _, f := range frames[key] {
		got = append(got, f.content)
	}
	return got
}

func ids(frames map[string][]frame, key string) []string {
	got := make([]string, 0, len(frames[key]))
	for _, f := range frames[key] {
		got = append(got, f.id)
	}
	return got
}

func assertOneCompleteFrame(t *testing.T, frames map[string][]frame, key, want string) {
	t.Helper()
	got := frames[key]
	if len(got) != 1 {
		t.Fatalf("%s: %d frames, want exactly 1", key, len(got))
	}
	if got[0].content != want {
		t.Errorf("%s carries %q, want %q", key, got[0].content, want)
	}
}
