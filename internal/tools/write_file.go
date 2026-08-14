package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/llm"
)

// WriteFileInput represents the input for the write_file tool
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_desc:"File path to write"`
	Content string `json:"content" jsonschema:"required" jsonschema_desc:"Content to write to the file"`
}

func NewWriteFileTool() llm.Tool {
	return llm.NewTool(
		"write_file",
		"Create a new file or replace the entire content of an existing file. For surgical edits to existing files, prefer edit_file instead.",
	).
		WithSchema(llm.MustGenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

func executeWriteFile(_ context.Context, args WriteFileInput) ([]llm.ContentPart, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if args.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Preserve an existing file's permission bits: overwriting a file must
	// not clobber its mode (an executable script would lose its +x). New
	// files default to 0644, narrowed by the process umask as usual — a
	// fixed 0600 made every new file unreadable to other users and never
	// executable.
	perm := os.FileMode(0644)
	if info, err := os.Stat(args.Path); err == nil {
		perm = info.Mode().Perm()
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), perm); err != nil {
		return nil, err
	}
	return []llm.ContentPart{&llm.TextPart{Text: "File written successfully"}}, nil
}
