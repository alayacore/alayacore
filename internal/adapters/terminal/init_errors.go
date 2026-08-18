package terminal

// InitErrorCollector collects errors during initialization before the TUI is ready.
// This prevents stderr output from corrupting the terminal display.
//
// InitErrorCollector is NOT safe for concurrent use. It is only used during
// single-threaded program initialization (before any goroutines are running).

import (
	"fmt"
)

// InitError represents a single error message collected during init.
type InitError struct {
	Message string
}

// InitErrorCollector buffers init errors until they can be displayed.
type InitErrorCollector struct {
	errors []InitError
}

// Addf adds an error to the collector.
func (ec *InitErrorCollector) Addf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	ec.errors = append(ec.errors, InitError{Message: msg})
}

// GetAndClear returns all errors and clears the buffer.
func (ec *InitErrorCollector) GetAndClear() []InitError {
	errs := ec.errors
	ec.errors = nil
	return errs
}
