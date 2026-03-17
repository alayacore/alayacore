package tools

import (
	"context"
	"os"

	"github.com/alayacore/alayacore/internal/llm"
)

// WriteFileInput represents the input for the write_file tool
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"required,description=The path of the file to write"`
	Content string `json:"content" jsonschema:"required,description=The content to write to the file"`
}

// NewWriteFileTool creates a tool for writing files
func NewWriteFileTool() llm.Tool {
	return llm.NewTool(
		"write_file",
		"Create a new file or replace the entire content of an existing file.",
	).
		WithSchema(llm.GenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

func executeWriteFile(_ context.Context, args WriteFileInput) (llm.ToolResultOutput, error) {
	if args.Path == "" {
		return llm.NewTextErrorResponse("path is required"), nil
	}
	if args.Content == "" {
		return llm.NewTextErrorResponse("content is required"), nil
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	return llm.NewTextResponse("File written successfully"), nil
}
