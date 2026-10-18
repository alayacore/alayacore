package agent

// Session persistence: the file format and the two operations over it.
//
// Loading and saving are functions, not a service: there is no state to carry
// between them (a session file is read whole or written whole), and the wrappers
// that used to sit between the Session methods and these had nothing to add.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/wire"
)

// errSessionVersionMismatch is returned when a session file has a version
// that does not match messageVersion and cannot be loaded.
var errSessionVersionMismatch = errors.New("session file version mismatch")

// loadSession loads a session from a file.
func loadSession(path string) (*sessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// No prefix of its own: the caller names the file it asked for, and the
		// error from the OS already says why it could not be read.
		return nil, err
	}
	return parseSessionData(data)
}

// saveSessionFile saves session contents to a file in markdown format.
func saveSessionFile(path string, meta sessionMeta, contents []llm.ContentPart) error {
	meta.UpdatedAt = time.Now()
	data := sessionData{
		sessionMeta: meta,
		Contents:    contents,
	}

	raw, err := formatSessionMarkdown(&data)
	if err != nil {
		// Already names the step that failed (see formatSessionMarkdown); the
		// caller names the operation.
		return err
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		// The caller names the operation, the OS error names the path and the
		// reason. Repeating either here is what made one failure read
		// "save: save: open …".
		return err
	}
	return nil
}

// ============================================================================
// Stateless utilities (package-level)
// ============================================================================

// formatFrontmatter writes the frontmatter using config.FormatKeyValue.
func formatFrontmatter(meta *sessionMeta) string {
	return "---\n" + config.FormatKeyValue(meta) + "---\n"
}

// formatSessionMarkdown serializes Content as frames, one per line and without
// history IDs. The body is exactly the frames: no separator lines, so every line
// of the file after the frontmatter is one frame.
func formatSessionMarkdown(data *sessionData) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString(formatFrontmatter(&data.sessionMeta))

	for _, part := range data.Contents {
		tag, body, err := contentPartJSON(part, "")
		if err != nil {
			return nil, fmt.Errorf("encoding the session body: %w", err)
		}
		frame, err := wire.Encode(tag, body)
		if err != nil {
			return nil, fmt.Errorf("encoding the session body: %w", err)
		}
		buf.Write(frame)
	}

	return []byte(buf.String()), nil
}

// parseFrontmatter extracts the frontmatter and body from content with "---" delimiters.
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", fmt.Errorf("session file missing frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", "", fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter = content[4 : endIdx+4]
	body = content[endIdx+9:]
	return frontmatter, body, nil
}

// parseSessionMeta parses key-value pairs from frontmatter into sessionMeta using struct tags.
func parseSessionMeta(frontmatter string) (sessionMeta, error) {
	var meta sessionMeta
	if errs := config.ParseKeyValue(frontmatter, &meta); len(errs) > 0 {
		// Surface parse errors (unknown keys, type conversion failures) so they
		// are not silently lost when loading session files.
		return meta, fmt.Errorf("session frontmatter has parse errors: %v", errs)
	}

	// Check message format version — must match exactly.
	if meta.MessageVersion != messageVersion {
		return meta, fmt.Errorf("%w: got %d, expected %d",
			errSessionVersionMismatch, meta.MessageVersion, messageVersion)
	}

	// Default reasoning_level to 1 (normal) when the key is absent.
	if !strings.Contains(frontmatter, "reasoning_level:") {
		meta.ReasoningLevel = config.DefaultReasoningLevel
	}

	// Validate reasoning_level range.
	if meta.ReasoningLevel < config.ReasoningLevelOff || meta.ReasoningLevel > config.ReasoningLevelMax {
		meta.ReasoningLevel = config.DefaultReasoningLevel
	}

	return meta, nil
}

// parseSessionData parses a session file (frontmatter + one frame per line) into sessionData.
func parseSessionData(data []byte) (*sessionData, error) {
	// A file written on a platform that translates line endings reads the same as
	// one that does not. The body already tolerates it (lines.Splitter strips the
	// CR before the LF it splits on), so the frontmatter has to as well — without
	// this, an editor's line endings make a session unloadable, with a message
	// about a missing delimiter that says nothing about why.
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	frontmatter, body, err := parseFrontmatter(raw)
	if err != nil {
		return nil, err
	}

	meta, err := parseSessionMeta(frontmatter)
	if err != nil {
		return nil, err
	}

	sd := &sessionData{
		sessionMeta: meta,
	}

	if len(body) > 0 {
		content, err := parseMessageFrames(body)
		if err != nil {
			return nil, err
		}
		sd.Contents = content
	}

	return sd, nil
}

// parseMessageFrames parses a session file's body — one frame per line — into
// ContentParts. The frame reader is the same one the wire uses, so a file that
// saves is a file that loads, and a corrupt one is reported rather than half-read.
func parseMessageFrames(body string) ([]llm.ContentPart, error) {
	est := len(body) / 64
	if est < 8 {
		est = 8
	}
	contents := make([]llm.ContentPart, 0, est)

	reader := wire.NewReader(strings.NewReader(body))
	var seqID uint64
	// frameNo counts frames read, not parts kept: control frames and AF start
	// frames are skipped, and a position that counted the kept ones would name
	// the wrong line of the file.
	frameNo := 0

	for {
		tag, value, err := reader.ReadFrame()
		if errors.Is(err, io.EOF) {
			return contents, nil
		}
		if err != nil {
			return contents, fmt.Errorf("read error at frame %d: %w", frameNo+1, err)
		}
		frameNo++

		msgPart, err := contentPartFromJSON(tag, []byte(value))
		if err != nil {
			return contents, fmt.Errorf("parse error at frame %d (tag %q): %w", frameNo, tag, err)
		}
		if msgPart == nil {
			continue
		}

		seqID++
		msgPart.SetHistoryID(seqID)
		contents = append(contents, msgPart)
	}
}
