package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alayacore/alayacore/internal/llm"
)

// readText is a test helper: run read_file and flatten its text parts.
func readText(t *testing.T, in ReadFileInput) (string, error) {
	t.Helper()
	parts, err := executeReadFile(context.Background(), in)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if tp, ok := p.(*llm.TextPart); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String(), nil
}

// writeTemp writes content to a named file in a temp dir and returns the path.
func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The regression this guards: read_file used a bufio.Scanner with a 1MB buffer,
// so any file containing a longer single line failed with
// "bufio.Scanner: token too long" — and because the scanner had to tokenize
// those lines just to count past them, start_line/num_lines failed too. Files
// like minified bundles and single-line JSON were simply unreadable, and the
// truncation header told the model to use the workaround that also failed.
//
// What a long line comes back as is the text budget's business now, not the
// per-line cap's: the cap still bounds what one line costs the scanner
// (lineReader, and TestLineReaderMarksALineOverTheCap for the marker it
// attaches), but no reply can be larger than the budget, so a range read that
// lands on a 3MB line is cut at the budget and the header says where the
// remainder is reachable. The properties this test keeps are the ones that broke:
// the read must succeed, must say it is truncated, and must stay bounded.
func TestReadFileLineLongerThanScannerLimit(t *testing.T) {
	path := writeTemp(t, "huge.txt", []byte(strings.Repeat("x", 3*1024*1024)+"\nsecond\n"))

	full, err := readText(t, ReadFileInput{Path: path})
	if err != nil {
		t.Fatalf("full read failed: %v", err)
	}
	if full == "" {
		t.Fatal("full read returned nothing")
	}

	// A range that skips the long line must work, and one that includes it
	// must return a truncated line instead of failing.
	second, err := readText(t, ReadFileInput{Path: path, StartLine: 2, NumLines: 1})
	if err != nil {
		t.Fatalf("range past the long line failed: %v", err)
	}
	if second != "second" {
		t.Errorf("range start_line=2 = %q, want %q", second, "second")
	}

	first, err := readText(t, ReadFileInput{Path: path, StartLine: 1, NumLines: 1})
	if err != nil {
		t.Fatalf("range on the long line failed: %v", err)
	}
	if !strings.Contains(first, "longer than the 64KB text budget") {
		t.Errorf("oversized line was not reported as cut: %.160q…", first)
	}
	if len(first) > maxToolTextBytes+512 {
		t.Errorf("a single long line returned %d bytes for a %d byte budget", len(first), maxToolTextBytes)
	}
}

// The per-line cap is a memory bound on the scan, and this is the mechanism it
// uses: keep the first maxLineBytes of the line, drop the rest without
// buffering it, and still count what was dropped as the same one line.
//
// It is pinned here rather than through read_file because no reply can carry
// this marker any more: the text budget (64KB) is the smaller bound on both the
// full and the range path, so it decides where the reply stops and says so in
// its own words. The cap still has to hold — it is what keeps a file made of one
// 3GB line from being assembled in memory before the budget ever sees it — and
// it must not split a line into two logical lines while doing it, which is the
// half the differential test cannot reach.
func TestLineReaderMarksALineOverTheCap(t *testing.T) {
	path := writeTemp(t, "cap.txt", []byte(strings.Repeat("x", maxLineBytes+10)+"\ntail\n"))
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	lr := newLineReader(f)
	line, err := lr.nextLine()
	if err != nil {
		t.Fatalf("the line over the cap must be readable, not an error: %v", err)
	}
	if !strings.HasSuffix(line, truncatedLineMarker) {
		t.Errorf("line over the cap is not marked: %.60q…", line)
	}
	if len(line) > maxLineBytes+len(truncatedLineMarker) {
		t.Errorf("the retained line is %d bytes, over the %d cap", len(line), maxLineBytes)
	}

	next, err := lr.nextLine()
	if err != nil {
		t.Fatalf("line after the capped one: %v", err)
	}
	if next != "tail" {
		t.Errorf("line after the capped one = %q, want %q — the dropped bytes became a line of their own", next, "tail")
	}
}

// maxToolTextBytes is documented to the model as the read limit. A single line
// larger than it used to be emitted whole (a 700KB line was returned against a
// 64KB limit) because "always keep at least one line" outran the cap.
func TestReadFileRespectsTheStatedSizeLimit(t *testing.T) {
	path := writeTemp(t, "bigline.txt", []byte(strings.Repeat("y", 700*1024)+"\ntail\n"))

	out, err := readText(t, ReadFileInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// The header and the marker are overhead; the payload must fit the budget.
	if len(out) > maxToolTextBytes+512 {
		t.Errorf("returned %d bytes for a %d byte limit", len(out), maxToolTextBytes)
	}
	if !strings.Contains(out, budgetTruncationMarker) {
		t.Errorf("expected the budget marker, got %.120q", out)
	}
}

// A range past the end of the file used to return "", identical to what an
// empty file returns. A model that cannot tell the two apart may conclude the
// file has no content and overwrite it.
func TestReadFileOutOfRangeRangeIsExplicit(t *testing.T) {
	path := writeTemp(t, "three.txt", []byte("a\nb\nc\n"))

	out, err := readText(t, ReadFileInput{Path: path, StartLine: 99, NumLines: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "past the end of the file") || !strings.Contains(out, "3 lines") {
		t.Errorf("expected an out-of-range explanation, got %q", out)
	}

	empty := writeTemp(t, "empty.txt", nil)
	out, err = readText(t, ReadFileInput{Path: empty, StartLine: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("expected an empty-file explanation, got %q", out)
	}
}

// Truncation must never split a multi-byte rune: the marker would arrive behind
// half a character and the model would see mojibake.
func TestReadFileTruncationKeepsUTF8Valid(t *testing.T) {
	path := writeTemp(t, "mb.txt", []byte(strings.Repeat("é", 900*1024)))

	out, err := readText(t, ReadFileInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(out, budgetTruncationMarker)
	if !utf8.ValidString(body) {
		t.Error("truncated content is not valid UTF-8")
	}

	ranged, err := readText(t, ReadFileInput{Path: path, StartLine: 1, NumLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(ranged) {
		t.Error("per-line truncation split a rune")
	}
}

// lineReader must match the old ScanLines behavior of dropping a carriage
// return before the newline, and of yielding a final line that has no newline.
func TestReadFileLineReaderParity(t *testing.T) {
	tests := []struct {
		name  string
		input string
		start int
		count int
		want  string
	}{
		{"crlf drops the CR", "one\r\ntwo\r\n", 1, 2, "one\ntwo"},
		{"final line without newline", "one\ntwo", 1, 0, "one\ntwo"},
		{"empty last line before EOF", "one\n", 1, 0, "one"},
		{"range to end", "a\nb\nc\n", 2, 0, "b\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemp(t, "in.txt", []byte(tt.input))
			got, err := readText(t, ReadFileInput{Path: path, StartLine: tt.start, NumLines: tt.count})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("read_file(%d,%d) of %q = %q, want %q", tt.start, tt.count, tt.input, got, tt.want)
			}
		})
	}
}

// Files under the limit must be returned byte-for-byte, exactly as before: the
// small-file path is io.ReadAll and does not normalize line endings.
func TestReadFileSmallFileUnchanged(t *testing.T) {
	const content = "line1\r\nline2\r\ntrailing spaces   \r\n"
	path := writeTemp(t, "small.txt", []byte(content))

	got, err := readText(t, ReadFileInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Errorf("small file changed:\n got %q\nwant %q", got, content)
	}
}
