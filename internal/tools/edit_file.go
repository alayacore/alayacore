package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alayacore/alayacore/internal/llm"
)

// EditFileInput represents the input for the edit_file tool
type EditFileInput struct {
	Path      string `json:"path" jsonschema:"required" jsonschema_desc:"File path to edit"`
	OldString string `json:"old_string" jsonschema:"required" jsonschema_desc:"Exact text to find and replace"`
	NewString string `json:"new_string" jsonschema:"required" jsonschema_desc:"Replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() llm.Tool {
	return llm.NewTool(
		"edit_file",
		`Apply an exact string replacement to a file (not regex). old_string must match EXACTLY (every space, tab, newline). If old_string appears multiple times, the edit fails.`,
	).
		WithSchema(llm.MustGenerateSchema(EditFileInput{})).
		WithExecute(llm.TypedExecute(executeEditFile)).
		Build()
}

// ============================================================================
// editSession — atomic file edit lifecycle
// ============================================================================

// editSession manages the lifecycle of an atomic search-and-replace edit.
// It owns the source file handle and the temporary file, ensuring cleanup
// via a single Close() call regardless of success or failure.
//
// Usage:
//
//	session, err := newEditSession(path)
//	if err != nil { ... }
//	defer session.Close()
//
//	editor := newStreamEditor(oldStr, newStr)
//	for { _, err := editor.processChunk(session, session); err != nil { ... } }
//	if err = editor.flushRemaining(session); err != nil { ... }
//
//	return session.Commit()  // renames temp → source; Close() skips removal
type editSession struct {
	srcPath   string
	srcFile   *os.File
	tempFile  *os.File
	tempPath  string
	fileInfo  os.FileInfo
	committed bool // set by Commit() to preserve temp file through Close()
}

// newEditSession opens the source file and creates a temp file in the same
// directory (to avoid cross-device rename errors).
func newEditSession(path string) (*editSession, error) {
	srcFile, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, err
	}

	fileInfo, err := srcFile.Stat()
	if err != nil {
		srcFile.Close()
		return nil, fmt.Errorf("failed to get file info: %v", err)
	}

	dir := filepath.Dir(path)
	tempFile, err := os.CreateTemp(dir, "edit_file_*.tmp")
	if err != nil {
		srcFile.Close()
		return nil, fmt.Errorf("failed to create temp file: %v", err)
	}

	return &editSession{
		srcPath:  path,
		srcFile:  srcFile,
		tempFile: tempFile,
		tempPath: tempFile.Name(),
		fileInfo: fileInfo,
	}, nil
}

// Close releases all resources. If the edit was not committed (via
// Commit), the temporary file is removed. Idempotent — safe to call
// multiple times.
func (s *editSession) Close() {
	if s.srcFile != nil {
		s.srcFile.Close()
		s.srcFile = nil
	}
	if s.tempFile != nil {
		s.tempFile.Close()
		s.tempFile = nil
	}
	if !s.committed && s.tempPath != "" {
		os.Remove(s.tempPath)
		s.tempPath = ""
	}
}

func (s *editSession) Read(p []byte) (int, error) {
	return s.srcFile.Read(p)
}

func (s *editSession) Write(p []byte) (int, error) {
	if s.tempFile == nil {
		return 0, fmt.Errorf("temp file already closed")
	}
	return s.tempFile.Write(p)
}

// Commit finalizes the edit by atomically renaming the temp file over the
// source. After a successful Commit, Close() will not remove the temp file
// (it no longer exists at its original path).
func (s *editSession) Commit() error {
	// Close both files before rename to release all OS handles.
	if err := s.tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %v", err)
	}
	s.tempFile = nil

	if err := s.srcFile.Close(); err != nil {
		return fmt.Errorf("failed to close source file: %v", err)
	}
	s.srcFile = nil

	// On Windows, os.Rename fails with "Access is denied" if the target
	// file still has an open handle — all handles are closed above.
	if err := os.Rename(s.tempPath, s.srcPath); err != nil {
		return fmt.Errorf("failed to replace file: %v", err)
	}
	s.committed = true

	if err := os.Chmod(s.srcPath, s.fileInfo.Mode()); err != nil {
		return fmt.Errorf("failed to restore file permissions: %v", err)
	}

	return nil
}

// ============================================================================
// streamEditor — streaming search and replace
// ============================================================================

// streamEditor handles streaming search and replace on a file.
// It reads from an io.Reader and writes to an io.Writer, making it
// testable without real files.
type streamEditor struct {
	oldBytes    []byte
	newBytes    []byte
	buffer      []byte
	chunk       []byte
	occurrences int
}

// maxEditBufferCapacity caps the stream editor's initial buffer
// pre-allocation. The capacity is only a hint — append grows the buffer as
// needed, so correctness is unaffected by the cap — but without it a huge
// LLM-supplied old_string would allocate 2× its length up front (a
// proportional allocation, a minor DoS surface).
const maxEditBufferCapacity = 1 << 20 // 1MB

func newStreamEditor(oldString, newString string) *streamEditor {
	const chunkSize = 4096
	oldBytes := []byte(oldString)
	capacity := len(oldBytes)*2 + chunkSize
	if capacity > maxEditBufferCapacity {
		capacity = maxEditBufferCapacity
	}
	return &streamEditor{
		oldBytes: oldBytes,
		newBytes: []byte(newString),
		buffer:   make([]byte, 0, capacity),
		chunk:    make([]byte, chunkSize),
	}
}

// processChunk reads and processes a chunk, writing to dst.
// Returns (done, error). done is true when the source is fully consumed.
func (se *streamEditor) processChunk(src io.Reader, dst io.Writer) (bool, error) {
	n, err := src.Read(se.chunk)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("failed to read file: %w", err)
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

		if _, err = dst.Write(se.buffer[:idx]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		if _, err = dst.Write(se.newBytes); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[idx+len(se.oldBytes):]
	}

	// Keep enough data in buffer to handle matches spanning chunks
	if len(se.buffer) > len(se.oldBytes) {
		writeLen := len(se.buffer) - len(se.oldBytes)
		if _, err = dst.Write(se.buffer[:writeLen]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[writeLen:]
	}

	return errors.Is(err, io.EOF), nil
}

// flushRemaining writes any remaining buffer content.
func (se *streamEditor) flushRemaining(dst io.Writer) error {
	if len(se.buffer) > 0 {
		if _, err := dst.Write(se.buffer); err != nil {
			return fmt.Errorf("failed to write to temp file: %v", err)
		}
	}
	return nil
}

// hasOccurrences returns true if the old_string was found (exactly once).
func (se *streamEditor) hasOccurrences() bool {
	return se.occurrences > 0
}

// ============================================================================
// executeEditFile — tool entry point
// ============================================================================

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

func executeEditFile(ctx context.Context, args EditFileInput) ([]llm.ContentPart, error) {
	path, err := validateEditFileInput(args)
	if err != nil {
		return nil, err
	}

	session, err := newEditSession(path)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	editor := newStreamEditor(args.OldString, args.NewString)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Canceled")
		default:
		}

		done, pErr := editor.processChunk(session, session)
		if pErr != nil {
			return nil, pErr
		}
		if done {
			break
		}
	}

	if err = editor.flushRemaining(session); err != nil {
		return nil, err
	}

	if !editor.hasOccurrences() {
		return nil, fmt.Errorf(
			"old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", args.OldString)
	}

	if err := session.Commit(); err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: "File edited successfully"}}, nil
}
