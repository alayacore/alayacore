package terminal

// Regression test: the fold arrow must not accumulate across renders.
// windowFragment prepends the arrow to the window's first visible row;
// a previous in-place mutation of the cached border.lines prepended
// another arrow on every render (▸▸▸▸▸▸▸▸▸▸ USER ...).

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestArrowNotAccumulatedAcrossRenders verifies repeated GetAll renders
// keep exactly one arrow per folded line.
func TestArrowNotAccumulatedAcrossRenders(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "say hello")
	wb.AppendOrUpdate(tlv.TagAssistantR, "r1", `The user says "say hello".`)

	// Render several times (each triggers windowFragment → arrow prepend).
	for i := 0; i < 5; i++ {
		wb.SetViewportPosition(0, 8)
		_ = wb.GetAll(-1, false)
	}

	out := stripANSI(wb.GetAll(-1, false))
	// Each folded line shows exactly one arrow.
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, foldArrow) {
			count := strings.Count(line, foldArrow)
			if count != 1 {
				t.Errorf("folded line has %d arrows, want 1: %q", count, line)
			}
		}
	}
}
