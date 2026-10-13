package terminal

import (
	"encoding/json"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// Display tags — the terminal's internal window vocabulary.
//
// The wire collapsed the per-kind content tags into UX/AX (content) with the
// concrete kind carried as a content item's "type" field. The renderers,
// windows and buffers below still dispatch on a single string per window kind;
// this is that vocabulary. The frame parser maps (wire tag, type) → display tag
// once, at ingest, so nothing below the parser deals in wire tags for content.
//
// The spellings are the retired wire tags on purpose: they are opaque keys
// here, and keeping them means every comparison below reads as it did before.
const (
	tagUserText  = "UT"
	tagUserImage = "UI"
	tagUserVideo = "UV"
	tagUserAudio = "UA"
	tagUserDoc   = "UD"

	tagAssistantText      = "AT"
	tagAssistantReasoning = "AR"
)

// contentDisplayTag maps a content frame (wire tag + content item kind) to the
// terminal's display tag. ok is false for a kind this adapter does not render.
func contentDisplayTag(wireTag, kind string) (string, bool) {
	switch wireTag {
	case tlv.TagUserX:
		switch kind {
		case protocol.TypeText:
			return tagUserText, true
		case protocol.TypeImage:
			return tagUserImage, true
		case protocol.TypeVideo:
			return tagUserVideo, true
		case protocol.TypeAudio:
			return tagUserAudio, true
		case protocol.TypeDocument:
			return tagUserDoc, true
		}
	case tlv.TagAssistantX:
		switch kind {
		case protocol.TypeText:
			return tagAssistantText, true
		case protocol.TypeReasoning:
			return tagAssistantReasoning, true
		}
	}
	return "", false
}

// mediaLabel returns the display label for a user-media display tag.
func mediaLabel(displayTag string) string {
	switch displayTag {
	case tagUserImage:
		return tlv.MediaLabel(tlv.KindImage)
	case tagUserVideo:
		return tlv.MediaLabel(tlv.KindVideo)
	case tagUserAudio:
		return tlv.MediaLabel(tlv.KindAudio)
	case tagUserDoc:
		return tlv.MediaLabel(tlv.KindDocument)
	}
	return ""
}

// decodeContent parses a content-frame body. ok is false on malformed JSON; the
// frame is then skipped rather than trusted.
func decodeContent(value string) (protocol.ContentData, bool) {
	var d protocol.ContentData
	if err := json.Unmarshal([]byte(value), &d); err != nil {
		return protocol.ContentData{}, false
	}
	return d, true
}
