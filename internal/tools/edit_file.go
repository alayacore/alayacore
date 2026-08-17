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
	OldString string `json:"old_string" jsonschema:"required" jsonschema_desc:"Text to find and replace. Exact match preferred; whitespace-only differences (indentation, tabs, line endings, extra spaces) are tolerated automatically."`
	NewString string `json:"new_string" jsonschema:"required" jsonschema_desc:"Replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() llm.Tool {
	return llm.NewTool(
		"edit_file",
		`Apply a string replacement to a file (not regex). old_string is matched exactly first; if no exact match exists, whitespace differences (indentation, tabs, spaces, CRLF vs LF, trailing spaces) are tolerated automatically. Non-whitespace content must match exactly. If old_string appears multiple times, the edit fails.`,
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
		return executeEditFileTolerant(ctx, session, args)
	}

	if err := session.Commit(); err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: "File edited successfully"}}, nil
}

// ============================================================================
// Whitespace-tolerant fallback matching
// ============================================================================
//
// When exact matching fails, a second strategy is tried: ASCII whitespace
// runs (spaces, tabs, CR, LF) are collapsed to a single space in both the
// file and old_string, and the normalized old_string is searched for there.
// This tolerates the whitespace mistakes small models commonly make
// (indentation counts, tabs vs spaces, CRLF vs LF, trailing spaces) while
// still requiring non-whitespace content to match exactly.
//
// Matching is deliberately one-directional: a whitespace RUN in the file can
// match a single space in old_string, but old_string cannot contain MORE
// whitespace than the file. This preserves the existence of whitespace —
// collapsing to nothing would make "foo bar" match "foobar", which is
// unsafe. Multi-byte UTF-8 is untouched (only ASCII whitespace bytes are
// normalized).

// errNormalizedNotFound is returned by findNormalizedMatch when the
// whitespace-normalized old_string does not appear in the file.
var errNormalizedNotFound = errors.New("normalized old_string not found")

// isASCIIWhitespace reports whether b is a normalized whitespace byte.
func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// normalizeWhitespace collapses every run of ASCII whitespace bytes in src
// to a single space, preserving all non-whitespace bytes (including
// multi-byte UTF-8 sequences). Used to build the normalized old_string.
func normalizeWhitespace(src []byte) []byte {
	dst := make([]byte, 0, len(src))
	pendingSpace := false
	for _, c := range src {
		if isASCIIWhitespace(c) {
			pendingSpace = true
			continue
		}
		if pendingSpace {
			dst = append(dst, ' ')
			pendingSpace = false
		}
		dst = append(dst, c)
	}
	if pendingSpace {
		dst = append(dst, ' ')
	}
	return dst
}

// findNormalizedMatch streams src once, normalizing ASCII whitespace runs to
// single spaces, and searches for the normalized old_string.
//
// It returns the normalized (whitespace-collapsed) start index of the match.
// Errors: errNormalizedNotFound if absent, a descriptive error if present
// multiple times, or an I/O error. Memory stays bounded by the search window
// appendNormalized appends the normalized form of chunk to dst, collapsing
// ASCII whitespace runs to single spaces. pendingSpace carries a trailing
// whitespace run across chunk boundaries (it becomes a single space before
// the next non-whitespace byte, or at EOF).
func appendNormalized(dst, chunk []byte, pendingSpace *bool) []byte {
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]
		if isASCIIWhitespace(c) {
			*pendingSpace = true
			continue
		}
		if *pendingSpace {
			dst = append(dst, ' ')
			*pendingSpace = false
		}
		dst = append(dst, c)
	}
	return dst
}

// (≈ len(old_string) + chunk), independent of file size.
func findNormalizedMatch(ctx context.Context, src io.Reader, oldString string) (int64, error) {
	normOld := normalizeWhitespace([]byte(oldString))
	const chunkSize = 4096

	buffer := make([]byte, 0, min(2*len(normOld)+chunkSize, maxEditBufferCapacity))
	chunk := make([]byte, chunkSize)

	var (
		pendingSpace bool
		released     int64 // normalized chars already discarded from buffer
		occurrences  int
		matchStart   int64
	)

	search := func() error {
		for {
			idx := bytes.Index(buffer, normOld)
			if idx == -1 {
				return nil
			}
			occurrences++
			if occurrences > 1 {
				return fmt.Errorf("old_string found multiple times in file after whitespace normalization. Include more surrounding context to make it unique, or use a different portion of text")
			}
			matchStart = released + int64(idx)
			buffer = buffer[idx+len(normOld):]
			released += int64(idx + len(normOld))
		}
	}

	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("Canceled")
		default:
		}

		n, err := src.Read(chunk)
		buffer = appendNormalized(buffer, chunk[:n], &pendingSpace)

		if serr := search(); serr != nil {
			return 0, serr
		}

		// Keep enough of the tail to catch matches spanning chunk boundaries.
		if len(buffer) > len(normOld) {
			keep := len(buffer) - len(normOld)
			released += int64(keep)
			buffer = buffer[keep:]
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("failed to read file: %w", err)
		}
	}

	// Flush a trailing whitespace run at EOF so the file's final normalized
	// char (if any) participates in matching.
	if pendingSpace {
		buffer = append(buffer, ' ')
		if serr := search(); serr != nil {
			return 0, serr
		}
	}

	if occurrences == 0 {
		return 0, errNormalizedNotFound
	}
	return matchStart, nil
}

// locateAndWrite streams src a second time, normalizing whitespace on the
// fly, and writes dst = file[0:start) + newString + file[end:), where start
// and end are the original byte offsets of the normalized span
// [startNorm, endNorm) found by findNormalizedMatch.
//
// The source is read exactly once: prefix bytes are written as they stream
// past, the matched span is skipped, and newString plus the suffix are
// written once the span's end is located. Memory is O(chunk) regardless of
// file size. Fails with an internal error if the span cannot be located,
// which indicates the two passes disagreed (an implementation bug).
func locateAndWrite(ctx context.Context, src io.Reader, dst io.Writer, startNorm, endNorm int64, newString string) error {
	if startNorm < 0 || endNorm <= startNorm {
		return fmt.Errorf("internal error: invalid normalized span [%d,%d)", startNorm, endNorm)
	}

	const chunkSize = 4096
	chunk := make([]byte, chunkSize)

	var (
		pendingSpace bool
		count        int64 // normalized chars generated so far
		state        int   // 0 = prefix, 1 = matched span, 2 = suffix
		newWritten   bool
	)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Canceled")
		default:
		}

		n, rerr := src.Read(chunk)
		startIdx, endIdx := scanNormalizedSpan(chunk[:n], &pendingSpace, &count, state, startNorm, endNorm)

		var werr error
		state, werr = writeChunkByState(dst, state, chunk[:n], startIdx, endIdx, newString, &newWritten)
		if werr != nil {
			return werr
		}

		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("failed to read file: %w", rerr)
		}
	}

	switch state {
	case 0:
		return fmt.Errorf("internal error: normalized match start not located")
	case 1:
		// Match spans to end of file.
		if werr := writeNewString(dst, newString, &newWritten); werr != nil {
			return werr
		}
	}
	if count < endNorm {
		return fmt.Errorf("internal error: normalized char count %d < endNorm %d", count, endNorm)
	}
	return nil
}

// scanNormalizedSpan scans one chunk, normalizing whitespace and counting
// normalized chars, and reports the first index (if any) at which the chunk
// crosses the match start and end. A normalized char is generated per
// non-whitespace byte and per whitespace run (at its first byte); char index
// equals count before incrementing, so startNorm/endNorm are compared
// against count BEFORE the increment.
func scanNormalizedSpan(buf []byte, pendingSpace *bool, count *int64, state int, startNorm, endNorm int64) (startIdx, endIdx int) {
	startIdx, endIdx = -1, -1
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if isASCIIWhitespace(c) {
			if !*pendingSpace {
				*pendingSpace = true
				if state == 0 && *count == startNorm {
					startIdx = i
				}
				if state < 2 && *count == endNorm {
					endIdx = i
				}
				*count++
			}
			continue
		}
		if *pendingSpace {
			*pendingSpace = false
		}
		if state == 0 && *count == startNorm {
			startIdx = i
		}
		if state < 2 && *count == endNorm {
			endIdx = i
		}
		*count++
	}
	return startIdx, endIdx
}

// writeChunkByState writes the appropriate slice of buf for the current
// state (0 = prefix before the match, 1 = inside the match, 2 = suffix after
// it), inserts newString when the match end is crossed, and returns the
// updated state.
func writeChunkByState(dst io.Writer, state int, buf []byte, startIdx, endIdx int, newString string, newWritten *bool) (int, error) {
	switch state {
	case 0:
		if startIdx < 0 {
			return state, writeBuf(dst, buf)
		}
		if werr := writeBuf(dst, buf[:startIdx]); werr != nil {
			return state, werr
		}
		if endIdx < 0 {
			return 1, nil
		}
		if werr := writeNewString(dst, newString, newWritten); werr != nil {
			return state, werr
		}
		return 2, writeBuf(dst, buf[endIdx:])
	case 1:
		if endIdx < 0 {
			return state, nil
		}
		if werr := writeNewString(dst, newString, newWritten); werr != nil {
			return state, werr
		}
		return 2, writeBuf(dst, buf[endIdx:])
	default:
		return state, writeBuf(dst, buf)
	}
}

// writeNewString writes newString to dst once, guarded by newWritten.
func writeNewString(dst io.Writer, newString string, newWritten *bool) error {
	if *newWritten {
		return nil
	}
	if _, err := dst.Write([]byte(newString)); err != nil {
		return fmt.Errorf("failed to write to temp file: %v", err)
	}
	*newWritten = true
	return nil
}

// writeBuf writes buf to dst with a uniform error message.
func writeBuf(dst io.Writer, buf []byte) error {
	if _, err := dst.Write(buf); err != nil {
		return fmt.Errorf("failed to write to temp file: %v", err)
	}
	return nil
}

// executeEditFileTolerant is the whitespace-tolerant fallback path: it runs
// only when exact matching failed. It rewinds the source file, searches the
// normalized stream, then performs a second streaming pass that writes the
// replacement. Returns a success message that explicitly marks the edit as
// whitespace-insensitive so the model knows its old_string was not exact.
func executeEditFileTolerant(ctx context.Context, session *editSession, args EditFileInput) ([]llm.ContentPart, error) {
	if _, err := session.srcFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek source file: %v", err)
	}

	startNorm, err := findNormalizedMatch(ctx, session.srcFile, args.OldString)
	if err != nil {
		if errors.Is(err, errNormalizedNotFound) {
			return nil, fmt.Errorf(
				"old_string not found in file. Both exact matching and whitespace-tolerant matching (indentation, tabs, line endings, extra spaces) failed, so the content differs beyond whitespace.\n\nSearched for:\n%q",
				args.OldString)
		}
		return nil, err
	}

	normOld := normalizeWhitespace([]byte(args.OldString))
	if _, err := session.srcFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek source file: %v", err)
	}
	// The exact-matching pass already streamed the whole file into the temp
	// file (it only failed to find a match). Rewind and truncate it so
	// locateAndWrite replaces the content instead of appending.
	if err := session.tempFile.Truncate(0); err != nil {
		return nil, fmt.Errorf("failed to truncate temp file: %v", err)
	}
	if _, err := session.tempFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek temp file: %v", err)
	}
	if err := locateAndWrite(ctx, session.srcFile, session, startNorm, startNorm+int64(len(normOld)), args.NewString); err != nil {
		return nil, err
	}

	if err := session.Commit(); err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: "File edited successfully (whitespace-insensitive match)"}}, nil
}
