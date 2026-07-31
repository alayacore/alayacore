package agent

// Persistence service: saving, loading, and formatting sessions.
//
// Extracted from session_persist.go to separate concerns.
// Stateless parsing/serialization utilities remain package-level functions.
// PersistenceService wraps Load/Save with optional future state (e.g. config).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// maxTLVContentSize is the safety limit for a single TLV record's content.
const maxTLVContentSize = 10 * 1024 * 1024

// ErrSessionVersionMismatch is returned when a session file has a version
// that does not match MessageVersion and cannot be loaded.
var ErrSessionVersionMismatch = errors.New("session file version mismatch")

// PersistenceService handles session file I/O and serialization.
// It is stateless and thread-safe.
type PersistenceService struct{}

// NewPersistenceService creates a new PersistenceService.
func NewPersistenceService() *PersistenceService {
	return &PersistenceService{}
}

// defaultPersistence is the package-level persistence service instance.
// Used by session_persist.go wrappers to maintain backward compatibility.
var defaultPersistence = NewPersistenceService()

// LoadSession loads a session from a file.
func (ps *PersistenceService) LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	return parseSessionData(data)
}

// SaveContentToFile saves session contents to a file in markdown format.
func (ps *PersistenceService) SaveContentToFile(path string, meta SessionMeta, contents []llm.ContentPart) error {
	meta.UpdatedAt = time.Now()
	data := SessionData{
		SessionMeta: meta,
		Contents:    contents,
	}

	raw, err := formatSessionMarkdown(&data)
	if err != nil {
		return fmt.Errorf("save: %w", err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// ============================================================================
// Stateless utilities (package-level)
// ============================================================================

// formatFrontmatter writes the frontmatter using config.FormatKeyValue.
func formatFrontmatter(meta *SessionMeta) string {
	return "---\n" + config.FormatKeyValue(meta) + "---\n"
}

// formatSessionMarkdown serializes Content to TLV (without history IDs).
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf, tlvBuf strings.Builder
	buf.WriteString(formatFrontmatter(&data.SessionMeta))

	for _, part := range data.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			return nil, fmt.Errorf("save: %w", err)
		}
		encoded, err := tlv.EncodeTLV(tag, content)
		if err != nil {
			return nil, fmt.Errorf("save: %w", err)
		}
		tlvBuf.WriteString("\n\n")
		tlvBuf.Write(encoded)
	}

	buf.WriteString(tlvBuf.String())
	return []byte(buf.String()), nil
}

// parseFrontmatter extracts the frontmatter and body from content with "---" delimiters.
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", fmt.Errorf("load: session file missing frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", "", fmt.Errorf("load: session file missing frontmatter end marker")
	}

	frontmatter = content[4 : endIdx+4]
	body = content[endIdx+9:]
	return frontmatter, body, nil
}

// parseSessionMeta parses key-value pairs from frontmatter into SessionMeta using struct tags.
func parseSessionMeta(frontmatter string) (SessionMeta, error) {
	var meta SessionMeta
	if errs := config.ParseKeyValue(frontmatter, &meta); len(errs) > 0 {
		// Surface parse errors (unknown keys, type conversion failures) so they
		// are not silently lost when loading session files.
		return meta, fmt.Errorf("load: session frontmatter has parse errors: %v", errs)
	}

	// Check message format version — must match exactly.
	if meta.MessageVersion != MessageVersion {
		return meta, fmt.Errorf("%w: got %d, expected %d",
			ErrSessionVersionMismatch, meta.MessageVersion, MessageVersion)
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

// parseSessionData parses a session file (frontmatter + TLV body) into SessionData.
func parseSessionData(data []byte) (*SessionData, error) {
	frontmatter, body, err := parseFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	meta, err := parseSessionMeta(frontmatter)
	if err != nil {
		return nil, err
	}

	sd := &SessionData{
		SessionMeta: meta,
	}

	if len(body) > 0 {
		content, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Contents = content
	}

	return sd, nil
}

// parseMessagesTLV parses TLV-encoded body into ContentParts.
func parseMessagesTLV(body string) ([]llm.ContentPart, error) {
	est := len(body) / 64
	if est < 8 {
		est = 8
	}
	contents := make([]llm.ContentPart, 0, est)

	reader := newTLVReader(body)
	var seqID uint64

	for {
		tag, raw, err := reader.read()
		if err == io.EOF {
			return contents, nil
		}
		if err != nil {
			return contents, fmt.Errorf("read error at chunk %d: %w", len(contents), err)
		}

		msgPart, err := contentPartFromTLV(tag, raw)
		if err != nil {
			return contents, fmt.Errorf("parse error at chunk %d (tag %q): %w", len(contents), tag, err)
		}
		if msgPart == nil {
			continue
		}

		seqID++
		msgPart.SetHistoryID(seqID)
		contents = append(contents, msgPart)
	}
}

// persistenceTLVReader reads sequential TLV records from a string.
// Exported as an alias from session_persist.go for backward compatibility.
type persistenceTLVReader struct {
	reader *strings.Reader
}

func newTLVReader(body string) *persistenceTLVReader {
	return &persistenceTLVReader{reader: strings.NewReader(body)}
}

// read advances to the next TLV record. Returns io.EOF when exhausted.
func (r *persistenceTLVReader) read() (tag string, content []byte, err error) {
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return "", nil, err
		}
		if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
			_ = r.reader.UnreadByte()
			break
		}
	}

	tagBytes := make([]byte, 2)
	if _, err := io.ReadFull(r.reader, tagBytes); err != nil {
		return "", nil, err
	}

	var length int32
	if err := binary.Read(r.reader, binary.BigEndian, &length); err != nil {
		return "", nil, fmt.Errorf("failed to read length: %w", err)
	}
	if length < 0 || length > maxTLVContentSize {
		return "", nil, fmt.Errorf("invalid length: %d", length)
	}

	content = make([]byte, length)
	if _, err := io.ReadFull(r.reader, content); err != nil {
		return "", nil, fmt.Errorf("failed to read content: %w", err)
	}

	return string(tagBytes), content, nil
}
