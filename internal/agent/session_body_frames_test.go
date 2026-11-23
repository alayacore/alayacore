package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
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
	// The version is the current one by construction: a fixture that spelled a
	// number would have to be edited at every bump, and a test about frame shape
	// should not fail because the protocol moved.
	raw := "---\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n" +
		"message_version: " + strconv.Itoa(protocol.MessageVersion) + "\nreasoning_level: 1\n---\n" +
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
	raw := "---\r\ncreated_at: 2026-01-01T00:00:00Z\r\nmessage_version: " + strconv.Itoa(protocol.MessageVersion) + "\r\n---\r\n" +
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

// A tool call the file never answers, and a result that answers nothing, are the
// two shapes the API rejects outright — a request built from either fails on the
// first prompt, with an error naming neither the record nor the file. Neither can
// come from the agent loop (every step finish trims unpaired calls, and :fork
// refuses to write a prefix with one), so loading is where a hand-edited or
// truncated file has to be caught.
func TestLoadSession_RejectsAToolCallWithNoResult(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"read it"}`,
		`AF{"call_id":"call-1","name":"read_file","input":{}}`,
	)

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession accepted a tool call with no result")
	}
	if !strings.Contains(err.Error(), "call-1") {
		t.Errorf("error = %q, want it to name the call", err)
	}
}

func TestLoadSession_RejectsAToolResultWithNoCall(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"read it"}`,
		`UF{"call_id":"call-1","output":[{"type":"text","text":"ok"}]}`,
	)

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession accepted a tool result with no call")
	}
	if !strings.Contains(err.Error(), "call-1") {
		t.Errorf("error = %q, want it to name the result", err)
	}
}

// A result must answer a call that precedes it: that is the only shape a request
// accepts, so a file with the two swapped is as unusable as one missing a half.
func TestLoadSession_RejectsAToolResultBeforeItsCall(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"read it"}`,
		`UF{"call_id":"call-1","output":[{"type":"text","text":"ok"}]}`,
		`AF{"call_id":"call-1","name":"read_file","input":{}}`,
	)

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession accepted a tool result that precedes its call")
	}
	if !strings.Contains(err.Error(), "precedes") {
		t.Errorf("error = %q, want it to name the ordering problem", err)
	}
}

// The paired form loads, so the rejection is about the pair and not about tool
// records in a file at all.
func TestLoadSession_AcceptsAPairedToolCallAndResult(t *testing.T) {
	path := writeSessionFile(t,
		`UX{"type":"text","text":"read it"}`,
		`AF{"call_id":"call-1","name":"read_file","input":{}}`,
		`UF{"call_id":"call-1","output":[{"type":"text","text":"ok"}]}`,
	)

	data, err := loadSession(path)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(data.Contents) != 3 {
		t.Fatalf("loaded %d parts, want the prompt, the call and its result", len(data.Contents))
	}
	if _, ok := data.Contents[1].(*llm.ToolInputPart); !ok {
		t.Errorf("part 1 = %#v, want the tool call", data.Contents[1])
	}
	if _, ok := data.Contents[2].(*llm.ToolOutputPart); !ok {
		t.Errorf("part 2 = %#v, want the tool result", data.Contents[2])
	}
}
