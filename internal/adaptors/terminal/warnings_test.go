package terminal

import (
	"strings"
	"testing"
)

func TestWarningCollector(t *testing.T) {
	wc := &WarningCollector{}

	// Test adding warnings (no trailing \n)
	AddWarningf(wc, "Test warning %d", 1)
	AddWarningf(wc, "Another warning")

	warnings := wc.GetAndClear()
	if len(warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d", len(warnings))
	}

	if warnings[0].Message != "Test warning 1" {
		t.Errorf("Expected 'Test warning 1', got '%s'", warnings[0].Message)
	}

	if warnings[1].Message != "Another warning" {
		t.Errorf("Expected 'Another warning', got '%s'", warnings[1].Message)
	}

	// Verify no trailing newlines
	for i, w := range warnings {
		if strings.HasSuffix(w.Message, "\n") {
			t.Errorf("Warning %d should not have trailing newline: %q", i, w.Message)
		}
	}

	// Test that warnings are cleared after retrieval
	warnings = wc.GetAndClear()
	if len(warnings) != 0 {
		t.Errorf("Expected 0 warnings after GetAndClear, got %d", len(warnings))
	}
}

func TestWarningCollectorHasWarnings(t *testing.T) {
	wc := &WarningCollector{}

	if wc.HasWarnings() {
		t.Error("Expected no warnings initially")
	}

	wc.Addf("test")
	if !wc.HasWarnings() {
		t.Error("Expected warnings after Addf")
	}

	wc.GetAndClear()
	if wc.HasWarnings() {
		t.Error("Expected no warnings after GetAndClear")
	}
}

func TestAddWarningfNilSafe(t *testing.T) {
	// Should not panic with nil collector
	AddWarningf(nil, "test %d", 1)
}
