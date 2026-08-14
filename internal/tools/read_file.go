package tools

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

const maxTextReadSize = 64 * 1024 // 64KB limit for text files (~16K tokens)

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

// isMediaFile checks whether the file at path is a supported media type
// (image, video, audio, document) by examining both the extension and content.
// Content sniffing is used to catch false positives where the extension-based
// MIME type is misleading (e.g., .mod → audio/x-mod for a go.mod text file).
// Only overrides extension-based detection when content explicitly says text.
// Returns the MIME type and true if it's a media file.
func isMediaFile(path string) (mimeType string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))

	// Get extension-based MIME
	var extMime string
	if ext != "" {
		extMime = mime.TypeByExtension(ext)
	}

	// Open and sniff content (first 512 bytes) for verification
	f, err := os.Open(path)
	if err != nil {
		return knownMediaType(extMime)
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf) // partial read is fine for sniffing

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
	info, err := os.Stat(args.Path)
	if err != nil {
		return nil, err
	}

	// Validate line parameters
	if valErr := validateLineParams(args.StartLine, args.NumLines); valErr != nil {
		return nil, valErr
	}

	// Detect media files — return content directly (only when reading full file)
	if args.StartLine == 0 && args.NumLines == 0 {
		if mimeType, ok := isMediaFile(args.Path); ok {
			return readMediaFile(args.Path, mimeType)
		}
	}

	// Full file read case
	if args.StartLine == 0 && args.NumLines == 0 {
		if info.Size() > maxTextReadSize {
			return readLargeFileTruncated(args.Path, info.Size())
		}
		var content []byte
		content, err = os.ReadFile(args.Path)
		if err != nil {
			return nil, err
		}
		return []llm.ContentPart{&llm.TextPart{Text: string(content)}}, nil
	}

	// Line range case: stream from file to avoid loading entire file into memory
	file, err := os.Open(args.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Normalize: start_line=0 means start at line 1
	startLine := args.StartLine
	if startLine == 0 {
		startLine = 1
	}

	lines, err := readLinesRange(ctx, file, startLine, args.NumLines)
	if err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: strings.Join(lines, "\n")}}, nil
}

// maxMediaReadSize caps the size of media files (image/video/audio/document)
// read fully into memory and embedded as base64 data URIs. Unlike text files
// (capped at maxTextReadSize), media cannot be meaningfully truncated — the
// whole file is loaded and then base64-encoded (×1.33), so a multi-GB video
// would OOM the process. Files above the cap are reported instead of read.
const maxMediaReadSize = 16 * 1024 * 1024 // 16MB

// readMediaFile reads a media file and returns a ContentPart with base64-encoded data.
// Supported types: image, video, audio, document (PDF, etc.).
// Files larger than maxMediaReadSize are not loaded; the caller receives a
// text message pointing at the limit so it can pick a smaller file instead.
func readMediaFile(path, mimeType string) ([]llm.ContentPart, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxMediaReadSize {
		mb := float64(info.Size()) / (1024 * 1024)
		return []llm.ContentPart{&llm.TextPart{Text: fmt.Sprintf(
			"File %s is %.1fMB — larger than the %dMB media read limit. Media is embedded as base64 and cannot be truncated; use a smaller file or process it with execute_command instead.",
			filepath.Base(path), mb, maxMediaReadSize/(1024*1024),
		)}}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	sizeKB := float64(len(data)) / 1024

	return []llm.ContentPart{
		&llm.TextPart{Text: fmt.Sprintf("Read %s (%.1fKB, %s)", filepath.Base(path), sizeKB, mimeType)},
		llm.MediaContentPart(mimeType, dataURI),
	}, nil
}

// readLargeFileTruncated reads a large file and returns up to maxTextReadSize
// bytes of content with a metadata header showing total line count and size.
// Single-pass: collects lines until the byte limit, then continues counting.
func readLargeFileTruncated(path string, totalSize int64) ([]llm.ContentPart, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Scanner buffer (1MB) is much larger than the truncation limit
	// (maxTextReadSize, 64KB) because the scanner needs to hold individual
	// lines that may exceed 64KB. The truncation limit applies to total
	// output, not individual lines — a single line can be arbitrarily long
	// (e.g. minified JS, base64-encoded data). The 1MB buffer covers the
	// vast majority of real-world cases while preventing memory exhaustion
	// from pathological input.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	var bytesRead int64
	totalLines := 0
	collecting := true

	for scanner.Scan() {
		totalLines++
		if collecting {
			line := scanner.Text()
			lineBytes := int64(len(line)) + 1 // +1 for newline

			if bytesRead+lineBytes > maxTextReadSize && len(lines) > 0 {
				collecting = false
				continue
			}
			lines = append(lines, line)
			bytesRead += lineBytes
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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

func readLinesRange(ctx context.Context, file *os.File, startLine, numLines int) ([]string, error) {
	scanner := bufio.NewScanner(file)
	// Scanner buffer (1MB) is much larger than the truncation limit
	// (maxTextReadSize, 64KB) because the scanner needs to hold individual
	// lines that may exceed 64KB. The truncation limit applies to total
	// output, not individual lines — a single line can be arbitrarily long
	// (e.g. minified JS, base64-encoded data). The 1MB buffer covers the
	// vast majority of real-world cases while preventing memory exhaustion
	// from pathological input.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	currentLine := 1

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if currentLine < startLine {
			currentLine++
			continue
		}

		if numLines > 0 && len(lines) >= numLines {
			break
		}

		lines = append(lines, scanner.Text())
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}
