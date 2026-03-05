package tools

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"

	"charm.land/fantasy"
)

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" description:"The path of the file to read"`
	StartLine string `json:"start_line" description:"Optional: The starting line number (1-indexed)"`
	EndLine   string `json:"end_line" description:"Optional: The ending line number (1-indexed)"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"read_file",
		"Read the contents of a file. Supports optional line range using start_line and end_line parameters (1-indexed).",
		func(ctx context.Context, input ReadFileInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if input.Path == "" {
				return fantasy.NewTextErrorResponse("path is required"), nil
			}

			content, err := os.ReadFile(input.Path)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			if input.StartLine == "" && input.EndLine == "" {
				return fantasy.NewTextResponse(string(content)), nil
			}

			startLine := 0
			if input.StartLine != "" {
				var err error
				startLine, err = strconv.Atoi(input.StartLine)
				if err != nil {
					return fantasy.NewTextErrorResponse("invalid start_line: must be a number"), nil
				}
				if startLine < 1 {
					return fantasy.NewTextErrorResponse("start_line must be >= 1"), nil
				}
			}

			endLine := 0
			if input.EndLine != "" {
				var err error
				endLine, err = strconv.Atoi(input.EndLine)
				if err != nil {
					return fantasy.NewTextErrorResponse("invalid end_line: must be a number"), nil
				}
				if endLine < 1 {
					return fantasy.NewTextErrorResponse("end_line must be >= 1"), nil
				}
			}

			if startLine > 0 && endLine > 0 && startLine > endLine {
				return fantasy.NewTextErrorResponse("start_line must be <= end_line"), nil
			}

			lines, err := readLinesRange(bytes.NewReader(content), startLine, endLine)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse(strings.Join(lines, "\n")), nil
		},
	)
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
