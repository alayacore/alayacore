package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// A live session numbers history IDs by stream arrival (see llm.Agent
// blockID), while the record lays parts out by kind. The file stores no IDs at
// all, so loading re-issues them sequentially in record order. This pins that,
// because docs/architecture.md states it as the reason histCounter may resume
// at len(Contents) -- a resume point that is only safe if a loaded session's
// IDs are exactly 1..N.
//
// Both cases below are shapes a real session can actually be in, not arbitrary
// numbers:
func TestSaveAndLoadReissuesHistoryIDsInRecordOrder(t *testing.T) {
	tests := []struct {
		name     string
		contents []llm.ContentPart
	}{
		{
			// A provider that streams tool_calls before reasoning/text: the tool
			// touches IDGen first and takes 2, but getContents persists it after
			// reasoning and text, so the record reads 1, 3, 4, 2.
			name: "numbering against record order",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 1}},
				&llm.ReasoningPart{Text: "think", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 3}},
				&llm.TextPart{Text: "Answer.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 4}},
				&llm.ToolInputPart{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`),
					ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 2}},
			},
		},
		{
			// cleanIncompleteToolInputs() removes tool_use parts that already
			// claimed an ID, leaving a hole: 3 parts whose highest ID is 4. Were
			// IDs persisted, resuming at len(Contents) would hand out 4 again and
			// collide with a surviving part.
			name: "hole left by a discarded part",
			contents: []llm.ContentPart{
				&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 1}},
				&llm.ReasoningPart{Text: "think", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 2}},
				&llm.TextPart{Text: "Answer.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 4}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The premise must differ from the result somewhere, or the test
			// proves nothing about renumbering.
			anomalous := false
			for i, part := range tt.contents {
				if part.GetHistoryID() != uint64(i+1) {
					anomalous = true
				}
			}
			if !anomalous {
				t.Fatalf("case %q: input IDs already equal 1..N; nothing to renumber", tt.name)
			}

			sessionPath := filepath.Join(t.TempDir(), "renumber.alaya")
			s := &Session{
				runState: runState{},
				sessionConfig: sessionConfig{
					SessionConfig: SessionConfig{Input: &nopInput{}, Output: &nopOutput{}},
				},
			}
			if err := s.saveContentToFile(sessionPath, tt.contents); err != nil {
				t.Fatalf("save: %v", err)
			}

			loaded, err := loadSession(sessionPath)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if len(loaded.Contents) != len(tt.contents) {
				t.Fatalf("loaded %d parts, want %d", len(loaded.Contents), len(tt.contents))
			}

			for i, part := range loaded.Contents {
				if want := uint64(i + 1); part.GetHistoryID() != want {
					t.Errorf("part %d (%T): history ID = %d, want %d (re-issued by record position)",
						i, part, part.GetHistoryID(), want)
				}
			}
			// len(Contents) is then a correct resume point: highest ID equals the
			// part count, so the next prompt cannot collide with a loaded part.
			last := loaded.Contents[len(loaded.Contents)-1]
			if last.GetHistoryID() != uint64(len(loaded.Contents)) {
				t.Errorf("highest loaded ID %d != %d parts", last.GetHistoryID(), len(loaded.Contents))
			}
		})
	}
}
