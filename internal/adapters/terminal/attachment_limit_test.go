package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tools"
	"github.com/alayacore/alayacore/internal/wire"
)

// TestAttachmentValue_RejectsAnOversizedFile covers the bound that keeps a user
// action from reaching the wire's own: the frame an attachment becomes is about a
// third larger than the file (base64) plus its wrapper, and a file over this one
// is refused with a message rather than turned into a frame that cannot be
// written.
func TestAttachmentValue_RejectsAnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(path, make([]byte, tools.MaxMediaBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	value, problem := attachmentValue(attachment{path: path, kind: wire.KindImage})
	if value != "" {
		t.Errorf("attachmentValue() = %q, want no value for an oversized attachment", value)
	}
	if !strings.Contains(problem, "16MB media limit") {
		t.Errorf("problem = %q, want it to name the limit", problem)
	}
	// The route for media this large is a URL — the provider fetches it, so its size
	// is the provider's to bound — and this is the message a user sees when they hit
	// the wall, so it is where the route belongs.
	if !strings.Contains(problem, "URL") {
		t.Errorf("problem = %q, want it to point at the route for media this large", problem)
	}
}

func TestAttachmentValue_EncodesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	value, problem := attachmentValue(attachment{path: path, kind: wire.KindImage})
	if problem != "" {
		t.Fatalf("problem = %q, want none", problem)
	}
	if !strings.HasPrefix(value, "data:image/png;base64,") {
		t.Errorf("value = %q, want a png data URI", value)
	}
}

func TestAttachmentValue_ALinkIsItsOwnValue(t *testing.T) {
	value, problem := attachmentValue(attachment{path: "https://example.com/a.png", kind: wire.KindImage, isURL: true})
	if problem != "" || value != "https://example.com/a.png" {
		t.Errorf("attachmentValue() = %q, %q; want the URL and no problem", value, problem)
	}
}

// TestSubmitCmd_ReportsWriteFailure pins what used to be dropped: a failed write
// means the message did not arrive, and the user has to learn that rather than
// watching an attachment vanish while the text around it is sent.
func TestSubmitCmd_ReportsWriteFailure(t *testing.T) {
	msg := submitCmd(failingWriteCloser{}, nil, "hello")()
	if _, ok := msg.(displayErrorMsg); !ok {
		t.Fatalf("submitCmd() = %T, want displayErrorMsg", msg)
	}
}

// TestSubmitCmd_ReportsAMissingAttachment covers the read failure that was
// already reported and is easy to lose in a refactor.
func TestSubmitCmd_ReportsAMissingAttachment(t *testing.T) {
	attachments := []attachment{{path: filepath.Join(t.TempDir(), "gone.png"), kind: wire.KindImage}}
	msg := submitCmd(nopWriteCloser{}, attachments, "")()
	if _, ok := msg.(displayErrorMsg); !ok {
		t.Fatalf("submitCmd() = %T, want displayErrorMsg", msg)
	}
}

// failingWriteCloser refuses every write, standing in for a session whose input
// pipe is gone.
type failingWriteCloser struct{}

func (failingWriteCloser) Write([]byte) (int, error) { return 0, errors.New("pipe is gone") }
func (failingWriteCloser) Close() error              { return nil }
