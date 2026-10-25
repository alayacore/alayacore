package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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

// maxToolTextBytes caps the number of bytes of search output returned
// inline, mirroring execute_command and read_file (64KB). A line-count cap
// alone is not enough: a single matching line (e.g. minified JS or base64 data)
// can be arbitrarily large and would blow the context window, which is what
// maxToolTextBytes is for.
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
// maxToolTextBytes, with the remainder already on disk.
type searchResult struct {
	stdout   *capture
	stderr   *capture
	exitCode int
}

// buildSearchContentArgs builds the ripgrep invocation. args.Path is normalized
// by the entry points (searchPath), so it is always present: an omitted path is
// sent as "." rather than as no location argument, which ripgrep treats as a
// separate case.
//
// Nothing here widens the default scope, and noMatches tells the model what that
// scope leaves out — the two are one fact, so a flag that changes which files
// are searched (--hidden, --no-ignore, --follow, --binary) makes searchScopeNote
// wrong as well.
func buildSearchContentArgs(args SearchContentInput) []string {
	rgArgs := []string{
		"-n",
		"--no-heading",
		"--color=never",
		"-e", args.Pattern,
		args.Path,
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

// searchPath is the one place an omitted path is decided: it means the current
// directory, spelled out.
//
// Spelling it out is not cosmetic. With no location argument at all ripgrep
// takes a separate branch: a tree that leaves nothing to search — empty, or with
// every file filtered — exits 2 and advises --debug, a flag this tool cannot
// pass, where "." exits 1 and is the no-match it actually is. One spelling makes
// the default and the explicit call one call, so a result cannot depend on which
// of the two ways the same request was written.
func searchPath(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// searchDir returns the directory ripgrep runs in. A relative path argument —
// including the "." an omitted one becomes (searchPath) — resolves against the
// process directory, which is also what the system prompt states, so the tool
// and the model agree on the base without either of them writing it down twice.
//
// Best effort: a process directory that cannot be read is no reason to fail a
// search, and "." is what the shell would have used.
func searchDir() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// runSearch executes ripgrep in dir, writing stdout/stderr to the provided
// writers. It returns the rg exit code and a non-exit error (e.g. binary
// not found). rg exit codes signal status: 0 = matches found, 1 = no
// matches, 2+ = error (bad regex, permission denied, etc.).
func runSearch(ctx context.Context, args SearchContentInput, dir string, stdout, stderr io.Writer) (int, error) {
	if args.Pattern == "" {
		return 0, fmt.Errorf("pattern is required")
	}

	rgArgs := buildSearchContentArgs(args)

	// Search runs under the same global timeout as execute_command, so a
	// pathological scan (giant tree, slow filesystem) cannot hang forever
	// when a timeout is configured (0 = no limit).
	timeoutCtx := ctx
	if shell.DefaultCommandTimeout > 0 {
		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, shell.DefaultCommandTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(timeoutCtx, ripgrepBinary, rgArgs...)
	cmd.Dir = dir
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

// searchScopeNote is what ripgrep's defaults leave out of a search, in the
// model's words. It belongs with the flags that produce it (buildSearchContentArgs)
// and is repeated at the only moment it matters: an answer of "no matches",
// where the omission would otherwise read as "this exists nowhere".
const searchScopeNote = "ripgrep skips hidden, gitignored and binary files and symlinks, unless named as the path"

// noMatches is the result of a search that found nothing. It says what the
// search left out — the one part of this answer the caller cannot derive. Where
// it searched is derivable (the system prompt states the working directory, and
// the path argument is the caller's own), but ripgrep's default filters are
// stated nowhere, and a file holding the pattern that was never read answers
// "no matches" exactly like a pattern that does not exist.
func noMatches() []llm.ContentPart {
	return []llm.ContentPart{&llm.TextPart{Text: fmt.Sprintf("No matches found (%s)", searchScopeNote)}}
}

// formatSearchResult converts a structured search result into ContentParts.
// It is separated from runSearch so that each concern (execution vs. formatting)
// can be tested and reasoned about independently.
func formatSearchResult(result searchResult, maxLines int) ([]llm.ContentPart, error) {
	// rg exits with code 1 when no matches found — that's not an error for us.
	if result.exitCode == 1 && result.stderr.size() == 0 {
		return noMatches(), nil
	}

	// rg exits with code 2 when the run hit an error, and it keeps going after
	// one: an unreadable subdirectory beside readable ones leaves a permission
	// complaint on stderr and every match it could read on stdout. Those
	// matches are not in doubt, so the output is never discarded here — only a
	// run with nothing to show is an error, and a run with both reports itself
	// as incomplete. Dropping the output would answer "not found" about a
	// pattern that was found, which is the one answer the model cannot check.
	incomplete := result.exitCode != 0
	if incomplete && result.stdout.size() == 0 {
		errMsg := result.stderr.prefix()
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep exited with code %d", result.exitCode)
		}
		return nil, errors.New(errMsg)
	}

	// Success path
	if result.stdout.size() == 0 {
		return noMatches(), nil
	}

	// Said beside the results rather than as an error, because the results are
	// real and the model needs both halves: what was found, and that the tree
	// it was found in was not read in full.
	note := ""
	if incomplete {
		reason := stderrSummary(result.stderr)
		if reason == "" {
			// rg can fail without a word on stderr (a signal, say). Say that
			// much rather than leaving the warning dangling mid-sentence.
			reason = fmt.Sprintf("exit code %d", result.exitCode)
		}
		note = fmt.Sprintf("\n[Warning: the search was incomplete — ripgrep reported %s]", reason)
	}

	totalLines := result.stdout.lineTotal()

	// If output exceeds maxLines or maxToolTextBytes, save full results
	// to file and return metadata. maxLines of 0 (the default when omitted)
	// means no line limit; the 64KB byte cap always applies as a
	// context-window safety net. A spilled capture is by definition over the
	// byte cap.
	if (maxLines > 0 && totalLines > int64(maxLines)) || result.stdout.spilled() ||
		result.stdout.size() > maxToolTextBytes {
		return handleLargeSearchResult(result.stdout, totalLines, note)
	}

	return []llm.ContentPart{&llm.TextPart{Text: result.stdout.String() + note}}, nil
}

// stderrSummary reduces ripgrep's stderr to one line for a note that sits beside
// results. The capture holds up to 64KB of it, and a stack of permission-denied
// lines repeated in full would cost more than the results it warns about.
func stderrSummary(c *capture) string {
	first, rest, more := strings.Cut(strings.TrimSpace(c.prefix()), "\n")
	if len(first) > maxStderrSummaryBytes {
		first = cutToBudget(first, maxStderrSummaryBytes) + "…"
	}
	if !more || rest == "" {
		return first
	}
	return first + " (and further errors)"
}

// maxStderrSummaryBytes bounds one line of ripgrep's stderr in a warning note.
const maxStderrSummaryBytes = 200

// handleLargeSearchResult saves large search output to a temp file and
// returns a summary message with the file path. The capture is streamed to the
// file, so the full result set is preserved without ever being held in memory.
// note carries the incomplete-search warning, if any: the message is what the
// model reads before the file, so it is the half that has to say the search was
// partial even when every match is in the file.
func handleLargeSearchResult(stdout *capture, totalLines int64, note string) ([]llm.ContentPart, error) {
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
	// The warning goes into the file as well as the message. The file is what a
	// model reads back through read_file — now or after the summary has scrolled
	// out of its context — and a results list that omits why it is short is the
	// same false negative one layer down. execute_command writes its header into
	// the saved file for exactly this reason. A failed append costs nothing: the
	// message carries the same words, so it is not worth failing a search whose
	// results are already on disk.
	if note != "" {
		_, _ = f.WriteString(note + "\n")
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
	return []llm.ContentPart{&llm.TextPart{Text: text + note}}, nil
}

// executeSearchContent is the typed entry point for the search_content tool.
// It runs ripgrep and formats the results.
func executeSearchContent(ctx context.Context, args SearchContentInput) ([]llm.ContentPart, error) {
	args.Path = searchPath(args.Path)

	stdout, stderr := newCapture(maxToolTextBytes), newCapture(maxToolTextBytes)
	defer stdout.Close()
	defer stderr.Close()

	exitCode, err := runSearch(ctx, args, searchDir(), stdout, stderr)
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
	args.Path = searchPath(args.Path)

	sw := newStreamingWriterWith(onDelta, maxToolTextBytes)
	defer sw.Close()
	exitCode, err := runSearch(ctx, args, searchDir(), sw, errWriter{sw})
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
