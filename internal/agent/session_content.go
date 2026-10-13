package agent

// ContentPart ↔ wire content-item conversion.
//
// A "content item" is the JSON payload shared by a UX/AX frame and by each
// element of a tool result's output (see protocol.ContentItem). It is the one
// shape that describes a piece of content; this file is the only place it is
// built from, and read back into, the domain ContentPart types.

import (
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
)

// contentItemFromPart maps a domain ContentPart to its wire content item.
func contentItemFromPart(part llm.ContentPart) (protocol.ContentItem, error) {
	switch v := part.(type) {
	case *llm.TextPart:
		return protocol.ContentItem{Type: protocol.TypeText, Text: v.Text}, nil
	case *llm.ImagePart:
		return protocol.ContentItem{Type: protocol.TypeImage, URI: v.URI}, nil
	case *llm.VideoPart:
		return protocol.ContentItem{Type: protocol.TypeVideo, URI: v.URI, FPS: v.FPS, Res: v.Res}, nil
	case *llm.AudioPart:
		return protocol.ContentItem{Type: protocol.TypeAudio, URI: v.URI}, nil
	case *llm.DocumentPart:
		return protocol.ContentItem{Type: protocol.TypeDocument, URI: v.URI}, nil
	}
	return protocol.ContentItem{}, fmt.Errorf("unsupported content part type: %T", part)
}

// partFromContentItem is the inverse of contentItemFromPart. The returned part
// has no role or time set; the caller stamps those.
func partFromContentItem(item protocol.ContentItem) (llm.ContentPart, error) {
	switch item.Type {
	case protocol.TypeText:
		return &llm.TextPart{Text: item.Text}, nil
	case protocol.TypeReasoning:
		return &llm.ReasoningPart{Text: item.Text}, nil
	case protocol.TypeImage:
		return &llm.ImagePart{URI: item.URI}, nil
	case protocol.TypeVideo:
		return &llm.VideoPart{URI: item.URI, FPS: item.FPS, Res: item.Res}, nil
	case protocol.TypeAudio:
		return &llm.AudioPart{URI: item.URI}, nil
	case protocol.TypeDocument:
		return &llm.DocumentPart{URI: item.URI}, nil
	}
	return nil, fmt.Errorf("unknown content item type: %s", item.Type)
}

// contentItemsFromParts converts a list of parts to wire items.
func contentItemsFromParts(parts []llm.ContentPart) ([]protocol.ContentItem, error) {
	items := make([]protocol.ContentItem, 0, len(parts))
	for _, p := range parts {
		item, err := contentItemFromPart(p)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// partsFromContentItems converts wire items back to domain parts. Nested items
// are tool-result content, which is model *input*: a kind that is not
// intoModelKind (reasoning, or anything unknown) is rejected here for the same
// reason contentPartFromJSON rejects it under UX — a tool result may not carry
// assistant-only content, and the writer cannot produce any (contentItemFromPart
// errors on a ReasoningPart).
func partsFromContentItems(items []protocol.ContentItem) ([]llm.ContentPart, error) {
	parts := make([]llm.ContentPart, 0, len(items))
	for _, item := range items {
		p, err := partFromContentItem(item)
		if err != nil {
			return nil, err
		}
		if !intoModelKind(item.Type) {
			return nil, fmt.Errorf("content type %q cannot be tool-result content", item.Type)
		}
		parts = append(parts, p)
	}
	return parts, nil
}
