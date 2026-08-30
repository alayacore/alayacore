package tools

import (
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
	MaxLines   int    `json:"max_lines" jsonschema_desc:"Max matching lines (0 = no limit)"`
}

// ripgrepBinary is the external executable search_content shells out to. It is
// a documented system requirement (see README); the name is a single constant
// so the "not installed" error and the exec call cannot drift.
const ripgrepBinary = "rg"

// maxSearchContentSize caps the number of bytes of search output returned
// inline, mirroring execute_command and read_file (64KB). A line-count cap
// alone is not enough: a single matching line (e.g. minified JS or base64
// data) can be arbitrarily large and would blow the context window.
const maxSearchContentSize = 64 * 1024 // 64KB

// NewSearchContentTool creates the search_content tool for use by the agent.
func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns. Results exceeding max_lines (0 = no limit) or 64KB are saved to a temp file; only a summary with the file path is returned — use read_file to access specific matches.`,
	).
		WithSchema(llm.MustGenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
		WithExecuteStreaming(llm.TypedExecuteStreaming(executeSearchContentStreaming)).
		Build()
}

// searchResult carries the exit status and output streams of a ripgrep
// search between runSearch and formatSearchResult, keeping execution and
// formatting independently testable. The streams are captures rather than
// strings so a search matching hundreds of megabytes costs memory only up to
// maxSearchContentSize, with the remainder already on disk.
type searchResult struct {
	stdout   *capture
	stderr   *capture
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
	cmd := exec.CommandContext(timeoutCtx, ripgrepBinary, rgArgs...)
	cmd.Dir = cwd
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	execErr := cmd.Run()

	if execErr != nil {
		if timeoutCtx.Err() != nil {
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				return 0, fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded)
			}
			return 0, fmt.Errorf("%w: %w", ErrCanceled, timeoutCtx.Err())
		}

		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}

		// Non-exit error (e.g. binary doesn't exist). ripgrep is an external
		// requirement documented in the README, and the raw exec error
		// ("exec: \"rg\": executable file not found in $PATH") tells the model
		// nothing actionable — say what is missing and what to use instead.
		if errors.Is(execErr, exec.ErrNotFound) {
			return 0, fmt.Errorf("search_content needs ripgrep (%s) on PATH, which was not found: %w. Install ripgrep, or search with execute_command (e.g. grep/find) instead", ripgrepBinary, execErr)
		}
		return 0, execErr
	}

	return 0, nil
}

// formatSearchResult converts a structured search result into ContentParts.
// It is separated from runSearch so that each concern (execution vs. formatting)
// can be tested and reasoned about independently.
func formatSearchResult(result searchResult, maxLines int) ([]llm.ContentPart, error) {
	// rg exits with code 1 when no matches found — that's not an error for us.
	if result.exitCode == 1 && result.stderr.size() == 0 {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	// Real error (bad regex, permission denied, etc.)
	if result.exitCode != 0 {
		errMsg := result.stderr.prefix()
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep exited with code %d", result.exitCode)
		}
		return nil, errors.New(errMsg)
	}

	// Success path
	if result.stdout.size() == 0 {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	totalLines := result.stdout.lineTotal()

	// If output exceeds maxLines or maxSearchContentSize, save full results
	// to file and return metadata. maxLines of 0 (the default when omitted)
	// means no line limit; the 64KB byte cap always applies as a
	// context-window safety net. A spilled capture is by definition over the
	// byte cap.
	if (maxLines > 0 && totalLines > int64(maxLines)) || result.stdout.spilled() ||
		result.stdout.size() > maxSearchContentSize {
		return handleLargeSearchResult(result.stdout, totalLines)
	}

	return []llm.ContentPart{&llm.TextPart{Text: result.stdout.String()}}, nil
}

// handleLargeSearchResult saves large search output to a temp file and
// returns a summary message with the file path. The capture is streamed to the
// file, so the full result set is preserved without ever being held in memory.
func handleLargeSearchResult(stdout *capture, totalLines int64) ([]llm.ContentPart, error) {
	f, err := createProcTmpFile("search-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large search results: %w", err)
	}
	path := f.Name()

	if werr := stdout.writeOut(f); werr != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("failed to save large search results: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(path)
		return nil, fmt.Errorf("failed to save large search results: %w", cerr)
	}

	text := fmt.Sprintf(
		"Search found %d matching lines (%s). Results saved to: %s\nUse read_file to access specific matches.",
		totalLines, describeSize(stdout.size()), path,
	)
	if stdout.truncated() {
		text += "\n[Warning: part of the output could not be written to disk and was dropped.]"
	}
	return []llm.ContentPart{&llm.TextPart{Text: text}}, nil
}

// executeSearchContent is the typed entry point for the search_content tool.
// It runs ripgrep and formats the results.
func executeSearchContent(ctx context.Context, args SearchContentInput) ([]llm.ContentPart, error) {
	stdout, stderr := newCapture(maxSearchContentSize), newCapture(maxSearchContentSize)
	defer stdout.Close()
	defer stderr.Close()

	exitCode, err := runSearch(ctx, args, stdout, stderr)
	if err != nil {
		return nil, err
	}
	return formatSearchResult(searchResult{
		stdout:   stdout,
		stderr:   stderr,
		exitCode: exitCode,
	}, args.MaxLines)
}

// executeSearchContentStreaming is the streaming variant of
// executeSearchContent: it emits ephemeral preview snapshots (Uf) of the
// ripgrep output while the search runs, then returns the authoritative
// result exactly as the non-streaming path does.
func executeSearchContentStreaming(ctx context.Context, args SearchContentInput, onDelta func(string)) ([]llm.ContentPart, error) {
	sw := newStreamingWriterWith(onDelta, maxSearchContentSize)
	defer sw.Close()
	exitCode, err := runSearch(ctx, args, sw, errWriter{sw})
	sw.flushPreview() // search finished — emit the final snapshot

	if err != nil {
		return nil, err
	}
	return formatSearchResult(searchResult{
		stdout:   sw.buf,
		stderr:   sw.errBuf,
		exitCode: exitCode,
	}, args.MaxLines)
}
