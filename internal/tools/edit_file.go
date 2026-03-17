package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// EditFileInput represents the input for the edit_file tool
type EditFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=The path of the file to edit"`
	OldString string `json:"old_string" jsonschema:"required,description=The exact text to find and replace (must match exactly)"`
	NewString string `json:"new_string" jsonschema:"required,description=The replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() llm.Tool {
	return llm.NewTool(
		"edit_file",
		`Apply a search/replace edit to a file.

CRITICAL: Read the file first to get the exact text including whitespace.

Parameters:
- path: The file path to edit
- old_string: The exact text to find (must match exactly including all whitespace, indentation, newlines)
- new_string: The replacement text

Requirements:
- old_string must match EXACTLY (every space, tab, newline, character)
- Include 3-5 lines of context to make old_string unique
- If old_string appears multiple times, the edit fails
- To replace multiple occurrences, make separate calls with unique context

Example:
{
  "path": "test.go",
  "old_string": "func old() {\n    doSomething()\n}",
  "new_string": "func new() {\n    doSomethingElse()\n}"
}`,
	).
		WithSchema(llm.GenerateSchema(EditFileInput{})).
		WithExecute(llm.TypedExecute(executeEditFile)).
		Build()
}

func executeEditFile(_ context.Context, args EditFileInput) (llm.ToolResultOutput, error) {
	if args.Path == "" {
		return llm.NewTextErrorResponse("path is required"), nil
	}
	if args.OldString == "" {
		return llm.NewTextErrorResponse("old_string is required"), nil
	}
	// Note: new_string can be empty (for removing content)

	// Read original content
	originalContent, err := os.ReadFile(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return llm.NewTextErrorResponse(fmt.Sprintf("file not found: %s", args.Path)), nil
		}
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	originalStr := string(originalContent)

	// Count occurrences
	count := strings.Count(originalStr, args.OldString)
	if count == 0 {
		return llm.NewTextErrorResponse(fmt.Sprintf("old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", args.OldString)), nil
	}
	if count > 1 {
		return llm.NewTextErrorResponse(fmt.Sprintf("old_string found %d times in file. Include more surrounding context to make it unique, or use a different portion of text.", count)), nil
	}

	// Apply the replacement
	newContent := strings.Replace(originalStr, args.OldString, args.NewString, 1)

	// Write back
	if err := os.WriteFile(args.Path, []byte(newContent), 0644); err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	return llm.NewTextResponse("File edited successfully"), nil
}
