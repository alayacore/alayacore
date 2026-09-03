package providers

import (
	"math"
	"testing"
)

// openaiToolPos's contract is about slots it must never occupy: the two flat
// fields have their own declared positions, and a value that is zero or negative
// reads back as "not declared" and silently drops the claim. A raw index is
// server-supplied, so both failure modes are reachable without the clamping:
// -1 computed slot 2 (the text block's), and an index near math.MaxInt overflowed
// to a negative.
func TestOpenAIToolPositionsCannotCollideOrVanish(t *testing.T) {
	reserved := map[int]string{
		openaiReasoningPos: "reasoning",
		openaiTextPos:      "text",
	}
	// The values chosen to sit either side of the clamp points.
	for _, raw := range []int{
		math.MinInt, -1000, -2, -1, 0, 1, 2, 7, 1 << 20, (1 << 20) + 1,
		math.MaxInt - 1, math.MaxInt,
	} {
		got := openaiToolPos(raw)
		if owner, taken := reserved[got]; taken {
			t.Errorf("openaiToolPos(%d) = %d, which is the %s block's slot", raw, got, owner)
		}
		if got <= 0 {
			t.Errorf("openaiToolPos(%d) = %d; a non-positive slot reads back as undeclared", raw, got)
		}
		if got < openaiToolPosBase {
			t.Errorf("openaiToolPos(%d) = %d, before the first tool slot %d", raw, got, openaiToolPosBase)
		}
	}
}

// Well-behaved indices must keep the protocol's relative order, since that is the
// order the model asked for its parallel calls.
func TestOpenAIToolPositionsPreserveIndexOrder(t *testing.T) {
	if !(openaiToolPos(0) < openaiToolPos(1) && openaiToolPos(1) < openaiToolPos(2)) {
		t.Errorf("positions out of order: %d %d %d",
			openaiToolPos(0), openaiToolPos(1), openaiToolPos(2))
	}
}
