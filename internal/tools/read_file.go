package tools

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=The path of the file to read"`
	StartLine string `json:"start_line" jsonschema:"description=Optional: The starting line number (1-indexed)"`
	EndLine   string `json:"end_line" jsonschema:"description=Optional: The ending line number (1-indexed)"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		"Read the contents of a file. Supports optional line range using start_line and end_line parameters (1-indexed).",
	).
		WithSchema(llm.GenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

func executeReadFile(_ context.Context, args ReadFileInput) (llm.ToolResultOutput, error) {
	content, err := os.ReadFile(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	if args.StartLine == "" && args.EndLine == "" {
		return llm.NewTextResponse(string(content)), nil
	}

	startLine := 0
	if args.StartLine != "" {
		startLine, err = strconv.Atoi(args.StartLine)
		if err != nil {
			return llm.NewTextErrorResponse("invalid start_line: must be a number"), nil
		}
		if startLine < 1 {
			return llm.NewTextErrorResponse("start_line must be >= 1"), nil
		}
	}

	endLine := 0
	if args.EndLine != "" {
		endLine, err = strconv.Atoi(args.EndLine)
		if err != nil {
			return llm.NewTextErrorResponse("invalid end_line: must be a number"), nil
		}
		if endLine < 1 {
			return llm.NewTextErrorResponse("end_line must be >= 1"), nil
		}
	}

	if startLine > 0 && endLine > 0 && startLine > endLine {
		return llm.NewTextErrorResponse("start_line must be <= end_line"), nil
	}

	lines, err := readLinesRange(bytes.NewReader(content), startLine, endLine)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	return llm.NewTextResponse(strings.Join(lines, "\n")), nil
}

func readLinesRange(r *bytes.Reader, startLine, endLine int) ([]string, error) {
	scanner := bufio.NewScanner(r)
	var lines []string
	currentLine := 1

	for scanner.Scan() {
		if startLine > 0 && currentLine < startLine {
			currentLine++
			continue
		}

		if endLine > 0 && currentLine > endLine {
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
