package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// What a session file's body may hold. It is one frame per line, so a file can be
// written by hand — and a stream's own output, or the frame samples, carry the
// control frames UE and CE. Those are *not* welcome in a file, and the reason is
// worth pinning: the loader groups frames into messages by role, so a UE between
// two user frames would be a message boundary the author drew and the loader
// ignored, merging two messages into one. A file that says what it means has no
// control frames in it.

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

func TestLoadSession_RejectsStreamDelimiters(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"first question"}`,
		"UE",
		`UX{"type":"text","text":"second question"}`,
	)

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession accepted a UE: it would have merged the two user messages")
	}
	if !strings.Contains(err.Error(), "stream delimiter") {
		t.Errorf("error = %q, want it to say what the frame is", err)
	}
}

// The same file without the delimiter loads, so the rejection is about the frame
// and not about the shape of the file around it.
func TestLoadSession_ConsecutiveContentFramesLoad(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"first question"}`,
		`AX{"type":"text","text":"an answer"}`,
	)

	data, err := loadSession(path)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(data.Contents) != 2 {
		t.Fatalf("loaded %d parts, want 2", len(data.Contents))
	}
	if tp, ok := data.Contents[0].(*llm.TextPart); !ok || tp.Text != "first question" {
		t.Errorf("part 0 = %#v, want the user's text", data.Contents[0])
	}
	if tp, ok := data.Contents[1].(*llm.TextPart); !ok || tp.Text != "an answer" {
		t.Errorf("part 1 = %#v, want the assistant's text", data.Contents[1])
	}
}

// TestLoadSession_ReadsCRLF covers a file an editor (or a checkout with
// autocrlf) gave Windows line endings: the body already tolerated them, and the
// frontmatter — parsed line by line — has to as well, or the file is unloadable
// with a message about a missing delimiter that says nothing about line endings.
func TestLoadSession_ReadsCRLF(t *testing.T) {
	raw := "---\r\ncreated_at: 2026-01-01T00:00:00Z\r\nmessage_version: 15\r\n---\r\n" +
		"UX{\"type\":\"text\",\"text\":\"hello\"}\r\n"
	path := filepath.Join(t.TempDir(), "crlf.alaya")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := loadSession(path)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(data.Contents) != 1 {
		t.Fatalf("loaded %d parts, want 1", len(data.Contents))
	}
	if tp, ok := data.Contents[0].(*llm.TextPart); !ok || tp.Text != "hello" {
		t.Errorf("part 0 = %#v, want the user's text", data.Contents[0])
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
