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

	"github.com/alayacore/alayacore/internal/atomicfile"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// errSessionVersionMismatch is returned when a session file has a version
// that does not match protocol.MessageVersion and cannot be loaded.
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
	// Atomic (internal/atomicfile): the file being replaced is the whole
	// conversation. os.WriteFile truncated it first, so a crash or a full disk
	// during a save destroyed the previous transcript as well as failing to
	// write the new one — and a second instance saving the same --session path
	// could interleave with this one instead of merely winning.
	if err := atomicfile.Write(path, raw, 0600); err != nil {
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

	// The protocol version must match exactly: the number says which vocabulary
	// and framing wrote the file, and a reader that guessed would be reading a
	// file it was never written for. Converting an older file is editing its
	// frontmatter line — the user's call; nothing here migrates — and
	// docs/architecture.md's "Wire version history" says what each generation
	// moved.
	if meta.MessageVersion != protocol.MessageVersion {
		return meta, fmt.Errorf("%w: got %d, expected %d",
			errSessionVersionMismatch, meta.MessageVersion, protocol.MessageVersion)
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
		if err := invalidToolPair(content); err != nil {
			return nil, err
		}
		sd.Contents = content
	}

	return sd, nil
}

// invalidToolPair reports the first tool record in parts that has no partner.
// The API rejects either shape — a call with no answer is an unfinished step, and
// a result with no earlier call answers nothing — so a body that contains one is
// not a conversation this program can continue from.
//
// The agent loop cannot produce such a body: every step finish trims unpaired
// calls (cleanIncompleteToolInputs), and :fork refuses to write a prefix with one
// (unpairedToolUse, the call-side half of this check). That leaves the file
// itself — hand-edited, truncated, or written by something else — and loading is
// where it has to be caught. The alternative is the provider's 400 on the first
// prompt, which names neither the record nor the file.
func invalidToolPair(parts []llm.ContentPart) error {
	// First index of each record and the order they appear in, so the message
	// names the first problem in the file rather than whichever map entry a
	// random iteration reached.
	callAt := make(map[string]int, len(parts))
	resultAt := make(map[string]int, len(parts))
	var calls, results []string
	for i, p := range parts {
		switch v := p.(type) {
		case *llm.ToolInputPart:
			if _, ok := callAt[v.ID]; !ok {
				callAt[v.ID] = i
				calls = append(calls, v.ID)
			}
		case *llm.ToolOutputPart:
			if _, ok := resultAt[v.ID]; !ok {
				resultAt[v.ID] = i
				results = append(results, v.ID)
			}
		}
	}
	for _, id := range calls {
		if _, ok := resultAt[id]; !ok {
			return fmt.Errorf("tool call %q has no result", id)
		}
	}
	for _, id := range results {
		at, ok := callAt[id]
		if !ok {
			return fmt.Errorf("tool result %q answers no tool call", id)
		}
		if at > resultAt[id] {
			return fmt.Errorf("tool result %q precedes the call it answers", id)
		}
	}
	return nil
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
