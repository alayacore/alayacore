package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

const maxFullReadSize = 10 * 1024 * 1024 // 10MB limit for full file reads

const sniffSize = 512 // Number of bytes to check for binary detection

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=The path of the file to read"`
	StartLine string `json:"start_line" jsonschema:"description=Optional: The starting line number (1-indexed)"`
	EndLine   string `json:"end_line" jsonschema:"description=Optional: The ending line number (1-indexed)"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		"Read the contents of a file. Supports optional line range using start_line and end_line parameters (1-indexed).",
	).
		WithSchema(llm.GenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

func executeReadFile(_ context.Context, args ReadFileInput) (llm.ToolResultOutput, error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Check if file is binary before attempting to read
	file, err := os.Open(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	isBinary, err := isBinaryFile(file)
	if err != nil {
		file.Close()
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	if isBinary {
		file.Close()
		return llm.NewTextErrorResponse("file appears to be binary (non-text). This tool only works with text files."), nil
	}

	// Parse line range parameters
	startLine, endLine, err := parseLineRange(args.StartLine, args.EndLine)
	if err != nil {
		file.Close()
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Full file read case
	if startLine == 0 && endLine == 0 {
		file.Close()
		if info.Size() > maxFullReadSize {
			return llm.NewTextErrorResponse(fmt.Sprintf(
				"file is too large for full read (%d bytes, limit is %d). Use start_line and end_line to read a specific range.",
				info.Size(), maxFullReadSize,
			)), nil
		}
		content, err := os.ReadFile(args.Path)
		if err != nil {
			return llm.NewTextErrorResponse(err.Error()), nil
		}
		return llm.NewTextResponse(string(content)), nil
	}

	// Line range case: stream from file to avoid loading entire file into memory
	// Reset file position to beginning for reading lines
	_, err = file.Seek(0, 0)
	if err != nil {
		file.Close()
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	defer file.Close()

	lines, err := readLinesRange(file, startLine, endLine)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	return llm.NewTextResponse(strings.Join(lines, "\n")), nil
}

func parseLineRange(startLineStr, endLineStr string) (startLine, endLine int, err error) {
	if startLineStr == "" && endLineStr == "" {
		return 0, 0, nil
	}

	if startLineStr != "" {
		startLine, err = strconv.Atoi(startLineStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start_line: must be a number")
		}
		if startLine < 1 {
			return 0, 0, fmt.Errorf("start_line must be >= 1")
		}
	}

	if endLineStr != "" {
		endLine, err = strconv.Atoi(endLineStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end_line: must be a number")
		}
		if endLine < 1 {
			return 0, 0, fmt.Errorf("end_line must be >= 1")
		}
	}

	if startLine > 0 && endLine > 0 && startLine > endLine {
		return 0, 0, fmt.Errorf("start_line must be <= end_line")
	}

	return startLine, endLine, nil
}

func readLinesRange(file *os.File, startLine, endLine int) ([]string, error) {
	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle long lines (default is 64KB)
	// We use 1MB which should be reasonable for most cases while still
	// preventing memory exhaustion from extremely long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	currentLine := 1

	for scanner.Scan() {
		if startLine > 0 && currentLine < startLine {
			currentLine++
			continue
		}

		if endLine > 0 && currentLine > endLine {
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

// isBinaryFile detects if a file is binary (non-text) by checking for null bytes
// and the ratio of non-printable characters in the first sniffSize bytes.
func isBinaryFile(file *os.File) (bool, error) {
	buf := make([]byte, sniffSize)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	buf = buf[:n]

	// Empty files are treated as text
	if len(buf) == 0 {
		return false, nil
	}

	// Check for null bytes - a sure sign of binary content
	for _, b := range buf {
		if b == 0 {
			return true, nil
		}
	}

	// Count non-printable characters (excluding common whitespace)
	nonPrintable := 0
	for _, b := range buf {
		// Allow common whitespace: tab, newline, carriage return
		if b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		// Check for non-printable ASCII (below 32 or above 126, but allow extended ASCII 127-255 for UTF-8)
		if b < 32 || b > 255 {
			nonPrintable++
		}
	}

	// If more than 30% non-printable characters, consider it binary
	// This threshold is conservative to avoid false positives on UTF-8 text
	if len(buf) > 0 && float64(nonPrintable)/float64(len(buf)) > 0.30 {
		return true, nil
	}

	return false, nil
}
