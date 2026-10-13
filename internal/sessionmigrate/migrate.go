// Package sessionmigrate upgrades an old session file to the current wire format.
//
// The core does not read an older file any more — loading demands an exact
// message_version match — so an upgrade needs its own copy of the old formats.
// This package is that copy: it reads the length-prefixed framing that v13 and
// v14 both used (a 2-byte tag, a 4-byte big-endian length, and the value) and
// writes the current one, one frame per line. It is a one-off tool: nothing in
// the running program imports it (only misc/session_upgrade_v15.go and the
// migration test do).
//
// The two old generations differ in more than framing. A v13 frame's tags and
// value shapes are a generation away from the current vocabulary, so it is
// converted as it is read. A v14 frame's values are already the ones this version
// carries, so it only needs re-framing. Either way every value is re-marshaled
// through encoding/json's compactor, which is what guarantees a converted file
// has one frame per line — and is why a hand-edited file with an indented value
// converts instead of producing a file no reader accepts.
//
// Old files carry no per-message time, so converted v13 parts carry none either.
// The adapter stamps those with its receipt clock, exactly as v13 did. They carry
// no per-video fps/resolution either, so a migrated video serializes with the
// provider default (2 / "default"), not the session's :video_config — the old
// file never recorded a per-video setting, and "unset" is the provider default.
// It therefore does not follow later :video_config changes, which is what keeps
// history immutable.
package sessionmigrate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

const (
	version13 = 13
	version14 = 14
	// targetVersion is the format this package writes. It must equal
	// internal/agent's messageVersion: a file this tool produces has to load, and
	// loading demands an exact match.
	targetVersion = 15
)

// Migrate rewrites a v13 or v14 session file (frontmatter + length-prefixed body)
// in the current wire format. A file already at the current version is returned
// unchanged.
func Migrate(raw []byte) ([]byte, error) {
	front, body, err := split(raw)
	if err != nil {
		return nil, err
	}
	from, err := versionOf(front)
	if err != nil {
		return nil, err
	}
	if from == targetVersion {
		return raw, nil
	}

	// convert is nil for a v14 body: its frames only need re-framing, since its
	// tags and value shapes are already the current ones.
	var convert func(tag, value string) (string, string, error)
	switch from {
	case version13:
		convert = convertV13
	case version14:
	default:
		return nil, fmt.Errorf("cannot upgrade a message_version %d file", from)
	}

	newBody, err := reframe(body, convert)
	if err != nil {
		return nil, err
	}
	return append(upgradeFrontmatter(front), newBody...), nil
}

// split peels the "---\n…---\n" frontmatter off the body, mirroring the offsets
// persistence.parseFrontmatter uses.
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
	return []byte(s[:end+9]), []byte(s[end+9:]), nil
}

// versionOf reads message_version out of the frontmatter.
func versionOf(front []byte) (int, error) {
	for _, line := range strings.Split(string(front), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "message_version:")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("message_version: %w", err)
		}
		return n, nil
	}
	return 0, fmt.Errorf("frontmatter has no message_version")
}

// upgradeFrontmatter sets message_version to the target, adding the key if absent.
func upgradeFrontmatter(front []byte) []byte {
	s := string(front)
	target := "message_version: " + strconv.Itoa(targetVersion)
	if i := strings.Index(s, "message_version:"); i >= 0 {
		lineEnd := strings.IndexByte(s[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(s) - i
		}
		return []byte(s[:i] + target + s[i+lineEnd:])
	}
	closing := strings.LastIndex(s, "---")
	return []byte(s[:closing] + target + "\n" + s[closing:])
}

// reframe rewrites the length-prefixed body as one frame per line. convert, when
// non-nil, rewrites each frame's tag and value as it goes (the v13 path); a value
// is compacted either way, so what comes out is one line whatever the input held.
func reframe(body []byte, convert func(tag, value string) (string, string, error)) ([]byte, error) {
	var out bytes.Buffer
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

		if convert != nil {
			newTag, newValue, err := convert(tag, value)
			if err != nil {
				return nil, fmt.Errorf("frame %q at byte %d: %w", tag, pos, err)
			}
			tag, value = newTag, newValue
		}

		compacted, err := compactValue(tag, value)
		if err != nil {
			return nil, fmt.Errorf("frame %q at byte %d: %w", tag, pos, err)
		}
		frame, err := wire.Encode(tag, compacted)
		if err != nil {
			return nil, fmt.Errorf("frame %q at byte %d: %w", tag, pos, err)
		}
		out.Write(frame)
	}
	return out.Bytes(), nil
}

// compactValue elides the insignificant whitespace in a value, so the line it
// becomes is one line. Key order is preserved, which matters: a sample or a
// fixture that a test compares against a re-marshaled struct still matches.
//
// A control frame (UE, CE) carries no value at all, and is passed through.
func compactValue(tag, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(value)); err != nil {
		return "", fmt.Errorf("value of %s is not JSON: %w", tag, err)
	}
	return compact.String(), nil
}

// convertV13 maps a v13 frame's tag and value onto the current vocabulary.
func convertV13(tag, value string) (string, string, error) {
	switch tag {
	case "UT", "UI", "UV", "UA", "UD", "AT", "AR":
		return convertV13Content(tag, value)
	case "AF":
		return convertV13ToolInput(value)
	case "UF":
		return convertV13ToolOutput(value)
	}
	return "", "", fmt.Errorf("unknown v13 tag %q", tag)
}

func convertV13Content(tag, value string) (string, string, error) {
	wireTag := wire.TagUserX
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
		wireTag = wire.TagAssistantX
	case "AR":
		wireTag = wire.TagAssistantX
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

func convertV13ToolInput(value string) (string, string, error) {
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
	return wire.TagAssistantF, string(body), nil
}

func convertV13ToolOutput(value string) (string, string, error) {
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
	return wire.TagUserF, string(body), nil
}
