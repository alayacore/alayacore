package providers

// Shared SSE reader limits and error normalization.

import (
	"bufio"
	"errors"
	"fmt"
)

// sseMaxLineBytes bounds a single SSE line from a provider. A line over the
// buffer means the provider put an entire oversized payload (a huge tool call,
// say) on one line instead of streaming it in deltas.
const sseMaxLineBytes = 1 << 20 // 1MB

// sseScannerErr normalizes a reader failure so an over-long line reads as what
// it is. The raw bufio error — "bufio.Scanner: token too long" — names an
// internal mechanism rather than the provider behavior that produced it, and
// both Anthropic and OpenAI can hit it the same way.
func sseScannerErr(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("SSE line exceeded %dMB limit — the model may have generated an oversized tool call", sseMaxLineBytes>>20)
	}
	return err
}
