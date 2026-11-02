package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alayacore/alayacore/internal/atomicfile"
	"github.com/alayacore/alayacore/internal/llm"
)

// defaultWriteMode is the mode a file this tool creates asks for, before the
// process umask narrows it — the same 0644 os.WriteFile used.
const defaultWriteMode = os.FileMode(0o644)

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
// The write itself is internal/atomicfile's: the new content goes to a sibling
// temp file that is renamed over the target, so a failed or interrupted write can
// never leave a half-written file where a complete one used to be, and a reader
// outside this process cannot catch the file truncated. os.WriteFile truncated the
// target up front, which meant a crash or a full disk destroyed the original — the
// very risk edit_file guards against.
//
// That package also owns the two rules this tool used to spell out here: an
// existing file keeps its exact mode while a new one is narrowed by the umask,
// and a writable file inside a read-only directory still gets written. That last
// one is why this tool calls WriteBestEffort rather than Write: the tool's promise
// is "write the file the model named", and refusing because the *directory* is
// read-only would remove a capability the user already had (their alternative is a
// shell command, not a safer write). The fallback it uses is not atomic, which is
// what internal/atomicfile documents; AlayaCore's own config files call Write and
// fail loudly instead.
func executeWriteFile(_ context.Context, args WriteFileInput) ([]llm.ContentPart, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Serialize against other in-process file mutations (notably edit_file) so
	// a concurrent write cannot interleave with an edit's read-rewrite-rename.
	// See fileMutationMu.
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()

	// Made absolute rather than left as the caller wrote it: the temp file this
	// tool picks is a path the tool chose, and its name reaches the model inside
	// the IO errors atomicfile.Write returns. One form across the whole
	// create-write-rename also means nothing can reinterpret the path
	// mid-operation. Writing through a symlink to the file it points at is
	// atomicfile.Write's own doing (see atomicfile.ResolveSymlink).
	target := args.Path
	if abs, absErr := filepath.Abs(target); absErr == nil {
		target = abs
	}

	inPlace, err := atomicfile.WriteBestEffort(target, []byte(args.Content), defaultWriteMode)
	if err != nil {
		return nil, err
	}

	// Say when the write was not atomic. The fallback is taken only when the
	// directory cannot hold a temp file, and the caller — the model, and the user
	// reading the transcript — must not believe it got the guarantee it did not.
	msg := "File written successfully"
	if inPlace {
		msg += " (in place: the directory is not writable, so this write was not atomic)"
	}
	return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
}
