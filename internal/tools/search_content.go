package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

// SearchContentInput represents the input for the search_content tool.
type SearchContentInput struct {
	Pattern    string `json:"pattern" jsonschema:"required" jsonschema_desc:"Regex pattern to search for"`
	Path       string `json:"path" jsonschema_desc:"File or directory to search (default: cwd)"`
	FileType   string `json:"file_type" jsonschema_desc:"File type filter (e.g. go, python, rust)"`
	Glob       string `json:"glob" jsonschema_desc:"Glob pattern (e.g. *.go, *.{ts,tsx})"`
	IgnoreCase bool   `json:"ignore_case" jsonschema_desc:"Enable case-insensitive search"`
	MaxLines   *int   `json:"max_lines" jsonschema_desc:"Max matching lines (0 = no line limit; default 100)"`
}

const defaultSearchContentMaxLines = 100

// maxSearchContentSize caps the number of bytes of search output returned
// inline, mirroring execute_command and read_file (64KB). A line-count cap
// alone is not enough: a single matching line (e.g. minified JS or base64
// data) can be arbitrarily large and would blow the context window.
const maxSearchContentSize = 64 * 1024 // 64KB

// NewSearchContentTool creates the search_content tool for use by the agent.
func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns. Results exceeding max_lines (default 100) or 64KB are saved to a temp file; only a summary with the file path is returned — use read_file to access specific matches.`,
	).
		WithSchema(llm.MustGenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
		WithExecuteStreaming(llm.TypedExecuteStreaming(executeSearchContentStreaming)).
		Build()
}

// searchResult carries the exit status and output streams of a ripgrep
// search between runSearch and formatSearchResult, keeping execution and
// formatting independently testable.
type searchResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func buildSearchContentArgs(args SearchContentInput) []string {
	rgArgs := []string{
		"-n",
		"--no-heading",
		"--color=never",
		"-e", args.Pattern,
	}

	if args.Path != "" {
		rgArgs = append(rgArgs, args.Path)
	}

	if args.FileType != "" {
		rgArgs = append(rgArgs, "--type", args.FileType)
	}

	if args.IgnoreCase {
		rgArgs = append(rgArgs, "-i")
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}

	return rgArgs
}

// runSearch executes ripgrep, writing stdout/stderr to the provided
// writers. It returns the rg exit code and a non-exit error (e.g. binary
// not found). rg exit codes signal status: 0 = matches found, 1 = no
// matches, 2+ = error (bad regex, permission denied, etc.).
func runSearch(ctx context.Context, args SearchContentInput, stdout, stderr io.Writer) (int, error) {
	if args.Pattern == "" {
		return 0, fmt.Errorf("pattern is required")
	}

	rgArgs := buildSearchContentArgs(args)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// Search runs under the same global timeout as execute_command, so a
	// pathological scan (giant tree, slow filesystem) cannot hang forever
	// when a timeout is configured (0 = no limit).
	timeoutCtx := ctx
	if shell.DefaultCommandTimeout > 0 {
		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, shell.DefaultCommandTimeout)
		defer cancel()
	}

	//nolint:gosec // G204: args are from user input, rg is a trusted binary
	cmd := exec.CommandContext(timeoutCtx, "rg", rgArgs...)
	cmd.Dir = cwd
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	execErr := cmd.Run()

	if execErr != nil {
		if timeoutCtx.Err() != nil {
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				return 0, fmt.Errorf("timed out")
			}
			return 0, fmt.Errorf("canceled")
		}

		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}

		// Non-exit error (e.g. binary doesn't exist).
		return 0, execErr
	}

	return 0, nil
}

// formatSearchResult converts a structured search result into ContentParts.
// It is separated from runSearch so that each concern (execution vs. formatting)
// can be tested and reasoned about independently.
func formatSearchResult(result searchResult, maxLines *int) ([]llm.ContentPart, error) {
	// rg exits with code 1 when no matches found — that's not an error for us.
	if result.exitCode == 1 && result.stderr == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	// Real error (bad regex, permission denied, etc.)
	if result.exitCode != 0 {
		errMsg := result.stderr
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep exited with code %d", result.exitCode)
		}
		return nil, errors.New(errMsg)
	}

	// Success path
	output := result.stdout
	if output == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	// nil = not specified → use default; 0 = explicit "no line limit"
	// (the 64KB byte cap still applies and saves oversized results to a file).
	limit := defaultSearchContentMaxLines
	if maxLines != nil {
		limit = *maxLines
	}

	// Count total lines in output
	totalLines := countLines(output)

	// If output exceeds the line limit or maxSearchContentSize, save full
	// results to file and return metadata. A line limit of 0 disables the
	// line check; the byte cap remains as a context-window safety net.
	if (limit > 0 && totalLines > limit) || len(output) > maxSearchContentSize {
		return handleLargeSearchResult(output, totalLines)
	}

	return []llm.ContentPart{&llm.TextPart{Text: output}}, nil
}

// handleLargeSearchResult saves large search output to a temp file and
// returns a summary message with the file path.
func handleLargeSearchResult(output string, totalLines int) ([]llm.ContentPart, error) {
	filePath, err := saveToTmpFile(output, "search-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large search results: %w", err)
	}

	totalKB := float64(len(output)) / 1024
	return []llm.ContentPart{&llm.TextPart{Text: fmt.Sprintf(
		"Search found %d matching lines (%.1fKB). Results saved to: %s\nUse read_file to access specific matches.",
		totalLines, totalKB, filePath,
	)}}, nil
}

// executeSearchContent is the typed entry point for the search_content tool.
// It runs ripgrep and formats the results.
func executeSearchContent(ctx context.Context, args SearchContentInput) ([]llm.ContentPart, error) {
	var stdout, stderr bytes.Buffer
	exitCode, err := runSearch(ctx, args, &stdout, &stderr)
	if err != nil {
		return nil, err
	}
	return formatSearchResult(searchResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: exitCode,
	}, args.MaxLines)
}

// executeSearchContentStreaming is the streaming variant of
// executeSearchContent: it emits ephemeral preview snapshots (Uf) of the
// ripgrep output while the search runs, then returns the authoritative
// result exactly as the non-streaming path does.
func executeSearchContentStreaming(ctx context.Context, args SearchContentInput, onDelta func(string)) ([]llm.ContentPart, error) {
	sw := newStreamingWriter(onDelta)
	exitCode, err := runSearch(ctx, args, sw, errWriter{sw})
	sw.flushPreview() // search finished — emit the final snapshot

	if err != nil {
		return nil, err
	}
	return formatSearchResult(searchResult{
		stdout:   sw.buf.String(),
		stderr:   sw.errBuf.String(),
		exitCode: exitCode,
	}, args.MaxLines)
}
