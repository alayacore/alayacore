package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"

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
		`Apply a search/replace edit to a file. old_string must match EXACTLY (every space, tab, newline). Use 3-5 lines of context to make it unique. If old_string appears multiple times, the edit fails.`,
	).
		WithSchema(llm.GenerateSchema(EditFileInput{})).
		WithExecute(llm.TypedExecute(executeEditFile)).
		Build()
}

// streamEditor handles streaming search and replace on a file
type streamEditor struct {
	oldBytes    []byte
	newBytes    []byte
	buffer      []byte
	chunk       []byte
	occurrences int
}

func newStreamEditor(oldString, newString string) *streamEditor {
	const chunkSize = 4096
	oldBytes := []byte(oldString)
	return &streamEditor{
		oldBytes: oldBytes,
		newBytes: []byte(newString),
		buffer:   make([]byte, 0, len(oldBytes)*2+chunkSize),
		chunk:    make([]byte, chunkSize),
	}
}

// processChunk reads and processes a chunk, writing to tempFile
// Returns (done, error)
func (se *streamEditor) processChunk(file *os.File, tempFile *os.File) (bool, error) {
	n, err := file.Read(se.chunk)
	if err != nil && err.Error() != "EOF" {
		return false, fmt.Errorf("failed to read file: %v", err)
	}

	se.buffer = append(se.buffer, se.chunk[:n]...)

	// Search for old_string in buffer
	for {
		idx := bytes.Index(se.buffer, se.oldBytes)
		if idx == -1 {
			break
		}

		se.occurrences++
		if se.occurrences > 1 {
			return false, fmt.Errorf("old_string found multiple times in file. Include more surrounding context to make it unique, or use a different portion of text")
		}

		if _, err = tempFile.Write(se.buffer[:idx]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		if _, err = tempFile.Write(se.newBytes); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[idx+len(se.oldBytes):]
	}

	// Keep enough data in buffer to handle matches spanning chunks
	if len(se.buffer) > len(se.oldBytes) {
		writeLen := len(se.buffer) - len(se.oldBytes)
		if _, err = tempFile.Write(se.buffer[:writeLen]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[writeLen:]
	}

	return err != nil && err.Error() == "EOF", nil
}

// flushRemaining writes any remaining buffer content
func (se *streamEditor) flushRemaining(tempFile *os.File) error {
	if len(se.buffer) > 0 {
		if _, err := tempFile.Write(se.buffer); err != nil {
			return fmt.Errorf("failed to write to temp file: %v", err)
		}
	}
	return nil
}

func validateEditFileInput(args EditFileInput) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if args.OldString == args.NewString {
		return "", fmt.Errorf("old_string and new_string are identical — no changes would be made. If you intended to modify the file, make sure new_string is different from old_string")
	}
	return args.Path, nil
}

func openFileForEdit(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, err
	}
	return file, nil
}

func executeEditFile(ctx context.Context, args EditFileInput) (llm.ToolResultOutput, error) {
	path, err := validateEditFileInput(args)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	file, err := openFileForEdit(path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	defer file.Close()

	tempFile, err := os.CreateTemp("", "edit_file_*.tmp")
	if err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to create temp file: %v", err)), nil
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	editor := newStreamEditor(args.OldString, args.NewString)

	for {
		select {
		case <-ctx.Done():
			tempFile.Close()
			return llm.NewTextErrorResponse("Canceled"), nil
		default:
		}

		var done bool
		done, err = editor.processChunk(file, tempFile)
		if err != nil {
			tempFile.Close()
			return llm.NewTextErrorResponse(err.Error()), nil
		}
		if done {
			break
		}
	}

	if err = editor.flushRemaining(tempFile); err != nil {
		tempFile.Close()
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	if err = tempFile.Close(); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to close temp file: %v", err)), nil
	}

	if editor.occurrences == 0 {
		return llm.NewTextErrorResponse(
			fmt.Sprintf("old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", args.OldString)), nil
	}

	fileInfo, err := os.Stat(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to get file info: %v", err)), nil
	}

	if err = os.Rename(tempPath, args.Path); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to replace file: %v", err)), nil
	}

	if err = os.Chmod(args.Path, fileInfo.Mode()); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to restore file permissions: %v", err)), nil
	}

	return llm.NewTextResponse("File edited successfully"), nil
}
