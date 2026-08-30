package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
		"Create a new file or replace the entire content of an existing file. Writing an empty content truncates the file to zero length. For surgical edits to existing files, prefer edit_file instead.",
	).
		WithSchema(llm.MustGenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

// executeWriteFile replaces a file's content.
//
// The normal path is atomic: the new content goes to a sibling temp file and is
// renamed over the target, so a failed or interrupted write can never leave a
// half-written file where a complete one used to be. os.WriteFile truncated the
// target up front, which meant a crash or a full disk destroyed the original —
// the very risk edit_file guards against.
//
// Atomicity is a property of the directory, not of the file: rename needs write
// permission on the directory, while opening a file for writing does not. So a
// writable file inside a read-only directory falls back to the in-place write,
// keeping every target the previous implementation could update updatable.
func executeWriteFile(_ context.Context, args WriteFileInput) ([]llm.ContentPart, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Write through a symlink to the file it points at (see resolveWriteTarget).
	target := resolveWriteTarget(args.Path)

	// Permission semantics mirror os.WriteFile: an existing file keeps its
	// exact mode (overwriting must not clobber it — an executable script would
	// lose its +x), while a new file is created 0644 narrowed by the umask.
	// The narrowing matters: chmod bypasses the umask, so applying a literal
	// 0644 would silently WIDEN permissions for anyone running under a
	// restrictive umask, and this tool writes files that may hold secrets.
	//
	// `existing` is remembered rather than re-Stat'd below: a second Stat
	// would only look like it validated the target, while the file could
	// change between the two calls. One read, one decision.
	var existing bool
	perm := newFilePerm(currentUmask())
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("cannot write to %s: it is a directory", args.Path)
		}
		existing = info.Mode().IsRegular()
		perm = info.Mode().Perm()
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}

	// Create the temp file beside the target: renaming across filesystems
	// fails, and a sibling is the only location guaranteed to be on the same
	// device as the destination.
	tmp, err := os.CreateTemp(dir, tempFilePattern(base, "write"))
	if err != nil {
		// A rename needs write permission on the *directory*; os.WriteFile
		// never did, it only needed the file itself to be writable. Without
		// this fallback, a writable file sitting in a read-only directory —
		// a normal layout for vendored trees and service configs — would turn
		// a write that used to work into a hard failure. Degraded, not denied.
		if existing {
			return writeInPlace(target, args.Content)
		}
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if err := writeAndClose(tmp, args.Content, perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return nil, err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to replace file: %w", err)
	}

	return []llm.ContentPart{&llm.TextPart{Text: "File written successfully"}}, nil
}

// writeInPlace replaces an existing file's content without a rename. It is the
// fallback for targets whose directory cannot hold a temp file, and keeps the
// pre-existing behavior of write_file (which is what those users relied on).
//
// Weaker guarantee — a crash mid-write can leave a truncated file — but only
// reachable when the atomic path is impossible on POSIX, so the alternative is
// refusing the write, not a safer one. The inode is reused, so the file keeps
// its own mode and ownership without any chmod.
func writeInPlace(target, content string) ([]llm.ContentPart, error) {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	_ = f.Sync() // best effort; see writeAndClose
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("failed to close file: %w", err)
	}
	return []llm.ContentPart{&llm.TextPart{Text: "File written successfully"}}, nil
}

// writeAndClose fills the temp file, restores its mode, and flushes it to
// stable storage before the caller renames it into place. Without the sync a
// power loss can leave the renamed file present but empty: the rename is
// atomic, but it does not order the data write behind it.
func writeAndClose(f *os.File, content string, perm os.FileMode) error {
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	// Sync is best effort: some filesystems reject it and the data is already
	// written, so failing the tool here would be worse than a weaker guarantee.
	_ = f.Sync()
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(f.Name(), perm); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}
	return nil
}
