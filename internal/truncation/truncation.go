// Package truncation provides shared text truncation utilities for the agent.
//
// All functions guarantee that their output is valid UTF-8 — they never split
// a multi-byte character.  Callers may use any function without worrying about
// encoding corruption.
//
// There are two truncation strategies, each suited to a different use case:
//
//   - Lines: keeps the first N non-empty lines of line-oriented output
//     (search results).  Useful for tools like ripgrep whose output is
//     naturally line-based.
//
//   - Front: keeps the front of text (command output, tool results in
//     compacted history).  Uses a byte budget so scripts with high
//     bytes-per-rune ratios (Chinese, Japanese, Korean) are handled
//     fairly without any approximation.
package truncation

import (
	"bufio"
	"bytes"
	"unicode/utf8"
)

// Marker is appended to truncated text so the LLM can recognize that
// content was removed.
const Marker = "\n[truncated]"

// ---------------------------------------------------------------------------
// Lines
// ---------------------------------------------------------------------------

// Lines returns the first maxLines non-empty lines from input.
// It returns (wasTruncated, result).
//
// UTF-8 safety: Lines never slices the input by byte offset.  It reads
// complete lines via bufio.Scanner, which internally respects line endings
// and never breaks mid-character.  Each line written to the output buffer
// is an intact, valid-UTF-8 substring of the original input.  Therefore
// Lines is safe for multi-byte encodings (CJK, emoji, etc.) without any
// special handling.
func Lines(input string, maxLines int) (bool, string) {
	scanner := bufio.NewScanner(bytes.NewBufferString(input))
	var buf bytes.Buffer
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		count++
		if count > maxLines {
			return true, buf.String()
		}
		if count > 1 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}
	return false, buf.String()
}

// ---------------------------------------------------------------------------
// Front
// ---------------------------------------------------------------------------

// Front truncates text so that the total output (content + marker) fits
// within budget bytes.  Returns the original text unchanged if it fits.
//
// budget is the maximum total byte count of the returned string (content + marker).
// Each rune pays its actual byte cost, so any mix of ASCII and multi-byte
// characters is handled fairly without approximation.
func Front(text string, budget int, marker string) string {
	if text == "" {
		return text
	}

	textBytes := len(text)
	if textBytes <= budget {
		return text
	}

	contentBudget := budget - len(marker)
	if contentBudget <= 0 {
		return marker
	}

	// Walk runes, accumulate actual byte cost, find the cut point.
	cut := 0
	for _, r := range text {
		runeLen := utf8.RuneLen(r)
		if cut+runeLen > contentBudget {
			break
		}
		cut += runeLen
	}

	return text[:cut] + marker
}
