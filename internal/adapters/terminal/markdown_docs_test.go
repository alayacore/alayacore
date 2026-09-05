package terminal

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// docExampleSources backs the `<!-- @example src=NAME width=N -->` markers in
// docs/markdown-rendering.md. Keep in sync with that document: a marker naming
// an absent source fails the test below.
var docExampleSources = map[string]string{
	"models": "| Model | Context | Price |\n|---|---|---|\n| qwen3:32b | 128K | local |\n| llama3.1 | 8K | local |\n| gpt-4o | 128K | $2.50/M |",
	"df": "| Filesystem | Mounted on | Type | Size | Used |\n|---|---|---|---|---|\n" +
		"| /dev/nvme0n1p2 | /home/wallace/projects/alayacore | ext4 | 916G | 703G |\n" +
		"| tmpfs | /run/user/1000/doc | tmpfs | 16G | 2.1G |",
	"cjk": "| 模型 | 上下文 | 说明 |\n|---|---:|---|\n" +
		"| qwen3:32b | 128K | 本地跑，需要 20G 显存，工具调用稳定 |\n| llama3.1 | 8K | 只适合短任务 |",
	"pipes_ascii": `| cmd | out |
|---|---|
| a\|b | ok |`,
	"pipes_box": "| cmd | out |\n|---|---|\n| a│b | ok |",
	"ps": "| PID | USER | %CPU | %MEM | VSZ | RSS | TTY | STAT | START | TIME |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"| 1 | root | 0.0 | 0.1 | 169444 | 13204 | ? | Ss | Jan12 | 0:12 |\n" +
		"| 2888 | wallace | 3.2 | 4.8 | 1248320 | 312880 | pts/3 | Rl+ | 09:14 | 4:31 |",
}

const docPath = "../../../docs/markdown-rendering.md"

// readDocForTest reads the document with its line endings normalized to LF.
//
// On the Windows runner this file reaches the working tree as CRLF: Git for
// Windows is installed with core.autocrlf=true and actions/checkout never
// changes it, so an LF-committed text file is smudged on checkout. The renderer
// joins its lines with "\n", so comparing the raw bytes of a checkout asserts
// the wrong thing — all seven examples went red there at once, for a cause no
// edit to the document could either produce or cure. Normalizing first makes the
// assertion about what the document says, which is how the product's own
// key-value readers already treat CRLF input: as the same text.
func readDocForTest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("cannot read the document: %v", err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// docExampleCounts is the census each source must appear with; wantExampleMarkers
// is their sum. Deliberately explicit: an assertion derived from len(map) let a
// deleted marker and an added source cancel each other out.
var docExampleCounts = map[string]int{
	"models": 1, "df": 2, "cjk": 1, "ps": 1, "pipes_ascii": 1, "pipes_box": 1,
}

const wantExampleMarkers = 7

// TestDocsMarkdownExamplesMatchRenderer re-renders every example the document
// shows and requires it to appear verbatim. Rendered examples go stale the
// moment the layout changes, and this document shows three distinct layouts
// (grid, grid with wrapped rows, record form) plus the exact widths that
// produce them.
func TestDocsMarkdownExamplesMatchRenderer(t *testing.T) {
	text := readDocForTest(t)
	// The marker is `<!-- @example src=NAME width=N -->`; keying on the opening
	// alone keeps every field a k=v pair.
	const marker = "<!-- @example "

	seen := 0
	docMarkerCounts := map[string]int{}
	for lineNo, line := range strings.Split(text, "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		seen++
		// Both indices must be absolute: strings.Index(line[i:], ...) is
		// relative to line[i:], so shift it back before slicing.
		end := i + strings.Index(line[i:], "-->")
		fields := map[string]string{}
		for _, f := range strings.Fields(line[i+len(marker) : end]) {
			if k, v, ok := strings.Cut(f, "="); ok {
				fields[k] = v
			}
		}
		src := fields["src"]
		docMarkerCounts[src]++
		in, ok := docExampleSources[src]
		if !ok {
			t.Errorf("line %d: unknown src %q — add it to docExampleSources or drop the marker", lineNo+1, src)
			continue
		}
		width, err := strconv.Atoi(fields["width"])
		if err != nil {
			t.Errorf("line %d: width %q is not a number", lineNo+1, fields["width"])
			continue
		}
		want := renderMarkdownTables(in, width)
		if !strings.Contains(text, want) {
			t.Errorf("line %d (src=%s width=%d): the document no longer shows what the renderer produces.\n\nwant:\n%s",
				lineNo+1, src, width, want)
		}
		// Provenance must sit directly above the block it describes.
		if !strings.HasPrefix(strings.TrimLeft(text[strings.Index(text, line)+len(line):], "\n"), "```") {
			t.Errorf("line %d: marker is not immediately followed by a fenced block", lineNo+1)
		}
	}
	if seen != wantExampleMarkers {
		t.Errorf("%d example markers found, expected %d — a marker was deleted or added without updating docExampleCounts",
			seen, wantExampleMarkers)
	}
	for src, want := range docExampleCounts {
		if got := docMarkerCounts[src]; got != want {
			t.Errorf("src %q appears %d times, expected %d", src, got, want)
		}
	}
}

// TestDocsMarkdownBoundsAreMeasured pins the numbers in the document's bound
// table, which are the only figures there that the layout can silently change.
func TestDocsMarkdownBoundsAreMeasured(t *testing.T) {
	cases := []struct {
		src    string
		n      int
		expect string // "<n> cells" as printed in the table
	}{
		{"models", 3, "13 cells"},
		{"df", 5, "21 cells"},
		{"cjk", 3, "16 cells"},
		{"ps", 10, "41 cells"},
	}
	text := readDocForTest(t)
	for _, tc := range cases {
		in := docExampleSources[tc.src]
		first := 0
		for w := 1; w <= 160; w++ {
			if strings.Contains(renderMarkdownTables(in, w), "│") {
				first = w
				break
			}
		}
		if got := fmt.Sprintf("%d cells", first); got != tc.expect {
			t.Errorf("%s: %d-column table frames from %d, document says %q", tc.src, tc.n, first, tc.expect)
		}
		if !strings.Contains(text, tc.expect) {
			t.Errorf("document no longer states %q for %s", tc.expect, tc.src)
		}
	}
}
