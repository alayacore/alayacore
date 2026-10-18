package providers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/sse"
)

// TestSSEErrorNamesTheCause pins the one thing this package adds to the shared
// SSE reader: the wording. The reader reports the condition (sse.ErrTooLarge);
// a provider names the cause a user can act on — a model that generated an
// oversized tool call — and must not rewrite an unrelated failure.
func TestSSEErrorNamesTheCause(t *testing.T) {
	tooBig := fmt.Errorf("%w: 9MB", sse.ErrTooLarge)
	got := sseErr(tooBig)
	if !strings.Contains(got.Error(), "exceeded") || !strings.Contains(got.Error(), "oversized tool call") {
		t.Errorf("sseErr(%v) = %v, want it to name the size and the likely cause", tooBig, got)
	}

	boom := errors.New("connection reset")
	if err := sseErr(boom); err != boom {
		t.Errorf("sseErr(%v) = %v, want an unrelated error passed through unchanged", boom, err)
	}
}
