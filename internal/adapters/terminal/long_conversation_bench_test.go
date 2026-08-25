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
	// Viewport shows ~20 windows (10 rows each + borders).
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
