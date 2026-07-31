package rawio

import (
	"bytes"
	"sync"
	"testing"
)

// TestLockedWriterSerializesFrames verifies that concurrent writes larger
// than PIPE_BUF (4096 bytes) never interleave: the output must be a
// sequence of intact frames, each containing a single uniform byte pattern.
// This is exactly the case where a bare os.Stdout write could tear and
// corrupt the TLV stream for the controlling process.
func TestLockedWriterSerializesFrames(t *testing.T) {
	const (
		goroutines = 8
		framesPer  = 200
		frameSize  = 8192 // > PIPE_BUF — the tearing case
	)

	var buf bytes.Buffer
	lw := &lockedWriter{w: &buf}

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(pattern byte) {
			defer wg.Done()
			frame := bytes.Repeat([]byte{pattern}, frameSize)
			for i := 0; i < framesPer; i++ {
				if _, err := lw.Write(frame); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(byte('a' + g))
	}
	wg.Wait()

	// Verify every frameSize block in the output is intact (uniform byte).
	if buf.Len() != goroutines*framesPer*frameSize {
		t.Fatalf("output length = %d, want %d", buf.Len(), goroutines*framesPer*frameSize)
	}
	data := buf.Bytes()
	for i := 0; i < len(data); i += frameSize {
		block := data[i : i+frameSize]
		first := block[0]
		for _, b := range block {
			if b != first {
				t.Fatalf("frame at offset %d is interleaved (found %q and %q)", i, first, b)
			}
		}
	}
}
