package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/testenv"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

// requireRipgrep gates the tests below on ripgrep, the binary search_content
// runs. A box without it skips them; `make test` refuses to start without it, so
// a run that was meant to be complete cannot lose them (see internal/testenv).
func requireRipgrep(t *testing.T) {
	t.Helper()
	testenv.RequireTool(t, "rg", "search_content's reference implementation")
}

func TestSearchContentBasicSearch(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	content := "hello world\nfoo bar\nhello again\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentNoMatches(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "nonexistent_pattern_xyz",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected 'No matches found', got %q", text)
	}
	// What the answer must carry is the part the caller cannot derive: ripgrep's
	// default filters. Where it searched is derivable — the system prompt states
	// the working directory and the path was the caller's own — so it is not
	// repeated here.
	if !strings.Contains(text, "gitignored") {
		t.Errorf("no-match text should say which files were skipped, got %q", text)
	}
}

func TestSearchContentEmptyPattern(t *testing.T) {
	_, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "",
	})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", err.Error())
	}
}

func TestSearchContentFileTypeFilter(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	goFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc test() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("func should not match\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "func",
		Path:     tmpDir,
		FileType: "go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentIgnoreCase(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello World\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected the case-sensitive search to report no matches, got %q", text)
	}

	result, err = executeSearchContent(context.Background(), SearchContentInput{
		Pattern:    "hello",
		Path:       tmpDir,
		IgnoreCase: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text = extractFirstText(result)
	if text == "" || strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected match with ignore_case=true, got %q", text)
	}
}

func TestSearchContentMaxLinesGlobal(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	for f := 0; f < 5; f++ {
		var content string
		for i := 0; i < 20; i++ {
			content += "match line\n"
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "file"+strconv.Itoa(f)+".txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "match",
		Path:     tmpDir,
		MaxLines: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.Contains(text, "matching lines") {
		t.Errorf("expected 'matching lines' in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected 'Results saved to:' in output, got:\n%s", text)
	}
}

func TestFormatSearchResultNoLineLimit(t *testing.T) {
	// max_lines of 0 (the default when omitted) disables the line cap:
	// 200 lines (well under the 64KB byte cap) must be returned inline
	// instead of being saved to a temp file.
	output := strings.Repeat("match line\n", 200)
	parts, err := formatSearchResult(searchResult{
		stdout:   newTestCapture(t, output),
		stderr:   newTestCapture(t, ""),
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := extractFirstText(parts)
	if text != output {
		t.Errorf("expected full %d-line output inline, got %d bytes", 200, len(text))
	}

	// A positive max_lines caps inline results: 200 lines with a limit of
	// 50 must be saved to a temp file.
	parts, err = formatSearchResult(searchResult{
		stdout:   newTestCapture(t, output),
		stderr:   newTestCapture(t, ""),
		exitCode: 0,
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	text = extractFirstText(parts)
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message with max_lines=50, got:\n%s", text)
	}

	// Even with max_lines = 0 the 64KB byte cap still applies.
	hugeLine := strings.Repeat("x", maxToolTextBytes+1)
	parts, err = formatSearchResult(searchResult{
		stdout:   newTestCapture(t, hugeLine),
		stderr:   newTestCapture(t, ""),
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text = extractFirstText(parts)
	if strings.Contains(text, "xxxxxxxxxx") {
		t.Errorf("oversized single-line output was returned inline: %.60q…", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message for oversized output, got:\n%s", text)
	}
}

func TestFormatSearchResultByteCap(t *testing.T) {
	// A single matching line can be arbitrarily large (minified JS,
	// base64 blobs) — the line-count cap alone would return it inline and
	// blow the context window. Output above maxToolTextBytes must be
	// saved to a temp file with a reference message instead.
	hugeLine := strings.Repeat("x", maxToolTextBytes+1)
	parts, err := formatSearchResult(searchResult{
		stdout:   newTestCapture(t, hugeLine),
		stderr:   newTestCapture(t, ""),
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := extractFirstText(parts)
	if strings.Contains(text, "xxxxxxxxxx") {
		t.Errorf("oversized single-line output was returned inline: %.60q…", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message, got:\n%s", text)
	}

	// At exactly the cap the output is still returned inline.
	atCap := strings.Repeat("y", maxToolTextBytes)
	parts, err = formatSearchResult(searchResult{
		stdout:   newTestCapture(t, atCap),
		stderr:   newTestCapture(t, ""),
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractFirstText(parts); len(text) != maxToolTextBytes {
		t.Errorf("output at the cap should be inline, got %d bytes", len(text))
	}
}

func TestSearchContentPatternLooksLikeFlag(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("--skill\n--help\nnormal text\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "--skill",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" || strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected match for '--skill' pattern, got %q", text)
	}
	if !strings.Contains(text, "--skill") {
		t.Errorf("expected output to contain '--skill', got %q", text)
	}
}

func TestSearchContentStreaming(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\nfoo bar\nhello again\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var previews []string
	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	}, func(text string) {
		previews = append(previews, text)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
	// flushPreview emits the final snapshot after completion, so a search
	// with output must deliver at least one preview frame.
	if len(previews) == 0 {
		t.Error("expected at least one preview snapshot")
	}
}

func TestSearchContentStreamingNoMatches(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	previewCount := 0
	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "nonexistent_pattern_xyz",
		Path:    tmpDir,
	}, func(string) {
		previewCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected 'No matches found', got %q", text)
	}
	// No output → no dirty state → no preview frames.
	if previewCount != 0 {
		t.Errorf("expected no previews for empty result, got %d", previewCount)
	}
}

func TestSearchContentStreamingEmptyPattern(t *testing.T) {
	_, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "",
	}, func(string) {})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", err.Error())
	}
}

func TestSearchContentStreamingMaxLines(t *testing.T) {
	requireRipgrep(t)

	tmpDir := t.TempDir()

	for f := 0; f < 5; f++ {
		var content string
		for i := 0; i < 20; i++ {
			content += "match line\n"
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "file"+strconv.Itoa(f)+".txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern:  "match",
		Path:     tmpDir,
		MaxLines: 5,
	}, func(string) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.Contains(text, "matching lines") {
		t.Errorf("expected 'matching lines' in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected 'Results saved to:' in output, got:\n%s", text)
	}
}

// ripgrep is an external dependency, and when it is missing the tool used to
// hand the model `exec: "rg": executable file not found in $PATH` — an error
// that names neither the fix nor an alternative. Hiding PATH (rather than
// requiring rg to be absent) makes this test independent of the host.
func TestSearchContentMissingRipgrepIsActionable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty directory: nothing is on PATH

	_, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "anything",
		Path:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected an error when ripgrep is not on PATH")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ripgrep") || !strings.Contains(msg, "PATH") {
		t.Errorf("error should name the missing dependency, got %q", msg)
	}
	if !strings.Contains(msg, "execute_command") {
		t.Errorf("error should point at a workable alternative, got %q", msg)
	}
	// The underlying exec failure must remain inspectable.
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error should wrap the exec failure, got %q", msg)
	}
}

// The reported match count must be the number of matches, not one more. The
// previous counter assumed the stream might lack a trailing newline and added
// 1 unconditionally, so every ripgrep result (which always ends with "\n") was
// over-reported by one line: 7 matches announced themselves as 8.
func TestSearchContentReportsExactLineCount(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()

	const wantMatches = 7
	if err := os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte(strings.Repeat("needle here\n", wantMatches)), 0644); err != nil {
		t.Fatal(err)
	}

	// Force the "save to file" path with max_lines below the match count,
	// since that is the message that carries the number.
	parts, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "needle",
		Path:     dir,
		MaxLines: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := extractFirstText(parts)

	want := "Search found 7 matching lines"
	if !strings.Contains(text, want) {
		t.Errorf("count line = %q, want it to contain %q (off-by-one regression)", text, want)
	}
	if strings.Contains(text, "8 matching lines") {
		t.Errorf("match count over-reported by one: %q", text)
	}
}

// A stopped search reports itself the way most tools here do: a bare error and
// no content. That is not a detail of this tool — it is the rule the agent
// layer applies to every tool (a result with no content is its error text, see
// llm.Agent.newToolOutput), and it is what carries the classification to the
// model instead of leaving it to Go callers. execute_command cannot use it —
// it always produces output — which is why that tool states the reason inside
// its own text (commandHeader).
//
// The context arrives already expired, so the test needs no slow search and no
// ripgrep: the deadline is checked before the exec error is inspected.
func TestSearchContentTimeoutIsReportedAsErrTimeout(t *testing.T) {
	orig := shell.DefaultCommandTimeout
	defer func() { shell.DefaultCommandTimeout = orig }()
	shell.DefaultCommandTimeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	content, err := executeSearchContent(ctx, SearchContentInput{Pattern: "package"})
	if err == nil {
		t.Fatal("expected an error for a search that cannot finish")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("error = %v, want errors.Is(err, ErrTimeout)", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to say the search timed out", err)
	}
	if len(content) != 0 {
		t.Errorf("a classified stop must carry no content, or the reason — which travels as the error — is dropped: content = %v", content)
	}
}

// A no-match answer must not claim a coverage the search did not have. ripgrep's
// defaults skip hidden, gitignored and binary files and never follow symlinks, so
// a pattern living only in one of those answers "No matches found" — a miss that
// is the tool's own doing and is indistinguishable from the pattern not existing,
// unless the answer says what it left out. Both halves are pinned here: the
// directory search misses the hidden file, and the same file matches when it is
// named as the path.
func TestSearchContentNoMatchesNamesTheSkippedFiles(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	hidden := filepath.Join(dir, ".hidden.txt")
	if err := os.WriteFile(hidden, []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	parts, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "needle",
		Path:    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(parts)
	if !strings.HasPrefix(text, "No matches found") {
		t.Fatalf("expected the directory search to miss the hidden file, got %q", text)
	}
	for _, want := range []string{"hidden", "gitignored", "binary", "symlink"} {
		if !strings.Contains(text, want) {
			t.Errorf("no-match text should say that %s files are skipped: %q", want, text)
		}
	}

	// The content was there all along: the miss is the default scope, which is
	// exactly what the message has to distinguish.
	parts, err = executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "needle",
		Path:    hidden,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := extractFirstText(parts); strings.HasPrefix(got, "No matches found") {
		t.Errorf("the hidden file does contain the pattern, but the search found nothing: %q", got)
	}
}

// Omitting path and naming "." must be one call. They used to reach ripgrep
// differently — no location argument at all versus "." — and ripgrep treats
// those as different situations, so the same tree could answer "no matches"
// through one and an error through the other. This pins the equality in both
// directions: the same result lines when files are searchable, and ripgrep's
// "./" prefix, which is what the default call pays for the uniformity.
//
// The comparison is on the lines as a set, not on the output as one string:
// ripgrep writes matches as its threads find them, so the order of files in a
// result is not a contract. Twelve runs of the same search over three files
// produced three different orders — a test comparing whole outputs verbatim is
// a coin flip, and it flaked in CI exactly that way.
func TestSearchContentDefaultPathMatchesExplicitDot(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	omitted, err := executeSearchContent(context.Background(), SearchContentInput{Pattern: "needle"})
	if err != nil {
		t.Fatalf("unexpected error without a path: %v", err)
	}
	explicit, err := executeSearchContent(context.Background(), SearchContentInput{Pattern: "needle", Path: "."})
	if err != nil {
		t.Fatalf("unexpected error with path %q: %v", ".", err)
	}

	got, want := extractFirstText(omitted), extractFirstText(explicit)
	if a, b := sortedLines(got), sortedLines(want); !slices.Equal(a, b) {
		t.Errorf("default path and \".\" disagree:\n no path: %q\n       .: %q", got, want)
	}
	for _, line := range []string{"./a.txt:1:needle", "./sub/b.txt:1:needle"} {
		if !slices.Contains(sortedLines(got), line) {
			t.Errorf("expected %q from the default call, got %q", line, got)
		}
	}
}

// sortedLines splits a result into its lines, sorted. Search results are
// ordered within a file and unordered between files, so anything that compares
// two results must compare these, not the streams.
func sortedLines(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	slices.Sort(lines)
	return lines
}

// The case the change exists for: a tree with nothing searchable in it. With no
// location argument ripgrep exits 2 and advises --debug, which this tool cannot
// pass; naming "." exits 1. The default now takes the second branch, so an empty
// or fully-filtered directory is reported as the no-match it is.
func TestSearchContentDefaultPathInEmptyTreeIsNoMatch(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	t.Chdir(dir)

	parts, err := executeSearchContent(context.Background(), SearchContentInput{Pattern: "needle"})
	if err != nil {
		t.Fatalf("a directory with nothing to search must answer no-match, not fail: %v", err)
	}
	text := extractFirstText(parts)
	if !strings.HasPrefix(text, "No matches found") {
		t.Errorf("expected a no-match answer, got %q", text)
	}
}

// searchScopeNote tells the model what ripgrep's defaults leave out, and that
// statement is true only while this argv stays free of the flags that would
// widen the scope. This is the coupling: a flag added without the note changes a
// helpful answer into a misleading one, silently, on every empty result.
func TestSearchContentArgsDoNotWidenTheDefaultScope(t *testing.T) {
	args := buildSearchContentArgs(SearchContentInput{Pattern: "p", Path: "."})

	// Each of these makes ripgrep search files the note says it skips.
	for _, widening := range []string{"--hidden", "--no-ignore", "--no-ignore-vcs", "--follow", "--binary", "-u", "-uu", "-uuu"} {
		if slices.Contains(args, widening) {
			t.Errorf("argv carries %q, which widens the scope — update searchScopeNote with what it adds", widening)
		}
	}

	// And the note itself must keep naming every category those flags would add,
	// or the reverse drift is possible: a note trimmed while the flags stay.
	for _, want := range []string{"hidden", "gitignored", "binary", "symlink"} {
		if !strings.Contains(searchScopeNote, want) {
			t.Errorf("searchScopeNote no longer names %q: %q", want, searchScopeNote)
		}
	}
}
