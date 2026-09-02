package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// fork() must not cut a tool call away from its result: a file ending in a
// tool_use with no tool_result loads happily and is rejected by the provider on
// the next prompt, which is nowhere near the action that produced it.
func TestForkKeepsToolResults(t *testing.T) {
	user := func(id uint64) llm.ContentPart {
		return &llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: id}}
	}
	reasoning := func(id uint64) llm.ContentPart {
		return &llm.ReasoningPart{Text: "think", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: id}}
	}
	text := func(id uint64) llm.ContentPart {
		return &llm.TextPart{Text: "Answer.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: id}}
	}
	call := func(cid string, id uint64) llm.ContentPart {
		return &llm.ToolInputPart{ID: cid, Name: "noop", Input: json.RawMessage(`{}`),
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: id}}
	}
	result := func(cid string, id uint64) llm.ContentPart {
		return &llm.ToolOutputPart{ID: cid, Output: []llm.ContentPart{&llm.TextPart{Text: "ok"}},
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool, HistoryID: id}}
	}

	oneCall := func() []llm.ContentPart {
		return []llm.ContentPart{user(1), reasoning(2), text(3), call("c1", 4), result("c1", 5)}
	}
	// A step's results follow all of its calls: call1, call2, result1, result2.
	twoCalls := func() []llm.ContentPart {
		return []llm.ContentPart{
			user(1), reasoning(2), call("c1", 3), call("c2", 4), result("c1", 5), result("c2", 6), text(7),
		}
	}

	tests := []struct {
		name     string
		contents []llm.ContentPart
		forkID   uint64
		wantIDs  []uint64 // history IDs expected in the forked prefix, in order
	}{
		{
			// The regression: this used to write a file ending in a bare tool_use.
			name:     "fork at a tool call pulls in its result",
			contents: oneCall(), forkID: 4,
			wantIDs: []uint64{1, 2, 3, 4, 5},
		},
		{
			name:     "fork at the result writes everything",
			contents: oneCall(), forkID: 5,
			wantIDs: []uint64{1, 2, 3, 4, 5},
		},
		{
			// No tool call is inside this prefix, so nothing to complete.
			name:     "fork at reasoning cuts there",
			contents: oneCall(), forkID: 2,
			wantIDs: []uint64{1, 2},
		},
		{
			name:     "fork at text cuts there, later calls excluded",
			contents: oneCall(), forkID: 3,
			wantIDs: []uint64{1, 2, 3},
		},
		{
			// Results of a message come after all of its calls, so completing the
			// first call necessarily reaches past the second: the whole group moves.
			name:     "fork at first of two calls keeps both calls and both results",
			contents: twoCalls(), forkID: 3,
			wantIDs: []uint64{1, 2, 3, 4, 5, 6},
		},
		{
			name:     "fork at second call keeps both results",
			contents: twoCalls(), forkID: 4,
			wantIDs: []uint64{1, 2, 3, 4, 5, 6},
		},
		{
			name:     "fork at first result still completes the second call",
			contents: twoCalls(), forkID: 5,
			wantIDs: []uint64{1, 2, 3, 4, 5, 6},
		},
		{
			// A trailing user part carries no calls.
			name:     "fork at a later step",
			contents: append(oneCall(), user(6), text(7)), forkID: 7,
			wantIDs: []uint64{1, 2, 3, 4, 5, 6, 7},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newForkTestSession(tt.contents)
			outPath := tempSessionPath(t, "fork")
			res, err := s.handleFork(uitoa(tt.forkID) + " " + outPath)
			if err != nil {
				t.Fatalf("fork: %v", err)
			}
			if got := res.(map[string]any)["count"]; got != len(tt.wantIDs) {
				t.Fatalf("reported count = %v, want %d", got, len(tt.wantIDs))
			}

			loaded, err := loadSession(outPath)
			if err != nil {
				t.Fatalf("load forked session: %v", err)
			}
			assertNoOrphanToolUse(t, loaded.Contents)
			if len(loaded.Contents) != len(tt.wantIDs) {
				t.Fatalf("forked %d parts, want %d", len(loaded.Contents), len(tt.wantIDs))
			}
			// IDs are re-issued on load, so compare shape: part type per position.
			for i, wantID := range tt.wantIDs {
				src := findPart(tt.contents, wantID)
				if src == nil {
					t.Fatalf("fixture has no part with id %d", wantID)
				}
				if gotType, wantType := typeName(loaded.Contents[i]), typeName(src); gotType != wantType {
					t.Errorf("part %d = %s, want %s", i, gotType, wantType)
				}
			}
		})
	}
}

// A call whose result does not exist anywhere cannot be completed, so it is
// dropped rather than written as an orphan.
func TestForkDropsUncallableToolUse(t *testing.T) {
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 1}},
		&llm.ToolInputPart{ID: "ghost", Name: "noop", Input: json.RawMessage(`{}`),
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 2}},
	}
	s := newForkTestSession(contents)
	outPath := tempSessionPath(t, "orphan")
	if _, err := s.handleFork("2 " + outPath); err != nil {
		t.Fatalf("fork: %v", err)
	}
	loaded, err := loadSession(outPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertNoOrphanToolUse(t, loaded.Contents)
	for _, p := range loaded.Contents {
		if _, ok := p.(*llm.ToolInputPart); ok {
			t.Fatalf("forked session still contains the unpaired tool call: %#v", loaded.Contents)
		}
	}
}

func TestForkUnknownID(t *testing.T) {
	s := newForkTestSession([]llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 1}},
	})
	if _, err := s.handleFork("99 " + tempSessionPath(t, "none")); err == nil {
		t.Fatal("expected NOT_FOUND for an unknown history ID")
	}
}

// --- helpers -------------------------------------------------------------

func newForkTestSession(contents []llm.ContentPart) *Session {
	return &Session{
		runState:      runState{Contents: contents},
		sessionConfig: sessionConfig{SessionConfig: SessionConfig{Input: &nopInput{}, Output: &nopOutput{}}},
	}
}

func tempSessionPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name+".alaya")
}

func uitoa(v uint64) string { return strconv.FormatUint(v, 10) }

func findPart(contents []llm.ContentPart, id uint64) llm.ContentPart {
	for _, p := range contents {
		if p.GetHistoryID() == id {
			return p
		}
	}
	return nil
}

func typeName(p llm.ContentPart) string { return fmt.Sprintf("%T", p) }

// assertNoOrphanToolUse fails if any tool call in parts lacks a result with the
// same call ID -- the shape a provider rejects.
func assertNoOrphanToolUse(t *testing.T, parts []llm.ContentPart) {
	t.Helper()
	calls, results := map[string]bool{}, map[string]bool{}
	for _, p := range parts {
		switch v := p.(type) {
		case *llm.ToolInputPart:
			calls[v.ID] = true
		case *llm.ToolOutputPart:
			results[v.ID] = true
		}
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("forked session has tool_use %q with no tool_result", id)
		}
	}
}

// A dangling call in the middle of the history cannot be repaired by widening
// the cut -- its result does not exist -- and the existing cleaner only examines
// the trailing assistant segment. Writing anyway would produce a file that loads
// cleanly and fails on the next prompt, so :fork refuses and writes nothing.
func TestForkRefusesUnrepairableHistory(t *testing.T) {
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 1}},
		&llm.ToolInputPart{ID: "ghost", Name: "noop", Input: json.RawMessage(`{}`),
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 2}}, // no result, anywhere
		&llm.TextPart{Text: "next turn", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 3}},
		&llm.TextPart{Text: "Answer.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 4}},
	}
	s := newForkTestSession(contents)
	outPath := tempSessionPath(t, "unrepairable")

	if _, err := s.handleFork("4 " + outPath); err == nil {
		t.Fatal("expected the fork to be refused, got success")
	} else if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the dangling call, got: %v", err)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Errorf("refused fork wrote %s anyway", outPath)
	}

	// A cut before the dangling call is still valid and must succeed.
	if _, err := s.handleFork("1 " + outPath); err != nil {
		t.Errorf("fork before the dangling call should succeed, got: %v", err)
	}
}
