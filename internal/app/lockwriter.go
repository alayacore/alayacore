package app

import (
	"io"
	"sync"
)

// LockedWriter serializes writes to a shared io.Writer. The plainio
// adapter writes to the session's TLV input pipe from two goroutines (the
// stdin reader and the MCP OAuth flow); wrapping the pipe writer in a
// LockedWriter keeps TLV frames from interleaving.
type LockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLockedWriter wraps w so every Write is serialized.
func NewLockedWriter(w io.Writer) *LockedWriter {
	return &LockedWriter{w: w}
}

// Write serializes writes to the underlying writer.
func (lw *LockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}
