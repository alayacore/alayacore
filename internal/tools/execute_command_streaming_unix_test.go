//go:build !windows

package tools

// The preview a streaming command shows comes from the same bytes the
// authoritative result is built from, and the two must not be confused. A read
// can end inside a character — it is a pipe, and the boundary is wherever the
// writer's chunks happen to land — so the current line can hold the first bytes
// of a character whose rest has not arrived. Every byte that leaves this package
// on its way to a terminal goes through a json.Marshal, which replaces an
// ill-formed byte with U+FFFD: an untrimmed preview is a box on screen for a
// character that is still arriving.
//
// These two tests are the trim from outside, and they are deliberately free of
// timing — the command writes the bytes and exits, so the split is where the
// code says it is rather than where a sleep hopes it is. The split itself is
// taken apart in execute_command_streaming_test.go, which hands the writer the
// two halves as two writes; what is here is the end: that the result still
// carries the child's bytes unrepaired, which is what says the trim was put on
// the view and not on the capture.

import (
	"context"
	"testing"
	"unicode/utf8"
)

// TestExecuteCommandStreamingPreviewNeverCarriesHalfACharacter writes one byte of
// a 3-byte character and stops.
func TestExecuteCommandStreamingPreviewNeverCarriesHalfACharacter(t *testing.T) {
	var previews []string
	contents, err := executeCommandStreaming(context.Background(), ExecuteCommandInput{
		Command: `printf 'progress \342'`, // █ is E2 96 88; this is its first byte and nothing more
	}, func(s string) { previews = append(previews, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The authoritative result is the child's bytes, unrepaired: the box in the
	// tool's result is the terminal's business, not this package's.
	if got := extractText(contents); got != "progress \xe2" {
		t.Errorf("result = %q, want the child's bytes as sent", got)
	}
	if len(previews) == 0 {
		t.Fatal("no preview was delivered")
	}
	for i, p := range previews {
		if !utf8.ValidString(p) {
			t.Errorf("preview %d = %q is not well-formed, so it reaches a terminal as U+FFFD", i, p)
		}
	}
	if last := previews[len(previews)-1]; last != "progress " {
		t.Errorf("last preview = %q, want the line without the byte that cannot be a character", last)
	}
}

// TestExecuteCommandStreamingPreviewKeepsAWholeCharacter is the other side: a
// character the child did send whole is shown whole. The trim is not a filter
// that removes anything unfamiliar — it holds exactly the bytes a character
// could still arrive to complete.
func TestExecuteCommandStreamingPreviewKeepsAWholeCharacter(t *testing.T) {
	var previews []string
	contents, err := executeCommandStreaming(context.Background(), ExecuteCommandInput{
		Command: `printf 'progress \342\226\210 42%%\n'`,
	}, func(s string) { previews = append(previews, s) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := extractText(contents); got != "progress █ 42%\n" {
		t.Errorf("result = %q, want %q", got, "progress █ 42%\n")
	}
	if len(previews) == 0 {
		t.Fatal("no preview was delivered")
	}
	if last := previews[len(previews)-1]; last != "progress █ 42%" {
		t.Errorf("last preview = %q, want %q", last, "progress █ 42%")
	}
}
