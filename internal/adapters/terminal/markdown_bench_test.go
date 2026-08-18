package terminal

// Benchmarks for markdown table rendering: the pure transform cost and
// the streaming cost in markdown mode (per-delta re-render vs the raw
// incremental path).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// BenchmarkRenderMarkdownTables measures the pure transform cost (no wrap).
func BenchmarkRenderMarkdownTables_Small(b *testing.B) {
	content := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |\n| Harry Potter | male | 10 |"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		renderMarkdownTables(content, 80)
	}
}

func BenchmarkRenderMarkdownTables_Large(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("| col1 | col2 | col3 | col4 | col5 |\n")
	sb.WriteString("|---|---|---|---|---|\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "| value %d | some longer text %d | 42 | 3.14 | note %d |\n", i, i*7, i)
	}
	content := sb.String()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		renderMarkdownTables(content, 120)
	}
}

// markdownPlainDeltas is a token-stream of plain text (no table rows).
var markdownPlainDeltas = []string{
	"Here is the ",
	"first paragraph of ",
	"assistant text. ",
	"It has no tables, ",
	"just ordinary words ",
	"streamed in pieces.\n",
	"\nSecond paragraph ",
	"continues the ",
	"incremental path.\n",
	"\nThird paragraph with ",
	"more words ",
	"and more words ",
	"and more words ",
	"and more words ",
	"and more words ",
	"and more words ",
	"and more words ",
	"and more words.\n",
}

// markdownTableDeltas streams a table row by row (header, delimiter, body).
func markdownTableDeltas(rows int) []string {
	deltas := []string{"| name | gender | age |\n", "|---|---|---|\n"}
	for i := 0; i < rows; i++ {
		deltas = append(deltas, fmt.Sprintf("| Walllace Gibbon %d | male | %d |\n", i, 100+i))
	}
	return deltas
}

// runMarkdownStreaming mirrors the real tick flow: AppendFromTLV, then
// TryLineCount (the ensureLineHeights fast path); on a miss, BuildInner
// re-renders (exactly what ensureLineHeights does on cache invalidation).
func runMarkdownStreaming(r *textRenderer, deltas []string, styles *Styles) {
	for _, d := range deltas {
		r.AppendFromTLV(tlv.TagAssistantT, d)
		if _, ok := r.TryLineCount(80); !ok {
			r.BuildInner(80, false, styles)
		}
	}
}

// BenchmarkMarkdownStreaming_PlainDeltas measures mdMode streaming where
// every delta is ordinary text: with the sniffing incremental path these
// go through appendDeltaToVisualLines; before it every delta re-rendered
// the whole window.
func BenchmarkMarkdownStreaming_PlainDeltas(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
		runMarkdownStreaming(r, markdownPlainDeltas, styles)
	}
}

// BenchmarkMarkdownStreaming_TableDeltas measures mdMode streaming where
// each delta extends a table (a new body row): column widths may change,
// so every delta re-renders (full path — the local table re-render is a
// future optimization).
func BenchmarkMarkdownStreaming_TableDeltas(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	deltas := markdownTableDeltas(20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
		runMarkdownStreaming(r, deltas, styles)
	}
}

// BenchmarkMarkdownStreaming_RawMode is the raw-mode baseline for the
// same plain deltas: always incremental.
func BenchmarkMarkdownStreaming_RawMode(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := &textRenderer{tag: tlv.TagAssistantT}
		runMarkdownStreaming(r, markdownPlainDeltas, styles)
	}
}
