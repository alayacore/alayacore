// Package sessionmigrate converts a v13 session file to the v14 wire format.
//
// The core does not read v13 files any more — loading demands an exact
// message_version match — so a migration needs its own copy of the old format's
// shape. This package is that copy. It is a one-off tool: nothing in the
// running program imports it (only misc/session_upgrade_20261014.go and the
// migration test do), and the old tags it understands are exactly the ones the
// v13 writer emitted: UT/UI/UV/UA/UD, AT/AR, AF, UF.
//
// Old files carry no per-message time, so the converted parts carry none either.
// The adapter stamps those with its receipt clock, exactly as v13 did.
package sessionmigrate

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// Migrate rewrites a whole v13 session file (frontmatter + TLV body) as v14.
func Migrate(raw []byte) ([]byte, error) {
	front, body, err := split(raw)
	if err != nil {
		return nil, err
	}
	newBody, err := upgradeBody(body)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{}, upgradeFrontmatter(front)...), newBody...), nil
}

// split peels the "---\n…---\n" frontmatter off the TLV body, mirroring the
// offsets persistence.parseFrontmatter uses.
func split(raw []byte) (front, body []byte, err error) {
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		return nil, nil, fmt.Errorf("missing frontmatter")
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return nil, nil, fmt.Errorf("unterminated frontmatter")
	}
	// "---\n" + frontmatter + "\n---\n"
	front = []byte(s[:end+9])
	body = []byte(s[end+9:])
	return front, body, nil
}

// upgradeFrontmatter sets message_version to 14, adding the key if absent.
func upgradeFrontmatter(front []byte) []byte {
	s := string(front)
	if i := strings.Index(s, "message_version:"); i >= 0 {
		lineEnd := strings.IndexByte(s[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(s) - i
		}
		return []byte(s[:i] + "message_version: 14" + s[i+lineEnd:])
	}
	close := strings.LastIndex(s, "---")
	return []byte(s[:close] + "message_version: 14\n" + s[close:])
}

// upgradeBody rewrites every TLV frame in the body.
func upgradeBody(body []byte) ([]byte, error) {
	var out strings.Builder
	pos := 0
	for pos < len(body) {
		for pos < len(body) && (body[pos] == '\n' || body[pos] == '\r' || body[pos] == ' ' || body[pos] == '\t') {
			pos++
		}
		if pos >= len(body) {
			break
		}
		if pos+6 > len(body) {
			return nil, fmt.Errorf("truncated frame header at byte %d", pos)
		}
		tag := string(body[pos : pos+2])
		length := int(binary.BigEndian.Uint32(body[pos+2 : pos+6]))
		start := pos + 6
		if start+length > len(body) {
			return nil, fmt.Errorf("frame %q at byte %d overruns the body", tag, pos)
		}
		value := string(body[start : start+length])
		pos = start + length

		newTag, newValue, err := convert(tag, value)
		if err != nil {
			return nil, err
		}
		encoded, err := tlv.EncodeTLV(newTag, newValue)
		if err != nil {
			return nil, err
		}
		out.WriteString("\n\n")
		out.Write(encoded)
	}
	return []byte(out.String()), nil
}

func convert(tag, value string) (string, string, error) {
	switch tag {
	case "UT", "UI", "UV", "UA", "UD", "AT", "AR":
		return convertContent(tag, value)
	case "AF":
		return convertToolInput(value)
	case "UF":
		return convertToolOutput(value)
	}
	return "", "", fmt.Errorf("unknown v13 tag %q", tag)
}

func convertContent(tag, value string) (string, string, error) {
	wireTag := tlv.TagUserX
	kind := protocol.TypeText
	switch tag {
	case "UI":
		kind = protocol.TypeImage
	case "UV":
		kind = protocol.TypeVideo
	case "UA":
		kind = protocol.TypeAudio
	case "UD":
		kind = protocol.TypeDocument
	case "AT":
		wireTag = tlv.TagAssistantX
	case "AR":
		wireTag = tlv.TagAssistantX
		kind = protocol.TypeReasoning
	}
	item := protocol.ContentItem{Type: kind}
	if kind == protocol.TypeText || kind == protocol.TypeReasoning {
		item.Text = value
	} else {
		item.URI = value
	}
	body, err := json.Marshal(protocol.ContentData{ContentItem: item})
	if err != nil {
		return "", "", err
	}
	return wireTag, string(body), nil
}

func convertToolInput(value string) (string, string, error) {
	var old struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", fmt.Errorf("AF frame: %w", err)
	}
	body, err := json.Marshal(protocol.ToolInputData{CallID: old.ID, Name: old.Name, Input: old.Input})
	if err != nil {
		return "", "", err
	}
	return tlv.TagAssistantF, string(body), nil
}

func convertToolOutput(value string) (string, string, error) {
	var old struct {
		ID      string                 `json:"id"`
		Output  []protocol.ContentItem `json:"output"`
		IsError bool                   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", fmt.Errorf("UF frame: %w", err)
	}
	body, err := json.Marshal(protocol.ToolOutputData{CallID: old.ID, Output: old.Output, IsError: old.IsError})
	if err != nil {
		return "", "", err
	}
	return tlv.TagUserF, string(body), nil
}
