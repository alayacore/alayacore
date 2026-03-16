package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/fantasy"
)

// EditFileInput represents the input for the edit_file tool
type EditFileInput struct {
	Path      string `json:"path" description:"The path of the file to edit"`
	OldString string `json:"old_string" description:"The exact text to find and replace (must match exactly)"`
	NewString string `json:"new_string" description:"The replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
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
		func(_ context.Context, input EditFileInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if input.Path == "" {
				return fantasy.NewTextErrorResponse("path is required"), nil
			}
			if input.OldString == "" {
				return fantasy.NewTextErrorResponse("old_string is required"), nil
			}

			// Read original content
			originalContent, err := os.ReadFile(input.Path)
			if err != nil {
				if os.IsNotExist(err) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s", input.Path)), nil
				}
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			originalStr := string(originalContent)

			// Count occurrences
			count := strings.Count(originalStr, input.OldString)
			if count == 0 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", input.OldString)), nil
			}
			if count > 1 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("old_string found %d times in file. Include more surrounding context to make it unique, or use a different portion of text.", count)), nil
			}

			// Apply the replacement
			newContent := strings.Replace(originalStr, input.OldString, input.NewString, 1)

			// Write back
			if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse("File edited successfully"), nil
		},
	)
}
