package terminal

// WarningCollector collects warnings during initialization before the TUI is ready.
// This prevents stderr output from corrupting the terminal display.

import (
	"fmt"
	"sync"
)

// Warning represents a single warning message
type Warning struct {
	Message string
}

// WarningCollector buffers warnings until they can be displayed
type WarningCollector struct {
	mu       sync.Mutex
	warnings []Warning
}

// Addf adds a warning to the collector
func (wc *WarningCollector) Addf(format string, args ...interface{}) {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	wc.warnings = append(wc.warnings, Warning{Message: msg})
}

// GetAndClear returns all warnings and clears the buffer
func (wc *WarningCollector) GetAndClear() []Warning {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	warnings := wc.warnings
	wc.warnings = nil
	return warnings
}

// AddWarningf adds a warning to the collector (nil-safe)
func AddWarningf(wc *WarningCollector, format string, args ...interface{}) {
	if wc != nil {
		wc.Addf(format, args...)
	}
}

// HasWarnings checks if there are any warnings
func (wc *WarningCollector) HasWarnings() bool {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return len(wc.warnings) > 0
}
