package terminal

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkScrollViewSetContent benchmarks ScrollView.SetContent at various sizes.
func BenchmarkScrollViewWithContent(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	for _, n := range sizes {
		content := strings.Repeat("line of text for testing\n", n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			sv := NewScrollView(80, 40)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sv.WithContent(content)
			}
		})
	}
}

// BenchmarkScrollViewView benchmarks ScrollView.View at various sizes.
func BenchmarkScrollViewView(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	for _, n := range sizes {
		content := strings.Repeat("line of text for testing\n", n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			sv := NewScrollView(80, 40)
			sv.WithContent(content)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = sv.View()
			}
		})
	}
}

// BenchmarkScrollViewScroll benchmarks scrolling through content.
func BenchmarkScrollViewScroll(b *testing.B) {
	sv := NewScrollView(80, 40)
	sv.WithContent(strings.Repeat("line of text for testing\n", 1000))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sv.ScrollDown(1)
		if sv.AtBottom() {
			sv.GotoTop()
		}
	}
}
