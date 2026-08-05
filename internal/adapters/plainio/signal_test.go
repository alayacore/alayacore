package plainio

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestHandleInterrupt(t *testing.T) {
	// Ctrl-C always sends a cancel frame (matching the terminal adapter's
	// Ctrl-G/:cancel) — the session decides whether there is something to
	// cancel.
	var buf bytes.Buffer
	if !handleInterrupt(&buf) {
		t.Fatal("expected a cancel frame to be sent")
	}

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("expected CI frame, got %s", tag)
	}
	var cmd protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &cmd); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if cmd.Name != "cancel" {
		t.Errorf("expected command 'cancel', got %q", cmd.Name)
	}
	if cmd.Input != "" {
		t.Errorf("expected empty input, got %q", cmd.Input)
	}
	if cmd.ID == "" {
		t.Error("CI frame should carry a generated call ID")
	}
}

func TestHandleInterrupt_WriteError(t *testing.T) {
	// A failing writer must not panic — the signal handler ignores write
	// errors (e.g. the input pipe is already closed).
	if handleInterrupt(&errorWriter{}) {
		t.Fatal("expected handleInterrupt to report failure on write error")
	}
}

// errorWriter fails every write.
type errorWriter struct{}

func (w *errorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("fake write error")
}

func TestLockedWriter(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	lw := &lockedWriter{mu: &mu, w: &buf}

	if _, err := lw.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestLockedWriterSerializesConcurrentWrites(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	lw := &lockedWriter{mu: &mu, w: &buf}

	block := bytes.Repeat([]byte("x"), 64)
	const goroutines = 8
	const writesPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				if _, err := lw.Write(block); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	want := goroutines * writesPerGoroutine * len(block)
	if buf.Len() != want {
		t.Fatalf("expected %d bytes, got %d", want, buf.Len())
	}
	// Every byte must be part of an intact block (no interleaving).
	data := buf.Bytes()
	for i := 0; i < len(data); i += len(block) {
		if !bytes.Equal(data[i:i+len(block)], block) {
			t.Fatalf("interleaved write detected at offset %d", i)
		}
	}
}
