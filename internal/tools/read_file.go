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
		`Read file contents. For media files (image, video, audio, document/PDF), returns the content directly for you to see. For text files, supports optional line range using start_line and num_lines parameters (1-indexed). A text read returns at most 64KB; when a range is cut at that budget the reply names the start_line that continues it.`,
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
	// in-place fallback is not like that: atomicfile.Write truncates the target
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

	rng, err := readLinesRange(ctx, file, startLine, args.NumLines)
	if err != nil {
		return nil, err
	}

	// An empty range used to come back as "" — indistinguishable from an
	// empty file, which invites a model to "restore" content by overwriting a
	// file it merely mis-indexed. Say which it was.
	if msg, ok := rangeDiagnostic(rng.lines, startLine, rng.scanned); ok {
		return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
	}

	text := strings.Join(rng.lines, "\n")
	if rng.truncated {
		text = rng.cutHeader(startLine) + "\n\n" + text
	}
	return []llm.ContentPart{&llm.TextPart{Text: text}}, nil
}

// lineRange is what one start_line/num_lines read produced.
type lineRange struct {
	lines []string

	// scanned is how many lines were read from the top of the file. It is the
	// file's line count only when the range produced nothing — that read goes on
	// to the end, because "start_line 99 is past the end of the file (3 lines)"
	// cannot be said without it. A range that produced content stops at the byte
	// budget and does not keep scanning to count what it will not return.
	scanned int

	// truncated reports that the 64KB text budget — not num_lines, and not the
	// end of the file — is what stopped the read.
	truncated bool

	// cutMidLine reports that the stop fell inside a single line: that line is
	// longer than the whole budget, so its remainder is reachable by no
	// start_line, and the header says where it is reachable instead.
	cutMidLine bool
}

// cutHeader explains a range the text budget cut short. The next start_line is
// named because it is the whole reason the budget is not a wall: a range read
// stops at the budget and the model pages on from the number it is given, so a
// file of any size is read deliberately, a page at a time, instead of arriving
// in one reply.
func (r lineRange) cutHeader(startLine int) string {
	last := startLine + len(r.lines) - 1
	if r.cutMidLine {
		return fmt.Sprintf(
			"[Line %d is longer than the 64KB text budget, so only its beginning is shown | the rest of that line is reachable only with execute_command (e.g. cut -c); the lines after it start at start_line=%d]",
			last, last+1)
	}
	return fmt.Sprintf(
		"[Lines %d-%d | the read stopped at the 64KB text budget; pass start_line=%d to continue]",
		startLine, last, last+1)
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
			path, mb, MaxMediaBytes/(1024*1024),
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
		// The caller's own path, in full and not filepath.Base: the model has to
		// be able to act on the file (shrink it, convert it) when an image is
		// too large to forward, and the note that replaces the image carries no
		// path at all. Both branches of this function name it the same way.
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

// cutToBudget keeps the first room bytes of s and marks the cut, so the reader
// of the result knows more exists. It is truncateToBudget plus the note.
func cutToBudget(s string, room int64) string {
	kept, truncated := truncateToBudget(s, room)
	if !truncated {
		return kept
	}
	return kept + budgetTruncationMarker
}

// truncateToBudget keeps the first room bytes of s, stepping back to a UTF-8
// rune boundary so the result never ends on a split multi-byte character. The
// bool reports that bytes were dropped, which is what the callers that phrase
// their own note need — the range read cuts a line too long for its budget and
// says so in the terms of a range, where "read a line range for more" (the
// marker below) would be advice it has already taken.
func truncateToBudget(s string, room int64) (string, bool) {
	if room <= 0 {
		return "", false
	}
	if int64(len(s)) <= room {
		return s, false
	}
	cut := int(room)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
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
// elsewhere).
//
// It holds the same 64KB text budget the full read holds. A range is the
// documented way to read *more* than one reply's worth of a file — the
// full-read marker says exactly that — so it is paging, not an exemption: every
// call is bounded, and the next page is asked for by number. Without this the
// cheapest way around the budget was to name a start line, and one mis-specified
// range put an arbitrarily large file into the context and into memory in a
// single reply.
func readLinesRange(ctx context.Context, file *os.File, startLine, numLines int) (lineRange, error) {
	lr := newLineReader(file)

	var out lineRange
	currentLine := 1
	var bytesRead int64

	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		line, err := lr.nextLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, err
		}
		out.scanned = currentLine

		if currentLine >= startLine {
			if numLines > 0 && len(out.lines) >= numLines {
				break
			}

			lineBytes := int64(len(line)) + 1 // +1 for the newline it ended with
			if bytesRead+lineBytes > maxToolTextBytes {
				// The budget, not the range, is the limit that arrived. Stop
				// before this line so the next page begins on a whole one — with
				// the single exception of the first line, which alone overflows
				// the budget and can only be cut (the same rule the full read
				// follows for a file made of one enormous line).
				if len(out.lines) == 0 {
					kept, _ := truncateToBudget(line, maxToolTextBytes)
					out.lines = append(out.lines, kept)
					out.cutMidLine = true
				}
				out.truncated = true
				return out, nil
			}

			bytesRead += lineBytes
			out.lines = append(out.lines, line)
		}
		currentLine++
	}

	return out, nil
}
