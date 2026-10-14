package terminal

import (
	"encoding/json"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
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
// terminal's display tag. ok is false for a plane this adapter does not ingest
// or a kind that plane does not admit.
//
// Which kinds a plane admits is the protocol's call (protocol.KindAllowed), not
// this adapter's; the table below only names how each admitted kind is drawn. A
// kind admitted there but absent here would be a silent drop at ingest, so
// content_tags_drift_test.go pins the two together.
func contentDisplayTag(wireTag, kind string) (string, bool) {
	plane, ok := contentPlane(wireTag)
	if !ok || !protocol.KindAllowed(plane, kind) {
		return "", false
	}
	tag, ok := contentDisplayTagFor[plane][kind]
	return tag, ok
}

// contentPlane maps a content frame's wire tag to the plane it carries.
func contentPlane(wireTag string) (protocol.Plane, bool) {
	switch wireTag {
	case wire.TagUserX:
		return protocol.PlaneUser, true
	case wire.TagAssistantX:
		return protocol.PlaneAssistant, true
	}
	return "", false
}

// contentDisplayTagFor is this adapter's rendering vocabulary: for each plane it
// draws, the display tag each admitted kind renders as. Kept as data so that
// contentDisplayTag and its inverse displayTagFrame cannot disagree about a
// kind.
var contentDisplayTagFor = map[protocol.Plane]map[string]string{
	protocol.PlaneUser: {
		protocol.TypeText:     tagUserText,
		protocol.TypeImage:    tagUserImage,
		protocol.TypeVideo:    tagUserVideo,
		protocol.TypeAudio:    tagUserAudio,
		protocol.TypeDocument: tagUserDoc,
	},
	protocol.PlaneAssistant: {
		protocol.TypeText:      tagAssistantText,
		protocol.TypeReasoning: tagAssistantReasoning,
	},
}

// displayTagFrame maps a display tag back to the wire tag and content kind it
// travels as: the inverse of contentDisplayTag, and the vocabulary's other
// direction. Tests name a window by the tag the renderer uses, so feeding one a
// frame the way a session would needs this mapping rather than a second copy of
// it.
func displayTagFrame(displayTag string) (wireTag, kind string, ok bool) {
	switch displayTag {
	case tagUserText:
		return wire.TagUserX, protocol.TypeText, true
	case tagUserImage:
		return wire.TagUserX, protocol.TypeImage, true
	case tagUserVideo:
		return wire.TagUserX, protocol.TypeVideo, true
	case tagUserAudio:
		return wire.TagUserX, protocol.TypeAudio, true
	case tagUserDoc:
		return wire.TagUserX, protocol.TypeDocument, true
	case tagAssistantText:
		return wire.TagAssistantX, protocol.TypeText, true
	case tagAssistantReasoning:
		return wire.TagAssistantX, protocol.TypeReasoning, true
	}
	return "", "", false
}

// mediaLabel returns the display label for a user-media display tag.
func mediaLabel(displayTag string) string {
	switch displayTag {
	case tagUserImage:
		return wire.MediaLabel(wire.KindImage)
	case tagUserVideo:
		return wire.MediaLabel(wire.KindVideo)
	case tagUserAudio:
		return wire.MediaLabel(wire.KindAudio)
	case tagUserDoc:
		return wire.MediaLabel(wire.KindDocument)
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
