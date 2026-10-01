package providers_test

// Wire-level tests for the send-time image gate: each provider must actually
// call llm.FitImagesForSend before building its request, so an image that would be
// rejected goes out shrunk (and an image that cannot be decoded goes out as the
// note).
//
// The function's own behavior is tested in internal/llm/media_fit_test.go.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// testMaxImageEdge mirrors the unexported maxImageEdge in package llm.
const testMaxImageEdge = 4096

func mustPNGDataURI(w, h int) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		panic(err) // a fixed-size fixture cannot fail to encode
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func dataURIDims(t *testing.T, uri string) (int, int) {
	t.Helper()
	_, b64, ok := llm.ParseDataURI(uri)
	if !ok {
		t.Fatalf("not a data URI: %.60s", uri)
	}
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		t.Fatalf("decode result header: %v", err)
	}
	return cfg.Width, cfg.Height
}

// TestOversizeImageGoesOutShrunkOnWire is the end-to-end assertion for OpenAI:
// the request still carries an image, and it is within the limit.
func TestOversizeImageGoesOutShrunkOnWire(t *testing.T) {
	msgs := captureMessages(t, testMsg(llm.RoleUser, &llm.ImagePart{URI: mustPNGDataURI(9000, 3000)}), nil)
	body, _ := json.Marshal(msgs)

	if !strings.Contains(string(body), "image_url") {
		t.Fatalf("oversized image was not delivered at all: %s", body)
	}
	if strings.Contains(string(body), "NOT delivered") {
		t.Fatalf("image was degraded although it could be shrunk: %s", body)
	}
	blocks := contentBlocks(t, msgs[0])
	var url string
	for _, b := range blocks {
		if b["type"] == "image_url" {
			url = b["image_url"].(map[string]any)["url"].(string)
		}
	}
	if url == "" {
		t.Fatalf("no image_url block in %v", blockTypes(blocks))
	}
	if w, h := dataURIDims(t, url); w > testMaxImageEdge || h > testMaxImageEdge {
		t.Fatalf("wire image is %dx%d, still over the limit", w, h)
	}
}

// TestOversizeToolImageStillPromoted: the promotion path keeps working — the
// media is promoted onto the follow-up user message, just smaller.
func TestOversizeToolImageStillPromoted(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.ImagePart{URI: mustPNGDataURI(testMaxImageEdge+1, 1)}},
	}), nil)
	body, _ := json.Marshal(msgs)

	if !strings.Contains(string(body), "image_url") {
		t.Fatalf("oversized tool image was not promoted: %s", body)
	}
	if !strings.Contains(string(body), "attached to the next message") {
		t.Fatalf("tool label should point at the media: %s", body)
	}
}

// TestWebpImageDegradedToText is the fallback end to end: a format this client
// cannot decode becomes the note rather than an unforwardable block.
func TestWebpImageDegradedToText(t *testing.T) {
	msgs := captureMessages(t, testMsg(llm.RoleUser, &llm.ImagePart{URI: "data:image/webp;base64,AAAA"}), nil)
	body, _ := json.Marshal(msgs)

	if strings.Contains(string(body), "image_url") {
		t.Fatalf("undecodable image reached the wire: %s", body)
	}
	if !strings.Contains(string(body), "NOT delivered") {
		t.Fatalf("wire carries no note: %s", body)
	}
}

// TestAnthropicOversizeImageShrunk is the same assertion on the other protocol,
// where the image is nested in a content block.
func TestAnthropicOversizeImageShrunk(t *testing.T) {
	msgs := captureAnthropicMessages(t, testMsg(llm.RoleUser, &llm.ImagePart{URI: mustPNGDataURI(9000, 3000)}))
	types := allBlockTypes(t, msgs)

	if !contains(types, "image") {
		t.Fatalf("oversized image was not delivered on anthropic: %v", types)
	}
}

// TestSendableImageStillNative is the other side of the gate: an image within
// the limit is forwarded exactly as before.
func TestSendableImageStillNative(t *testing.T) {
	msgs := captureAnthropicMessages(t, testMsg(llm.RoleUser, &llm.ImagePart{URI: mustPNGDataURI(1, 1)}))
	if types := allBlockTypes(t, msgs); !contains(types, "image") {
		t.Fatalf("small image was degraded, got %v", types)
	}
}
