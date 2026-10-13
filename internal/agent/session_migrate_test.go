package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/sessionmigrate"
	"github.com/alayacore/alayacore/internal/tlv"
)

// newSaveSession returns a bare Session usable for save/load, mirroring the
// fixture session_id_renumber_test.go uses.
func newSaveSession() *Session {
	return &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Input: &nopInput{}, Output: &nopOutput{}},
		},
	}
}

// TestMigrateV13SessionLoadsAsV14 pins the whole point of the migration: a file
// the core can no longer read (message_version 13, v13 tags) becomes one it
// loads, with the same parts in the same order.
func TestMigrateV13SessionLoadsAsV14(t *testing.T) {
	var body []byte
	add := func(tag, value string) {
		frame, err := tlv.EncodeTLV(tag, value)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, []byte("\n\n")...)
		body = append(body, frame...)
	}
	add("UT", "Read the file main.go")
	add("UI", "data:image/png;base64,AAAA")
	add("AF", `{"id":"t1","name":"read_file","input":{"path":"main.go"}}`)
	add("UF", `{"id":"t1","output":[{"type":"text","text":"ok"}],"is_error":false}`)
	add("AT", "Here it is.")
	add("AR", "thinking...")
	add("UV", "data:video/mp4;base64,AAAA")

	front := "---\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n" +
		"message_version: 13\nreasoning_level: 1\n---\n"
	migrated, err := sessionmigrate.Migrate(append([]byte(front), body...))
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	path := filepath.Join(t.TempDir(), "migrated.alaya")
	if err := os.WriteFile(path, migrated, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := loadSession(path)
	if err != nil {
		t.Fatalf("load migrated session: %v", err)
	}

	if len(data.Contents) != 7 {
		t.Fatalf("loaded %d parts, want 7", len(data.Contents))
	}
	if _, ok := data.Contents[0].(*llm.TextPart); !ok || data.Contents[0].GetRole() != llm.RoleUser {
		t.Errorf("part 0 = %T role %q, want user TextPart", data.Contents[0], data.Contents[0].GetRole())
	}
	if img, ok := data.Contents[1].(*llm.ImagePart); !ok || img.URI == "" {
		t.Errorf("part 1 = %T, want ImagePart with a URI", data.Contents[1])
	}
	if ti, ok := data.Contents[2].(*llm.ToolInputPart); !ok || ti.ID != "t1" || ti.Name != "read_file" {
		t.Errorf("part 2 = %#v, want ToolInputPart t1/read_file", data.Contents[2])
	}
	if to, ok := data.Contents[3].(*llm.ToolOutputPart); !ok || to.ID != "t1" || to.IsError {
		t.Errorf("part 3 = %#v, want ToolOutputPart t1 success", data.Contents[3])
	}
	if rp, ok := data.Contents[5].(*llm.ReasoningPart); !ok || rp.Text != "thinking..." {
		t.Errorf("part 5 = %#v, want ReasoningPart \"thinking...\"", data.Contents[5])
	}
	// A migrated video has no fps/res (the v13 file never recorded one), so it
	// serializes with the provider default and cannot follow a later
	// :video_config — see the sessionmigrate package doc.
	if v, ok := data.Contents[6].(*llm.VideoPart); !ok || v.URI == "" || v.FPS != 0 || v.Res != 0 {
		t.Errorf("part 6 = %#v, want a VideoPart with unset fps/res", data.Contents[6])
	}
	// Ids are re-issued in record order on load, as for any file.
	for i, p := range data.Contents {
		if p.GetHistoryID() != uint64(i+1) {
			t.Errorf("part %d history id = %d, want %d", i, p.GetHistoryID(), i+1)
		}
	}
	// Old files carry no per-message time; the migrated parts carry none either.
	if !data.Contents[0].GetTime().IsZero() {
		t.Errorf("migrated part carries time %v, want zero", data.Contents[0].GetTime())
	}
}

// TestSessionFileOmitsHistoryIDAndRoundTripsTime pins two v14 invariants: the
// session file never carries a history id (loading re-issues it), and a part's
// message time survives save + load.
func TestSessionFileOmitsHistoryIDAndRoundTripsTime(t *testing.T) {
	stamp := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser, HistoryID: 7, Time: stamp}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, HistoryID: 8, Time: stamp}},
		&llm.ToolOutputPart{ID: "call_1", Output: []llm.ContentPart{&llm.TextPart{Text: "ok"}},
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool, HistoryID: 9, Time: stamp}},
	}

	s := newSaveSession()
	path := filepath.Join(t.TempDir(), "time.alaya")
	if err := s.saveContentToFile(path, contents); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id":`) {
		t.Errorf("session file carries a history id (must be omitted):\n%s", raw)
	}

	loaded, err := loadSession(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, p := range loaded.Contents {
		if !p.GetTime().Equal(stamp) {
			t.Errorf("part %d time = %v, want %v", i, p.GetTime(), stamp)
		}
	}
}

// TestVideoSettingsSnapshotPerPart pins the reason this whole change exists: a
// video's fps/resolution is fixed on the part, so it cannot be rewritten by a
// later default change. A part that already carries settings is left alone by
// the default, and its serialization is a pure function of the part.
func TestVideoSettingsSnapshotPerPart(t *testing.T) {
	s := newSaveSession()
	s.modelService = &modelService{videoFPS: 5, videoRes: 1}

	plain := &llm.VideoPart{URI: "data:video/mp4;base64,AAAA"}
	s.applyVideoDefault(plain)
	if plain.FPS != 5 || plain.Res != 1 {
		t.Fatalf("unset video got fps/res %d/%d, want 5/1", plain.FPS, plain.Res)
	}

	explicit := &llm.VideoPart{URI: "data:video/mp4;base64,BBBB", FPS: 8, Res: 0}
	s.applyVideoDefault(explicit)
	if explicit.FPS != 8 {
		t.Fatalf("video with its own fps was overwritten to %d", explicit.FPS)
	}

	// Changing the default does not touch a part already stamped: this is the
	// property that stops :video_config from rewriting earlier messages.
	s.modelService.videoFPS, s.modelService.videoRes = 2, 0
	_, body, err := contentPartJSON(explicit, "")
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(body, `"fps":8`) {
		t.Errorf("serialized body %s lost the part's own fps after the default changed", body)
	}
}
