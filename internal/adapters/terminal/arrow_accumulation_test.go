package terminal

// Regression test: the fold marker must not accumulate across renders.
// Row 0 is built once per cache generation with its marker already in it
// (Window.Render → buildExpandHeader), and every path that touches row 0 —
// windowFragment's cursor swap, the pinned row, the cursor row — REPLACES
// the row rather than prepending to it. A previous in-place mutation of the
// cached rows prepended another marker on every render
// (++++++++++ USER ...).

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

	// Render several times (each re-renders the row that carries the marker).
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
