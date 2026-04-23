package tools

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"

	"github.com/alayacore/alayacore/internal/llm"
)

// SearchContentInput represents the input for the search_content tool
type SearchContentInput struct {
	Pattern    string `json:"pattern" jsonschema:"required,description=Regex pattern to search for"`
	Path       string `json:"path" jsonschema:"description=File or directory to search (default: cwd)"`
	FileType   string `json:"file_type" jsonschema:"description=File type filter (e.g. go, python, rust)"`
	Glob       string `json:"glob" jsonschema:"description=Glob pattern (e.g. *.go, *.{ts,tsx})"`
	IgnoreCase string `json:"ignore_case" jsonschema:"description=Set \"true\" for case-insensitive"`
	MaxLines   string `json:"max_lines" jsonschema:"description=Max matching lines (default 50)"`
}

const defaultSearchContentMaxLines = "50"

// RGAvailable checks if the rg binary is available on the system
func RGAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// NewSearchContentTool creates a tool for searching file contents using ripgrep (rg)
func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns.`,
	).
		WithSchema(llm.GenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
		Build()
}

func buildSearchContentArgs(args SearchContentInput, maxLines string) []string {
	rgArgs := []string{
		"-n",
		"--no-heading",
		"--color=never",
		"--max-count", maxLines,
		args.Pattern,
	}

	if args.Path != "" {
		rgArgs = append(rgArgs, args.Path)
	}

	if args.FileType != "" {
		rgArgs = append(rgArgs, "--type", args.FileType)
	}

	if args.IgnoreCase == "true" {
		rgArgs = append(rgArgs, "-i")
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}

	return rgArgs
}

func handleSearchContentResult(execErr error, stdout, stderr *bytes.Buffer, maxLines string) llm.ToolResultOutput {
	if execErr != nil {
		// rg exits with code 1 when no matches found — that's not an error for us
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 && stderr.Len() == 0 {
				return llm.NewTextResponse("No matches found")
			}
		}
		// Real error (bad regex, permission denied, etc.)
		errMsg := execErr.Error()
		if stderr.Len() > 0 {
			errMsg = stderr.String()
		}
		return llm.NewTextErrorResponse(errMsg)
	}

	// Truncate output to maxLines total matching lines.
	// rg's --max-count limits per-file, not globally, so we enforce a global cap here.
	maxLinesInt := parseMaxLines(maxLines)

	output := stdout.String()
	if output == "" {
		return llm.NewTextResponse("No matches found")
	}

	truncated, truncatedOutput := truncateLines(output, maxLinesInt)
	if truncated {
		truncatedOutput += "\n... (output truncated)"
	}

	return llm.NewTextResponse(truncatedOutput)
}

func executeSearchContent(ctx context.Context, args SearchContentInput) (llm.ToolResultOutput, error) {
	if args.Pattern == "" {
		return llm.NewTextErrorResponse("pattern is required"), nil
	}

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return llm.NewTextErrorResponse("ripgrep (rg) is not available on this system"), nil
	}

	maxLines := args.MaxLines
	if maxLines == "" {
		maxLines = defaultSearchContentMaxLines
	}

	rgArgs := buildSearchContentArgs(args, maxLines)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	//nolint:gosec // G204: rg path is validated, args are from user input which is intentional
	cmd := exec.Command(rgPath, rgArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Wait for command to complete, handling cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Parent context was canceled — kill the rg process
		killSearchContentProcess(cmd, done)
		return llm.NewTextErrorResponse("Canceled"), nil
	case execErr := <-done:
		return handleSearchContentResult(execErr, &stdout, &stderr, maxLines), nil
	}
}

func killSearchContentProcess(cmd *exec.Cmd, done chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill() //nolint:errcheck // best-effort kill on cancel path
		<-done                 // wait for Process to release resources
	}
}

// parseMaxLines converts the maxLines string to an int, falling back to the default on failure.
func parseMaxLines(maxLines string) int {
	n, err := strconv.Atoi(maxLines)
	if err != nil || n <= 0 {
		n = 50 // fallback to hardcoded default
	}
	return n
}

// truncateLines returns the first maxLines non-empty lines from input.
// Returns (wasTruncated, result).
func truncateLines(input string, maxLines int) (bool, string) {
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
