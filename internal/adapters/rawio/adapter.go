package rawio

// Package rawio provides a raw TLV stdin/stdout adapter for AlayaCore.
// It forwards raw bytes between stdin/stdout and the agent session —
// no parsing, no formatting, no interpretation.

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter forwards raw bytes between stdin/stdout and the agent session.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new rawio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the rawio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on any error (startup or task failure).
// The controlling process reads stdout and handles TLV itself.
func (a *Adapter) Start() int {
	// Serialize concurrent session writes (run + task goroutines) so TLV
	// frames larger than PIPE_BUF cannot interleave on stdout.
	out := &lockedWriter{w: os.Stdout}
	session, _, err := app.StartSession(a.Config, out, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Wait for the session to finish.
	<-session.Done()

	return 0
}
