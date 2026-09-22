package auth

import (
	"strings"
	"testing"
)

// TestReadCappedRejectsOversizedBody: a body over the cap must be reported as
// too large. Cutting it off instead would surface later as a JSON parse error,
// which blames the document rather than its size.
func TestReadCappedRejectsOversizedBody(t *testing.T) {
	// At the cap is still legitimate.
	if _, err := readCapped(strings.NewReader(strings.Repeat("a", maxResponseBytes))); err != nil {
		t.Errorf("body at the cap was rejected: %v", err)
	}

	_, err := readCapped(strings.NewReader(strings.Repeat("a", maxResponseBytes+1)))
	if err == nil {
		t.Fatal("oversized body was accepted")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want it to name the size limit", err)
	}
}
