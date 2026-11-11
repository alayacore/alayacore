package terminal

// Long-conversation benchmarks for the body-dimming change: how does the
// overlay (blocked) render cost scale with conversation length?

import (
	"fmt"
	"strings"
	"testing"
)

// buildLongBuffer creates a window buffer with ~totalLines of content:
// many small windows (10 visual lines each) so a fixed viewport shows a
// constant number of windows regardless of total history length.
func buildLongBuffer(totalLines int) *WindowBuffer {
	styles := DefaultStyles()
	width := 100
	wb := NewWindowBuffer(width, styles)
	perWindow := 10
	nWindows := totalLines / perWindow
	if nWindows < 1 {
		nWindows = 1
	}
	line := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor ", 2)[:80]
	for i := 0; i < nWindows; i++ {
		id := fmt.Sprintf("at-%d", i)
		var sb strings.Builder
		for j := 0; j < perWindow; j++ {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		wb.AppendOrUpdate("AT", id, sb.String())
	}
	// Viewport shows ~20 windows (10 content rows + the window's own line).
	wb.SetViewportPosition(0, 220)
	return wb
}

func BenchmarkGetAllLongConversation(b *testing.B) {
	for _, total := range []int{1000, 10000, 100000} {
		wb := buildLongBuffer(total)
		b.Run(fmt.Sprintf("lines=%d", total), func(b *testing.B) {
			b.Run("blocked_switch", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					// Alternate blocked state so every call is a full
					// rebuild of the visible windows (overlay open/close
					// churn on a long session).
					_ = wb.GetAll(-1, i%2 == 0)
				}
			})
			b.Run("steady_normal", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					_ = wb.GetAll(-1, false)
				}
			})
			b.Run("steady_blocked", func(b *testing.B) {
				_ = wb.GetAll(-1, true) // warm the blocked cache
				for i := 0; i < b.N; i++ {
					_ = wb.GetAll(-1, true)
				}
			})
		})
	}
}

// BenchmarkCountedWindowMotion prices one counted motion through the key
// handler, which is the only place in the frame path whose cost grows with the
// transcript AND with how far the user asks to go: `Nj` is N presses by
// definition, and each press asks GetWindowLineRange for a line number that is
// a linear sum, so the run is O(count × distance) rather than O(count).
//
// This is the benchmark to watch, not a contract to defend. The ceiling is
// measured and written down in the performance analysis
// (docs/internal/virtual-rendering-performance.md → GetWindowLineRange): a
// saturated count costs microseconds up to a few thousand windows and a few
// milliseconds on a 50 000-line transcript, which is where the argument for a
// prefix-sum table starts — and it has not started, because that table is a
// second derived array that must stay correct across append, fold, markdown
// toggle, resize and theme switch, and a silent scroll-clamping bug is the
// worse trade.
func BenchmarkCountedWindowMotion(b *testing.B) {
	for _, total := range []int{1000, 10000, 50000} {
		dm := NewDisplayModel(buildLongBuffer(total), DefaultStyles()).
			WithHeight(40).WithDisplayFocused(true).updateContent()
		dm = dm.WithWindowCursor(0).EnsureWindowCursorVisible().updateContent()
		nWindows := dm.windowBuffer.WindowCount()

		// one press, from the top of the transcript: the uncounted motion.
		b.Run(fmt.Sprintf("lines=%d/press", total), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = dm.Update(KeyPressMsg(Key{Code: 'j'}))
			}
		})

		// the count at its ceiling: it stops on the last window, so this is the
		// whole transcript walked one placement rule at a time.
		b.Run(fmt.Sprintf("lines=%d/windows=%d/saturated-count", total, nWindows), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m := dm
				for range 6 { // 999999, typed
					m, _ = m.Update(KeyPressMsg(Key{Code: '9'}))
				}
				_, _ = m.Update(KeyPressMsg(Key{Code: 'j'}))
			}
		})
	}
}
