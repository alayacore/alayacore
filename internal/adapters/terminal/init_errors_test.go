package terminal

import (
	"strings"
	"testing"
)

func TestInitErrorCollector(t *testing.T) {
	ec := &InitErrorCollector{}

	// Test adding errors (no trailing \n)
	ec.Addf("Test error %d", 1)
	ec.Addf("Another error")

	errs := ec.GetAndClear()
	if len(errs) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(errs))
	}

	if errs[0].Message != "Test error 1" {
		t.Errorf("Expected 'Test error 1', got '%s'", errs[0].Message)
	}

	if errs[1].Message != "Another error" {
		t.Errorf("Expected 'Another error', got '%s'", errs[1].Message)
	}

	// Verify no trailing newlines
	for i, e := range errs {
		if strings.HasSuffix(e.Message, "\n") {
			t.Errorf("Error %d should not have trailing newline: %q", i, e.Message)
		}
	}

	// Test that errors are cleared after retrieval
	errs = ec.GetAndClear()
	if len(errs) != 0 {
		t.Errorf("Expected 0 errors after GetAndClear, got %d", len(errs))
	}
}
