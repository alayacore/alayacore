package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// What a session file's body may hold. It is one frame per line, which means a
// file can be assembled by hand — or out of the frame samples, or out of a
// session's own stdout — and those carry the control frames UE and CE. They say
// nothing about a conversation, so they are skipped; anything else that is not
// conversation content is a file the format has no meaning for, and says so.

func writeSessionFile(t *testing.T, lines ...string) string {
	t.Helper()
	raw := "---\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n" +
		"message_version: 15\nreasoning_level: 1\n---\n" +
		strings.Join(lines, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "session.alaya")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSession_SkipsControlFrames(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"hello"}`,
		"UE",
		`AX{"type":"text","text":"hi"}`,
		"CE",
	)

	data, err := loadSession(path)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(data.Contents) != 2 {
		t.Fatalf("loaded %d parts, want 2 — the control frames carry nothing", len(data.Contents))
	}
	if tp, ok := data.Contents[0].(*llm.TextPart); !ok || tp.Text != "hello" {
		t.Errorf("part 0 = %#v, want the user's text", data.Contents[0])
	}
	if tp, ok := data.Contents[1].(*llm.TextPart); !ok || tp.Text != "hi" {
		t.Errorf("part 1 = %#v, want the assistant's text", data.Contents[1])
	}
}

func TestLoadSession_RejectsANonContentFrame(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"hello"}`,
		`SM{"type":"notify","data":{"text":"whatever"}}`,
	)

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession accepted a frame that is not conversation content")
	}
	if !strings.Contains(err.Error(), "not conversation content") {
		t.Errorf("error = %q, want it to say what is wrong with the frame", err)
	}
}
