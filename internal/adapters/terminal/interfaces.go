package terminal

import (
	"io"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Snapshot Types
// ============================================================================

// ThemeEntry holds a cached theme with its full content, received from the
// session via ThemeListMsg on startup.
type ThemeEntry struct {
	Name  string
	Theme *theme.Theme
}

// StatusSnapshot holds a consistent point-in-time view of session status.
type StatusSnapshot struct {
	ContextTokens   int64
	ContextLimit    int64
	InProgress      bool
	CurrentStep     int
	MaxSteps        int
	LastCurrentStep int // final step of the last completed task (0 = none)
	LastMaxSteps    int // max steps of the last completed task (0 = unlimited)
	ReasoningLevel  int
	ActiveTheme     string
	ActiveThemeData *theme.Theme
	VideoFPS        int
	VideoRes        int

	// MCP init status — reflects the current phase of MCP initialization.
	// Values: "" (no MCP), "connecting", "connected", "failed",
	// "auth_required", "auth_running", "done".
	MCPStatus  string
	MCPServer  string   // current server being connected/authorized
	MCPServers []string // full list of servers currently being initialized

	// CachedThemes holds the full list of themes received from the session.
	CachedThemes []ThemeEntry
}

// ModelSnapshot holds a consistent point-in-time view of model state.
type ModelSnapshot struct {
	Models     []protocol.ModelInfo
	ActiveID   int
	ActiveName string
}

// ============================================================================
// Interfaces for Testability
// ============================================================================

// OutputWriter is the interface for writing output from the session.
// It abstracts the terminal output writer for better testability.
type OutputWriter interface {
	io.Writer
	io.Closer

	// Configuration
	SetWindowWidth(width int)
	WithStyles(styles *Styles)

	// Snapshots (replaces many individual getters)
	SnapshotStatus() StatusSnapshot
	SnapshotModels() ModelSnapshot

	// Output methods
	WriteError(msg string)
	WriteNotify(msg string)

	// Confirm dialog support
	GetPendingToolConfirm() (id, toolName, toolInput string, ok bool)
	GetPendingMCPAuth() (server, url string, ok bool)
	ClearMCPAuths()
	ConsumeSessionReady() bool // returns true if initialization just completed (SM "session" ready frame)

	// Update signaling
	FlushPendingDeltas() // flushes accumulated delta frames to WindowBuffer
	DrainDirty() bool    // returns true if display was dirty, clears the flag
	WindowBuffer() *WindowBuffer
}

// Ensure outputWriter implements OutputWriter
var _ OutputWriter = (*outputWriter)(nil)

// DrainDirty returns true if the display was dirty and clears the flag.
// This replaces the channel-based update signaling with a simple atomic bool.
func (w *outputWriter) DrainDirty() bool {
	return w.dirty.CompareAndSwap(true, false)
}

// WindowBuffer returns the window buffer for direct access
func (w *outputWriter) WindowBuffer() *WindowBuffer {
	return w.windowBuffer
}
