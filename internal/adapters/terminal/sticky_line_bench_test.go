package terminal

// Benchmark for the sticky window line: the same oversize window rendered
// with its own line in its natural place (top of the viewport, viewport at
// the window's first row) and with it pinned (viewport one row further down,
// so the line is cut off and the pin draws it). Both go through the
// production renderVirtual, so the delta between the two sub-benchmarks IS
// the cost of the feature — and the contract is that it is one cached-row
// read plus one write, i.e. within noise of the same viewport without it
// (TestStickyPinAddsNoAllocations pins the allocation side).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// stickyBenchSession builds a session that is realistic for the pinned case:
// a very tall expanded answer (the window a reader scrolls through), behind
// a folded prompt and followed by a column of folded steps.
func stickyBenchSession() *WindowBuffer {
	wb := NewWindowBuffer(120, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "summarize the whole repo")
	lines := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		lines = append(lines, fmt.Sprintf("answer paragraph %03d with enough words to fill a row", i))
	}
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.Join(lines, "\n"))
	for i := 0; i < 60; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantR, fmt.Sprintf("r%02d", i), "short reasoning")
	}
	_ = wb.GetTotalLines()
	return wb
}

func BenchmarkStickyLineViewportRender(b *testing.B) {
	wb := stickyBenchSession()
	const height = 40

	b.Run("unpinned", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			wb.SetViewportPosition(1, height) // the window's own line is on screen
			_ = wb.GetAll(-1, false)
		}
	})
	b.Run("pinned", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			wb.SetViewportPosition(150, height) // deep into the window: pinned
			_ = wb.GetAll(-1, false)
		}
	})
	b.Run("pinned-scrolling", func(b *testing.B) {
		// One row per frame: the count on the pinned row changes, so its memo
		// misses and the row is rebuilt — the pessimistic case. The delta
		// against `pinned` is what a scroll step pays for the count.
		idx, _ := wb.LookupID("a1")
		winStart, winEnd := wb.GetWindowLineRange(idx)
		y := winStart + 1
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			y++
			if y+height >= winEnd {
				y = winStart + 1
			}
			wb.SetViewportPosition(y, height)
			_ = wb.GetAll(-1, false)
		}
	})
	b.Run("pinned-cursor-on-it", func(b *testing.B) {
		b.ReportAllocs()
		idx, _ := wb.LookupID("a1")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			wb.SetViewportPosition(150, height)
			_ = wb.GetAll(idx, false) // the pinned row is the highlighted variant
		}
	})
}
