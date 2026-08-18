package terminal

import (
	"strings"
	"testing"
)

func benchLine(n int) string {
	// Mixed ASCII/CJK line, ~2 cells per char on average.
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			parts[i] = "你"
		} else {
			parts[i] = "a"
		}
	}
	return strings.Join(parts, "")
}

func BenchmarkInputFieldInsertLongLine(b *testing.B) {
	// Repeatedly type into a 4000-cell line (scrolling active).
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := NewInputField()
		g = g.WithWidth(60)
		g = g.WithValue(benchLine(4000)).CursorEnd()
		for j := 0; j < 20; j++ {
			g, _ = g.Update(KeyPressMsg{Text: "你", Code: '你'})
		}
	}
}

func BenchmarkInputFieldMoveLongLine(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := NewInputField()
		g = g.WithWidth(60)
		g = g.WithValue(benchLine(4000)).CursorEnd()
		for j := 0; j < 100; j++ {
			g, _ = g.handleKeyMsg(KeyPressMsg{Text: "left", Code: 0})
		}
		for j := 0; j < 100; j++ {
			g, _ = g.handleKeyMsg(KeyPressMsg{Text: "right", Code: 0})
		}
	}
}

func BenchmarkInputFieldRunesWidthShort(b *testing.B) {
	line := []rune(benchLine(50))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if runesWidth(line) != 75 {
			b.Fatal("bad width")
		}
	}
}

func BenchmarkInputFieldRunesWidthLong(b *testing.B) {
	line := []rune(benchLine(10000))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if runesWidth(line) != 15000 {
			b.Fatal("bad width")
		}
	}
}

func BenchmarkInputFieldViewLongLine(b *testing.B) {
	b.ReportAllocs()
	g := NewInputField()
	g = g.WithWidth(60)
	g = g.WithValue(benchLine(2000)).CursorEnd()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.View()
	}
}
