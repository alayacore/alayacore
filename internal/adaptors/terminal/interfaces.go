package terminal

import (
	"io"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ============================================================================
// Interfaces for Testability
// ============================================================================

// OutputWriter is the interface for writing output from the session.
// It abstracts the terminal output writer for better testability.
type OutputWriter interface {
	io.Writer
	io.Closer

	// stream.Output methods
	WriteString(s string) (n int, err error)
	Flush() error

	// Configuration and state
	SetWindowWidth(width int)
	GetStatus() string
	GetQueueCount() int

	// Model management
	GetModels() []agentpkg.ModelInfo
	GetActiveModelID() string
	GetActiveModelName() string
	HasModels() bool
	GetModelConfigPath() string

	// Queue management
	GetQueueItems() []QueueItem

	// Output methods
	AppendError(format string, args ...any)
	WriteNotify(msg string)

	// Update signaling
	UpdateChan() <-chan struct{}
	WindowBuffer() *WindowBuffer
}

// Ensure outputWriter implements OutputWriter
var _ OutputWriter = (*outputWriter)(nil)

// UpdateChan returns the update channel for signaling display updates
func (w *outputWriter) UpdateChan() <-chan struct{} {
	return w.updateChan
}

// WindowBuffer returns the window buffer for direct access
func (w *outputWriter) WindowBuffer() *WindowBuffer {
	return w.windowBuffer
}
