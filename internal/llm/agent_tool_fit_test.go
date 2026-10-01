package llm

// Tests for the tool-output ingest hook in agent_execution.go. newToolOutput
// builds the one value that becomes both the UF frame the adapter sees and the
// part stored in history, so the fit has to run there — once, and before either
// of them is derived from it.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
)

func fitFixturePNG(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func fitFixtureDims(t *testing.T, uri string) (int, int) {
	t.Helper()
	_, b64, ok := ParseDataURI(uri)
	if !ok {
		t.Fatalf("not a data URI: %.60s", uri)
	}
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	return cfg.Width, cfg.Height
}

// TestNewToolOutputFitsOversizeImages pins the ingest hook for tool output.
//
// It asserts the part that is STORED, not the one on the wire: deleting the
// hook would still pass every wire test, because the send-time gate covers the
// request anyway. What it would hide is silent — the shrink would simply be
// paid on every send instead of once, which is exactly the regression the
// ingest hook exists to prevent.
func TestNewToolOutputFitsOversizeImages(t *testing.T) {
	oversize := fitFixturePNG(t, maxImageEdge+1, 3)

	var seen []ContentPart
	callbacks := StreamCallbacks{
		OnToolOutput: func(_ string, contents []ContentPart, _ error, _ uint64) error {
			seen = contents
			return nil
		},
	}
	got := newToolOutput(callbacks, "call-1", []ContentPart{&ImagePart{URI: oversize}}, nil, 7)

	img, ok := got.Output[0].(*ImagePart)
	if !ok {
		t.Fatalf("stored part = %T, want *ImagePart", got.Output[0])
	}
	if img.URI == oversize {
		t.Fatal("the oversized image was stored as-is — the ingest hook is gone")
	}
	if w, h := fitFixtureDims(t, img.URI); w > maxImageEdge || h > maxImageEdge {
		t.Fatalf("stored image is %dx%d, still over the limit", w, h)
	}
	// The frame the adapter receives and the part that is stored must be the
	// same value, or the design's whole claim ("everyone sees the same part")
	// is false.
	if len(seen) != 1 || seen[0] != got.Output[0] {
		t.Fatalf("the echoed frame and the stored part disagree: %#v vs %#v", seen, got.Output)
	}
}

// TestNewToolOutputLeavesUndecodableImageForTheSendGate pins the boundary
// between the two forms. Ingest shrinks; it does not substitute. An image this
// client cannot decode is stored as itself, because a stored note would replace
// the attachment in the user's own session and — since the echo uses the fitted
// part — come back to the adapter as user text instead of a media label. The
// send gate applies the note per request, where nothing is written down.
func TestNewToolOutputLeavesUndecodableImageForTheSendGate(t *testing.T) {
	webp := &ImagePart{URI: "data:image/webp;base64,AAAA"}
	got := newToolOutput(StreamCallbacks{}, "call-1", []ContentPart{webp}, nil, 7)

	if got.Output[0] != ContentPart(webp) {
		t.Fatalf("stored part = %#v, want the original *ImagePart untouched", got.Output[0])
	}
}

// TestFitWithinNeverEnlarges pins the rescaler's one hard invariant: the result
// never has more pixels than the source. The shrinkDataURI caller already
// returns early for an image inside the limit, so the guard inside fitWithin is
// unreachable from there — this test is what keeps it from being dead code a
// later caller silently trips over.
func TestFitWithinNeverEnlarges(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{8192, 2048, 4096, 1024},
		{2048, 8192, 1024, 4096},
		{100, 50, 100, 50}, // already inside the limit: unchanged, never enlarged
		{4096, 4096, 4096, 4096},
		{4097, 1, 4096, 1},
		{1, 4097, 1, 4096},
		{5000, 4999, 4096, 4095},
	}
	for _, tc := range cases {
		gotW, gotH := fitWithin(tc.w, tc.h, maxImageEdge)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("fitWithin(%d,%d) = %d,%d, want %d,%d", tc.w, tc.h, gotW, gotH, tc.wantW, tc.wantH)
		}
		if gotW > tc.w || gotH > tc.h {
			t.Errorf("fitWithin(%d,%d) enlarged the image to %d,%d", tc.w, tc.h, gotW, gotH)
		}
		if gotW > maxImageEdge || gotH > maxImageEdge {
			t.Errorf("fitWithin(%d,%d) = %d,%d exceeds the limit %d", tc.w, tc.h, gotW, gotH, maxImageEdge)
		}
	}
}

// TestUnsendableImageNoteMatchesDocs pins the full note sentence.
//
// docs/providers.md quotes this string verbatim as the block an endpoint
// receives for an image this client cannot read. Without a pin, rewording the
// message would leave the docs describing bytes no code ever produces — the
// same rule TestAnthropicPlaceholderTextMatchesDocs applies to the audio/video
// placeholder. If this fails, fix both.
func TestUnsendableImageNoteMatchesDocs(t *testing.T) {
	got := unsendableImageNote("data:image/webp;base64,AAAA")

	const documented = "[Unreadable image (image/webp): this client cannot read its dimensions " +
		"(unsupported image format), so the content was NOT delivered to you and you have not " +
		"perceived it. Do not describe or quote it. To inspect it, convert it to JPEG or PNG at " +
		"4096 px or less (e.g. with an image tool via execute_command), then read or attach it again.]"
	if got != documented {
		t.Errorf("note drifted from the text quoted in docs/providers.md:\n got: %v\nwant: %s", got, documented)
	}
}
