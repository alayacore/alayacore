package terminal

import (
	"io"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ============================================================================
// Snapshot Types
// ============================================================================

// StatusSnapshot holds a consistent point-in-time view of session status.
type StatusSnapshot struct {
	ContextStatus   string
	QueueCount      int
	InProgress      bool
	CurrentStep     int
	MaxSteps        int
	LastCurrentStep int
	LastMaxSteps    int
}

// ModelSnapshot holds a consistent point-in-time view of model state.
type ModelSnapshot struct {
	Models     []agentpkg.ModelInfo
	ActiveID   int
	ActiveName string
	HasModels  bool
	ConfigPath string
}

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

	// Configuration
	SetWindowWidth(width int)
	SetStyles(styles *Styles)

	// Snapshots (replaces many individual getters)
	SnapshotStatus() StatusSnapshot
	SnapshotModels() ModelSnapshot

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
