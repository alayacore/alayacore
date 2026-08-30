package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// Differential test: the new lineReader-based paths must agree with the
// original bufio.Scanner implementation for every input the scanner could
// handle at all. Where the old code errored (lines over 1MB), the new code is
// expected to differ — that is the bug fix — so those inputs are excluded here
// and covered by read_file_longline_test.go.

// oldReadLinesRange is the pre-fix implementation, verbatim in logic.
func oldReadLinesRange(file *os.File, startLine, numLines int) ([]string, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	currentLine := 1
	for scanner.Scan() {
		if currentLine < startLine {
			currentLine++
			continue
		}
		if numLines > 0 && len(lines) >= numLines {
			break
		}
		lines = append(lines, scanner.Text())
		currentLine++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// oldReadLargeTruncated is the pre-fix large-file path, verbatim in logic.
func oldReadLargeTruncated(file *os.File, totalSize int64) (string, int, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	var bytesRead int64
	totalLines := 0
	collecting := true

	for scanner.Scan() {
		totalLines++
		if collecting {
			line := scanner.Text()
			lineBytes := int64(len(line)) + 1
			if bytesRead+lineBytes > maxTextReadSize && len(lines) > 0 {
				collecting = false
				continue
			}
			lines = append(lines, line)
			bytesRead += lineBytes
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	// The original header, byte for byte.
	header := fmt.Sprintf(
		"[Lines 1-%d of %d | %.1fKB of %.1fKB shown]\n",
		len(lines), totalLines,
		float64(bytesRead)/1024, float64(totalSize)/1024,
	)
	return header + "\n" + strings.Join(lines, "\n"), totalLines, nil
}

func TestLineReaderMatchesOldScannerSemantics(t *testing.T) {
	const maxLine = 1 << 20

	corpus := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"single newline", "\n"},
		{"several newlines", "\n\n\n"},
		{"one line no terminator", "hello"},
		{"one line with terminator", "hello\n"},
		{"crlf", "a\r\nb\r\nc\r\n"},
		{"mixed line endings", "a\nb\r\nc"},
		{"trailing spaces", "a   \n  b\n"},
		{"tabs and unicode", "\t日本\t\némoji 🎉\n"},
		{"long-ish line under cap", strings.Repeat("x", 200000) + "\n"},
		{"blank line in middle", "a\n\nb\n"},
		{"whitespace only line", "   \n"},
		{"no newline at eof", "a\nb"},
		{"nul byte", "a\x00b\nc\n"},
		{"many lines", strings.Repeat("line\n", 500)},
	}

	for _, cc := range corpus {
		t.Run(cc.name, func(t *testing.T) {
			if len(cc.input) > maxLine {
				t.Skip("over the per-line cap: behavior intentionally differs")
			}
			path := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(path, []byte(cc.input), 0644); err != nil {
				t.Fatal(err)
			}

			// Compare across a spread of ranges, including out-of-range.
			type rng struct{ start, count int }
			ranges := []rng{{1, 0}, {1, 1}, {2, 1}, {1, 5}, {2, 0}, {99, 3}, {0, 3}, {3, 1}}

			for _, r := range ranges {
				newFile, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				newLines, _, errNew := readLinesRange(context.Background(), newFile, max(r.start, 1), r.count)
				newFile.Close()

				oldFile, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				oldLines, errOld := oldReadLinesRange(oldFile, max(r.start, 1), r.count)
				oldFile.Close()

				if (errNew == nil) != (errOld == nil) {
					t.Fatalf("start=%d count=%d: new err=%v old err=%v", r.start, r.count, errNew, errOld)
				}
				if strings.Join(newLines, "\n") != strings.Join(oldLines, "\n") {
					t.Errorf("start=%d count=%d:\n new = %q\n old = %q",
						r.start, r.count, strings.Join(newLines, "\n"), strings.Join(oldLines, "\n"))
				}
			}
		})
	}
}

// The header line the truncation path emits must keep its exact format, and
// the collected prefix must be identical for inputs the old code handled.
func TestTruncatedReadMatchesOldScannerSemantics(t *testing.T) {
	corpus := []struct {
		name  string
		input string
	}{
		{"uniform lines over cap", strings.Repeat("abcdefghij\n", 20000)},
		{"varying line sizes", strings.Repeat("a\n", 3000) + strings.Repeat("bbbbbbbbbbbbbbbbbbbb\n", 5000)},
		{"crlf over cap", strings.Repeat("hello world\r\n", 9000)},
		{"blank lines over cap", strings.Repeat("\n", 100000)},
		{"no trailing newline at eof", strings.Repeat("x", 70000) + "\n" + "tail"},
	}

	for _, cc := range corpus {
		t.Run(cc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "big.txt")
			if err := os.WriteFile(path, []byte(cc.input), 0644); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() <= maxTextReadSize {
				t.Skip("input is not over the truncation threshold")
			}

			newFile, _ := os.Open(path)
			newParts, errNew := readLargeFileTruncated(newFile, info.Size())
			newFile.Close()
			if errNew != nil {
				t.Fatalf("new path errored: %v", errNew)
			}
			newText := firstText(t, newParts)

			oldFile, _ := os.Open(path)
			oldText, _, errOld := oldReadLargeTruncated(oldFile, info.Size())
			oldFile.Close()
			if errOld != nil {
				t.Skipf("old path errored (intentionally-fixed case): %v", errOld)
			}

			// The old code could exceed the budget on a huge first line; where
			// it stayed within budget the outputs must match exactly.
			if len(oldText) <= maxTextReadSize+512 && newText != oldText {
				t.Errorf("output changed:\n new=%.160q\n old=%.160q", newText, oldText)
			}
			// The header format is part of the tool's contract with the model.
			header := strings.SplitN(newText, "\n", 2)[0]
			if !strings.HasPrefix(header, "[Lines 1-") || !strings.Contains(header, " of ") ||
				!strings.Contains(header, "KB of ") {
				t.Errorf("header format changed: %q", header)
			}
		})
	}
}

func firstText(t *testing.T, parts []llm.ContentPart) string {
	t.Helper()
	var b strings.Builder
	for _, p := range parts {
		if tp, ok := p.(*llm.TextPart); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}
