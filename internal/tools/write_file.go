package tools

import (
	"context"
	"os"

	"charm.land/fantasy"
)

// WriteFileInput represents the input for the write_file tool
type WriteFileInput struct {
	Path    string `json:"path" description:"The path of the file to write"`
	Content string `json:"content" description:"The content to write to the file"`
}

// NewWriteFileTool creates a tool for writing files
func NewWriteFileTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"write_file",
		"Create a new file or replace the entire content of an existing file.",
		func(_ context.Context, input WriteFileInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if input.Path == "" {
				return fantasy.NewTextErrorResponse("path is required"), nil
			}

			if err := os.WriteFile(input.Path, []byte(input.Content), 0644); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse("File written successfully"), nil
		},
	)
}
