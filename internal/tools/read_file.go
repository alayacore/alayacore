package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/alayacore/alayacore/internal/llm"
)

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" jsonschema:"required" jsonschema_desc:"File path to read"`
	StartLine int    `json:"start_line" jsonschema_desc:"Starting line number (1-indexed)"`
	NumLines  int    `json:"num_lines" jsonschema_desc:"Number of lines to read from start_line"`
}

func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		`Read file contents. For media files (image, video, audio, document/PDF), returns the content directly for you to see. For text files, supports optional line range using start_line and num_lines parameters (1-indexed).`,
	).
		WithSchema(llm.MustGenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

// sniffMedia checks whether the file at path is a supported media type
// (image, video, audio, document) by examining both the extension and the
// open file's content. Content sniffing is used to catch false positives
// where the extension-based MIME type is misleading (e.g., .mod →
// audio/x-mod for a go.mod text file); it only overrides the extension
// when the content explicitly says text. The handle is rewound to offset 0
// before returning, so the caller reads the full content from the start.
// Returns the MIME type and true if it's a media file.
func sniffMedia(file *os.File, path string) (mimeType string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))

	// Get extension-based MIME
	var extMime string
	if ext != "" {
		extMime = mime.TypeByExtension(ext)
	}

	// Sniff content (first 512 bytes) for verification, then rewind so the
	// caller's subsequent read starts at offset 0 on the same handle.
	// Seek failure is not propagated: sniffing is best-effort, and a seek
	// on a regular file opened read-only does not fail in practice.
	defer func() { _, _ = file.Seek(0, io.SeekStart) }()

	buf := make([]byte, 512)
	n, _ := file.Read(buf) // partial read is fine for sniffing

	var contentMime string
	if n > 0 {
		contentMime = http.DetectContentType(buf[:n])
	}

	// If extension says media, verify with content sniffing
	if isKnownMediaType(extMime) {
		// Content explicitly says text — extension was misleading
		// (e.g., .mod → audio/x-mod but go.mod is text)
		if contentMime != "" && strings.HasPrefix(contentMime, "text/") {
			return "", false
		}
		// Content isn't text — trust extension
		return extMime, true
	}

	// Extension didn't match — use content sniffing
	return knownMediaType(contentMime)
}

// isKnownMediaType returns true if the MIME type is one we can render.
func isKnownMediaType(mimeType string) bool {
	_, ok := knownMediaType(mimeType)
	return ok
}

// knownMediaType returns the MIME type if it's a known renderable type.
func knownMediaType(mimeType string) (string, bool) {
	switch {
	case mimeType == "":
		return "", false
	case strings.HasPrefix(mimeType, "image/"),
		strings.HasPrefix(mimeType, "video/"),
		strings.HasPrefix(mimeType, "audio/"):
		return mimeType, true
	case strings.HasPrefix(mimeType, "application/pdf"),
		strings.HasPrefix(mimeType, "application/vnd.openxmlformats-officedocument"),
		strings.HasPrefix(mimeType, "application/vnd.ms-"),
		strings.HasPrefix(mimeType, "application/msword"):
		return mimeType, true
	default:
		return "", false
	}
}

func executeReadFile(ctx context.Context, args ReadFileInput) ([]llm.ContentPart, error) {
	// Serialize against in-process file mutations for the whole read.
	// edit_file's commit is a rename, so a reader could skip the lock and
	// still see a whole file — the old one or the new one. write_file's
	// in-place fallback is not like that: writeInPlace truncates the target
	// and then writes the new content into it, so a read overlapping that
	// window returns a prefix of the new content that looks like a complete,
	// merely shorter file — silently wrong, and nothing in the result says
	// so. The lock is what keeps a reader out of that window; see
	// fileMutationMu.
	//
	// The read lock, not the write lock: a read has to exclude a mutation, and
	// nothing else. A model reading eight files in one step still reads them
	// in parallel, which is why this is a RWMutex and why the cost of the
	// exclusion stops at the writers.
	//
	// The price is that a read which blocks — a FIFO, a slow network mount —
	// holds the read lock for as long as it blocks, and every in-process file
	// mutation queues behind it (other reads do not). Accepted: such a read
	// already keeps the step from finishing at all (the runner waits for its
	// tool goroutines), so nothing is lost that could otherwise have run. See
	// docs/tool-execution.md.
	fileMutationMu.RLock()
	defer fileMutationMu.RUnlock()

	// Open once and operate on the same handle for every check (size,
	// media sniffing) and every read. Statting the open file instead of
	// the path means the size decision and the content read are the same
	// snapshot — the file cannot change between check and use (TOCTOU).
	file, err := os.Open(args.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	// Validate line parameters
	if valErr := validateLineParams(args.StartLine, args.NumLines); valErr != nil {
		return nil, valErr
	}

	// Detect media files — return content directly (only when reading full file)
	if args.StartLine == 0 && args.NumLines == 0 {
		if mimeType, ok := sniffMedia(file, args.Path); ok {
			return readMediaFile(file, args.Path, info, mimeType)
		}
	}

	// Full file read case
	if args.StartLine == 0 && args.NumLines == 0 {
		if info.Size() > maxToolTextBytes {
			return readLargeFileTruncated(file, info.Size())
		}
		var content []byte
		content, err = io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		return []llm.ContentPart{&llm.TextPart{Text: string(content)}}, nil
	}

	// Line range case: stream from file to avoid loading entire file into memory.
	// Normalize: start_line=0 means start at line 1.
	startLine := args.StartLine
	if startLine == 0 {
		startLine = 1
	}

	lines, totalSeen, err := readLinesRange(ctx, file, startLine, args.NumLines)
	if err != nil {
		return nil, err
	}

	// An empty range used to come back as "" — indistinguishable from an
	// empty file, which invites a model to "restore" content by overwriting a
	// file it merely mis-indexed. Say which it was.
	if msg, ok := rangeDiagnostic(lines, startLine, totalSeen); ok {
		return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
	}

	return []llm.ContentPart{&llm.TextPart{Text: strings.Join(lines, "\n")}}, nil
}

// rangeDiagnostic explains an empty line-range result, returning ok=false when
// there is nothing to explain (the range produced content).
func rangeDiagnostic(lines []string, startLine, totalSeen int) (string, bool) {
	if len(lines) > 0 {
		return "", false
	}
	switch {
	case totalSeen == 0:
		return "[file is empty — 0 lines]", true
	case startLine > totalSeen:
		return fmt.Sprintf(
			"[start_line %d is past the end of the file (%d lines); no content to read]",
			startLine, totalSeen), true
	}
	return "", false
}

// MaxMediaBytes caps one media file (image, video, audio, document) read fully
// into memory and embedded as a base64 data URI — the same ceiling for a file a
// tool reads and for one a user attaches, because it is the same decision. Unlike
// text (capped at maxToolTextBytes), media cannot be meaningfully truncated: the
// whole file is loaded and then base64-encoded (×4/3), so a multi-GB video would
// OOM the process. Files above the cap are reported instead of read.
//
// The frame it becomes is base64 inside a JSON wrapper, so this and the wire's
// MaxFrameSize are one decision rather than two that have to agree; the derived
// sizes live in one place, in internal/wire's MaxFrameSize comment, and are held
// together by TestMaxMediaFitsInAFrame.
const MaxMediaBytes = 16 * 1024 * 1024 // 16MB

// readMediaFile reads a media file from the already-open handle and returns
// a ContentPart with base64-encoded data. Supported types: image, video,
// audio, document (PDF, etc.). info comes from the same handle (file.Stat),
// so the size check and the read are one consistent snapshot.
// Files larger than MaxMediaBytes are not loaded; the caller receives a
// text message pointing at the limit so it can pick a smaller file instead.
func readMediaFile(file *os.File, path string, info os.FileInfo, mimeType string) ([]llm.ContentPart, error) {
	if info.Size() > MaxMediaBytes {
		mb := float64(info.Size()) / (1024 * 1024)
		return []llm.ContentPart{&llm.TextPart{Text: fmt.Sprintf(
			"File %s is %.1fMB — larger than the %dMB media read limit. Media is embedded as base64 and cannot be truncated; use a smaller file or process it with execute_command instead.",
			filepath.Base(path), mb, MaxMediaBytes/(1024*1024),
		)}}, nil
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	sizeKB := float64(len(data)) / 1024

	return []llm.ContentPart{
		// The full path, not filepath.Base: the model needs to be able to act
		// on the file (shrink it, convert it) when an image is too large to
		// forward — the note that replaces the image carries no path.
		&llm.TextPart{Text: fmt.Sprintf("Read %s (%.1fKB, %s)", path, sizeKB, mimeType)},
		llm.MediaContentPart(mimeType, dataURI),
	}, nil
}

// newLineReader wraps a file for logical-line reading with bounded memory.
//
// bufio.Scanner was used here with a 1MB buffer, which meant any file
// containing a single longer line failed the read outright with
// "bufio.Scanner: token too long" — and because the range path had to
// tokenize those lines just to count past them, start_line/num_lines could
// not recover either. Real files hit this constantly (minified bundles,
// single-line JSON, base64 blobs, notebooks), and the message the model got
// pointed it at the one workaround that also failed.
//
// lineReader never fails on a long line: it keeps at most maxLineBytes of each
// line, marks it truncated, and discards the remainder while still counting it
// as one line. Memory is O(maxLineBytes) per line regardless of the file.
func newLineReader(file *os.File) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(file, 64*1024)}
}

// maxLineBytes bounds how much of a single logical line is kept. Larger lines
// are truncated and marked, so a file made entirely of one enormous line still
// yields useful content instead of an error.
const maxLineBytes = 1 << 20 // 1MB

// truncatedLineMarker is appended to a line that exceeded maxLineBytes.
const truncatedLineMarker = " […line truncated: exceeds 1MB per line…]"

// lineReader yields logical lines (newline-terminated, without the newline)
// from a bufio.Reader, capping each line's length.
type lineReader struct {
	r         *bufio.Reader
	buf       []byte // assembled bytes of the line being read
	truncated bool   // current line exceeded the cap
}

// nextLine returns the next line. Truncation is reported inside the line
// itself (truncatedLineMarker), so no separate flag is needed. A final line
// without a trailing newline is still returned with a nil error; io.EOF means
// no bytes remain.
func (lr *lineReader) nextLine() (string, error) {
	lr.buf = lr.buf[:0]
	lr.truncated = false

	for {
		// ReadSlice returns the buffered bytes with ErrBufferFull when no
		// newline is found, so the loop keeps draining until the terminator.
		part, err := lr.r.ReadSlice('\n')
		if len(part) > 0 {
			if nl := bytes.IndexByte(part, '\n'); nl >= 0 {
				lr.collect(part[:nl])
				return lr.finish(), nil
			}
			lr.collect(part)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(lr.buf) == 0 && !lr.truncated {
					return "", io.EOF
				}
				return lr.finish(), nil
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			return "", err
		}
	}
}

// collect appends line bytes while the cap allows, marking the line truncated
// once it does not. Excess bytes are dropped, not buffered.
func (lr *lineReader) collect(body []byte) {
	room := maxLineBytes - len(lr.buf)
	switch {
	case room <= 0:
		lr.truncated = true
	case len(body) > room:
		lr.buf = append(lr.buf, body[:room]...)
		lr.truncated = true
	default:
		lr.buf = append(lr.buf, body...)
	}
}

// finish renders the assembled line, matching bufio.Scanner's ScanLines
// behavior of dropping a carriage return before the newline. A truncated line
// cannot have lost a '\r' that mattered, so the drop is skipped there.
func (lr *lineReader) finish() string {
	s := string(lr.buf)
	if lr.truncated {
		return s + truncatedLineMarker
	}
	return strings.TrimSuffix(s, "\r")
}

// readLargeFileTruncated reads a large file and returns up to maxToolTextBytes
// bytes of content with a metadata header showing total line count and size.
// Single-pass: collects lines until the byte limit, then continues counting.
func readLargeFileTruncated(file *os.File, totalSize int64) ([]llm.ContentPart, error) {
	lr := newLineReader(file)

	var lines []string
	var bytesRead int64
	totalLines := 0
	collecting := true

	for {
		line, err := lr.nextLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		totalLines++
		if !collecting {
			continue
		}

		lineBytes := int64(len(line)) + 1 // +1 for newline
		if bytesRead+lineBytes > maxToolTextBytes {
			if len(lines) == 0 {
				// The first line alone overflows the whole budget. It used to
				// be emitted in full (a 700KB line against a documented 64KB
				// limit) because the "always keep one line" rule bypassed the
				// cap; cut it to the budget instead.
				shown := cutToBudget(line, maxToolTextBytes-bytesRead)
				lines = append(lines, shown)
				bytesRead += int64(len(shown))
			}
			collecting = false
		} else {
			lines = append(lines, line)
			bytesRead += lineBytes
		}
	}

	shownLines := len(lines)
	content := strings.Join(lines, "\n")
	header := fmt.Sprintf(
		"[Lines 1-%d of %d | %.1fKB of %.1fKB shown]\n",
		shownLines, totalLines,
		float64(bytesRead)/1024, float64(totalSize)/1024,
	)

	return []llm.ContentPart{&llm.TextPart{Text: header + "\n" + content}}, nil
}

// budgetTruncationMarker is appended when the read budget cuts into a line.
const budgetTruncationMarker = " […truncated at the 64KB read limit; read a line range for more…]"

// cutToBudget keeps the first room bytes of s, stepping back to a UTF-8 rune
// boundary so the result never ends on a split multi-byte character.
func cutToBudget(s string, room int64) string {
	if room <= 0 {
		return ""
	}
	if int64(len(s)) <= room {
		return s
	}
	cut := int(room)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + budgetTruncationMarker
}

func validateLineParams(startLine, numLines int) error {
	if startLine < 0 {
		return fmt.Errorf("start_line must be >= 0")
	}
	if numLines < 0 {
		return fmt.Errorf("num_lines must be >= 0")
	}
	// 0 means "not specified" (default int value)
	// Positive startLine values are 1-indexed line numbers
	return nil
}

// readLinesRange streams from the file to avoid loading it whole into memory,
// collecting at most numLines lines starting at startLine (1-indexed; 0 means
// from the beginning when numLines is also 0 it is the full-file case handled
// elsewhere). It also reports how many lines the file actually holds, so the
// caller can tell "out of range" apart from "empty result".
func readLinesRange(ctx context.Context, file *os.File, startLine, numLines int) ([]string, int, error) {
	lr := newLineReader(file)

	var lines []string
	currentLine := 1

	for {
		select {
		case <-ctx.Done():
			return nil, currentLine - 1, ctx.Err()
		default:
		}

		line, err := lr.nextLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, currentLine - 1, err
		}

		if currentLine >= startLine {
			if numLines > 0 && len(lines) >= numLines {
				break
			}
			lines = append(lines, line)
		}
		currentLine++
	}

	return lines, currentLine - 1, nil
}
