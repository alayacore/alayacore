package providers_test

// The media URI gate, on the user-message path.
//
// openaiPromotableURI has guarded media out of a *tool result* since promotion
// existed, but the regular converter built a native block from any URI it was
// handed. A bare path, a file: URI or an empty string then went out as
// image_url.url "/tmp/x.png" (or a video/document url source), which does not
// fail one block — the server rejects the whole request, and because the part is
// persisted history, every later task in the session fails the same way.
//
// These tests pin the gate on the regular path for both providers, and pin that
// a data: or http(s): URI still becomes a native block.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestOpenAIRegularMediaDropsUnfetchableURI(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
		want string // the text placeholder that must stand in
	}{
		{"empty image uri", &llm.ImagePart{URI: ""}, "[Image: no URI]"},
		{"bare image path", &llm.ImagePart{URI: "/tmp/diagram.png"}, "[Image: /tmp/diagram.png]"},
		{"file image uri", &llm.ImagePart{URI: "file:///tmp/diagram.png"}, "[Image: file:///tmp/diagram.png]"},
		{"empty video uri", &llm.VideoPart{URI: ""}, "[Video: no URI]"},
		{"bare video path", &llm.VideoPart{URI: "clip.mp4"}, "[Video: clip.mp4]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := captureMessages(t, testMsg(llm.RoleUser, tc.part))
			body, _ := json.Marshal(msgs)

			for _, bad := range []string{"image_url", "video_url", "input_audio"} {
				if strings.Contains(string(body), bad) {
					t.Errorf("request carries %s for an unfetchable URI: %s", bad, body)
				}
			}
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("dropped media left no placeholder %q: %s", tc.want, body)
			}
		})
	}
}

func TestOpenAIRegularMediaKeepsFetchableURI(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{"data image", &llm.ImagePart{URI: testImageURI}, "image_url"},
		{"remote image", &llm.ImagePart{URI: "https://example.com/a.png"}, "image_url"},
		{"data video", &llm.VideoPart{URI: testVideoURI}, "video_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := captureMessages(t, testMsg(llm.RoleUser, tc.part))
			body, _ := json.Marshal(msgs)
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("fetchable URI was not delivered as %s: %s", tc.want, body)
			}
			if strings.Contains(string(body), "NOT delivered") {
				t.Errorf("fetchable URI was degraded: %s", body)
			}
		})
	}
}

func TestAnthropicRegularMediaDropsUnfetchableURI(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
	}{
		{"empty image uri", &llm.ImagePart{URI: ""}},
		{"bare image path", &llm.ImagePart{URI: "/tmp/diagram.png"}},
		{"file image uri", &llm.ImagePart{URI: "file:///tmp/diagram.png"}},
		{"empty document uri", &llm.DocumentPart{URI: ""}},
		{"bare document path", &llm.DocumentPart{URI: "/tmp/doc.pdf"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := captureAnthropicMessages(t, testMsg(llm.RoleUser, tc.part))
			types := allBlockTypes(t, msgs)
			for _, bad := range []string{"image", "document"} {
				if contains(types, bad) {
					t.Errorf("request carries a native %s block for an unfetchable URI: %v", bad, types)
				}
			}
			body, _ := json.Marshal(msgs)
			for _, want := range []string{"NOT delivered", "Do not describe or quote it"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("dropped media placeholder missing %q: %s", want, body)
				}
			}
		})
	}
}

func TestAnthropicRegularMediaKeepsFetchableURI(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{"data image", &llm.ImagePart{URI: mustPNGDataURI(1, 1)}, "image"},
		{"remote image", &llm.ImagePart{URI: "https://example.com/a.png"}, "image"},
		{"data document", &llm.DocumentPart{URI: "data:application/pdf;base64,JVBERi0="}, "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := captureAnthropicMessages(t, testMsg(llm.RoleUser, tc.part))
			if types := allBlockTypes(t, msgs); !contains(types, tc.want) {
				t.Errorf("fetchable URI did not stay a native %s block: %v", tc.want, types)
			}
		})
	}
}
