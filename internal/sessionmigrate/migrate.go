// Package sessionmigrate upgrades a session file one wire generation at a time:
// V13ToV14 for the content-item change, V14ToV15 for the move to one frame per
// line.
//
// The core reads none of the older generations — loading demands an exact
// message_version match — so an upgrade needs its own copy of the old framing.
// v13 and v14 share it (a 2-byte tag, a 4-byte big-endian length, and the value),
// and this package carries that reader, plus a writer for it, because nothing in
// the running program reads or writes either any more.
//
// The two steps stay separate on purpose: each is a generation, a file may be one
// or two generations behind, and chaining them would hide which conversion a file
// actually needed. misc/session_upgrade_v14.sh performs the first and
// misc/session_upgrade_v15.sh the second; a v13 file goes through both, in that
// order.
package sessionmigrate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

const (
	version13 = 13
	version14 = 14
	// version15 is the oldest generation this package leaves alone: a file at it
	// is current. It must equal internal/agent's messageVersion, since a file
	// these tools produce has to load, and loading demands an exact match.
	version15 = 15
)

// V13ToV14 rewrites a v13 session file as v14.
//
// v14 collapsed the per-kind content tags into UX/AX with the concrete kind in a
// content item's "type", moved the history id from a NUL-delimited prefix into the
// frame's "id", and renamed the tool-call id to "call_id". Old files carry no
// per-message time, so converted parts carry none either: the adapter stamps those
// with its receipt clock, exactly as v13 did. They carry no per-video
// fps/resolution either, so a migrated video serializes with the provider default
// (2 / "default"), not the session's :video_config — the old file never recorded a
// per-video setting, and "unset" is the provider default. It therefore does not
// follow later :video_config changes, which is what keeps history immutable.
func V13ToV14(raw []byte) ([]byte, error) {
	front, body, err := split(raw)
	if err != nil {
		return nil, err
	}
	from, err := versionOf(front)
	if err != nil {
		return nil, err
	}
	if from == version14 {
		return raw, nil // already this step's output
	}
	if from != version13 {
		return nil, fmt.Errorf("this step upgrades a v13 file; this one says message_version: %d", from)
	}

	newBody, err := reframe(body, encodeV14, convertV13)
	if err != nil {
		return nil, err
	}
	return append(setVersion(front, version14), newBody...), nil
}

// V14ToV15 rewrites a v14 session file as v15: one frame per line.
//
// A v14 frame's tag and value are already the ones v15 carries, so there is no
// conversion — only re-framing, with one thing to be careful about: a v14 file
// written by hand may hold a value with line breaks in it, and a value that is not
// one line is not a frame in v15. Every value is therefore re-marshaled through
// encoding/json's compactor, which elides insignificant whitespace and keeps
// everything else — key order included, so a value that a test compares against a
// re-marshaled struct still matches. A file already at v15 is returned unchanged.
func V14ToV15(raw []byte) ([]byte, error) {
	front, body, err := split(raw)
	if err != nil {
		return nil, err
	}
	from, err := versionOf(front)
	if err != nil {
		return nil, err
	}
	switch from {
	case version15:
		return raw, nil
	case version13:
		return nil, fmt.Errorf("this step upgrades a v14 file; this one is v13 — upgrade it to v14 first with misc/session_upgrade_v14.sh")
	case version14:
	default:
		return nil, fmt.Errorf("cannot upgrade a message_version %d file", from)
	}

	newBody, err := reframe(body, wire.Encode, nil)
	if err != nil {
		return nil, err
	}
	return append(setVersion(front, version15), newBody...), nil
}

// split peels the "---\n…---\n" frontmatter off the body, mirroring the offsets
// persistence.parseFrontmatter uses.
func split(raw []byte) (front, body []byte, err error) {
	// Same tolerance the reader and the frame splitter have: a file whose line
	// endings were translated by an editor still converts.
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
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

// setVersion returns the frontmatter with message_version set, adding the key if
// it is absent.
func setVersion(front []byte, version int) []byte {
	s := string(front)
	target := "message_version: " + strconv.Itoa(version)
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

// reframe reads the body's length-prefixed frames and writes each one back with
// encode. convert, when non-nil, rewrites a frame's tag and value first (the v13
// step); either way the value is compacted, so what comes out is one line per
// frame whatever the input held.
func reframe(body []byte, encode func(tag, value string) ([]byte, error), convert func(tag, value string) (string, string, error)) ([]byte, error) {
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
		rawLength := binary.BigEndian.Uint32(body[pos+2 : pos+6])
		// Checked in 64-bit arithmetic, which is exact on both platforms: on a
		// 32-bit one, int is 32 bits, a four-byte field can exceed it, and a length
		// that came out negative would slip past an overrun check and then panic on
		// the slice below. A corrupt file has to be an error, not a crash.
		// remaining is >= 0: the header was bounds-checked above.
		remaining := len(body) - pos - 6
		if int64(rawLength) > int64(remaining) {
			return nil, fmt.Errorf("frame %q at byte %d overruns the body", tag, pos)
		}
		length := int(rawLength) //nolint:gosec // G115: bounded by remaining, checked above
		start := pos + 6
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
		frame, err := encode(tag, compacted)
		if err != nil {
			return nil, fmt.Errorf("frame %q at byte %d: %w", tag, pos, err)
		}
		out.Write(frame)
	}
	return out.Bytes(), nil
}

// encodeV14 writes a frame in the v14 framing: a 2-byte tag, a 4-byte big-endian
// length, and the value. This package carries it because internal/wire writes the
// v15 framing now — one frame per line — and the v13 step's output has to be a v14
// file, which is the input the v14 step expects.
func encodeV14(tag, value string) ([]byte, error) {
	if len(tag) != 2 {
		return nil, fmt.Errorf("tag %q is not two bytes", tag)
	}
	// The length field is four bytes, so a value that does not fit in one cannot be
	// framed this way. Nothing in a session file comes close (the current bound is
	// 64MB), but the conversion has to be a total function, and a wrapped length
	// would produce a file that reads as something else entirely.
	if len(value) > math.MaxUint32 {
		return nil, fmt.Errorf("%s frame's value is %d bytes, too long for the v14 length field", tag, len(value))
	}
	frame := make([]byte, 6+len(value))
	copy(frame, tag)
	binary.BigEndian.PutUint32(frame[2:6], uint32(len(value))) //nolint:gosec // G115: bounded by the check above
	copy(frame[6:], value)
	return frame, nil
}

// compactValue elides the insignificant whitespace in a value, so the line it
// becomes is one line. Key order is preserved, which matters: a value that a test
// compares against a re-marshaled struct still matches.
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

// convertV13 maps a v13 frame's tag and value onto the v14 (and current)
// vocabulary.
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
