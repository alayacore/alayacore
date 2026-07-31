package rawio

import (
	"io"
	"sync"
)

// lockedWriter serializes concurrent writes so each TLV frame is written
// atomically.
//
// The session writes from two goroutines (run() and the task goroutine).
// A single os.File.Write on a pipe is only atomic up to PIPE_BUF (4096
// bytes); larger frames (base64 media, large tool output, big system
// messages) could otherwise interleave mid-frame and corrupt the TLV
// stream for the controlling process. plainio and terminal protect their
// output with a mutex; rawio must do the same.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// Write writes p to the underlying writer under lock.
func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}
