package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// These tests cover media promotion out of OpenAI tool results: a `tool`
// message can only carry a string, so media returned by a tool is moved onto a
// follow-up `user` message that accepts a content array. Promotion is the only
// way a model on this protocol can actually see a multimodal file it asked a
// tool to read.

const (
	testImageURI = "data:image/png;base64,iVBORw0KGgo="
	testVideoURI = "data:video/mp4;base64,AAAA"
	testWavURI   = "data:audio/wav;base64,UklGRiQ="
	testPDFURI   = "data:application/pdf;base64,JVBERi0="
)

// captureMessages runs a provider against a stub server and returns the
// wire-format messages it received, so assertions are made on the real JSON
// body rather than on internal Go structures.
func captureMessages(t *testing.T, contents []llm.ContentPart, configure func(*providers.OpenAIProvider)) []map[string]any {
	t.Helper()

	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		captured = reqBody.Messages

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	p, err := providers.NewOpenAI(providers.BaseConfig{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(p)
	}

	events, err := p.StreamMessages(context.Background(), contents, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	return captured
}

// roles returns the role of each message, for order assertions.
func roles(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = fmt.Sprint(m["role"])
	}
	return out
}

// contentBlocks returns a message's content as block maps, or nil when the
// content is a plain string (which is what a tool message must be).
func contentBlocks(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	arr, ok := msg["content"].([]any)
	if !ok {
		return nil
	}
	blocks := make([]map[string]any, 0, len(arr))
	for _, b := range arr {
		bm, ok := b.(map[string]any)
		if !ok {
			t.Fatalf("content block is %T, want object", b)
		}
		blocks = append(blocks, bm)
	}
	return blocks
}

func blockTypes(blocks []map[string]any) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = fmt.Sprint(b["type"])
	}
	return out
}

func toolRound(toolOutputs ...*llm.ToolOutputPart) []llm.ContentPart {
	inputs := make([]llm.ContentPart, 0, len(toolOutputs))
	for _, o := range toolOutputs {
		inputs = append(inputs, &llm.ToolInputPart{ID: o.ID, Name: "test_tool", Input: json.RawMessage(`{}`)})
	}
	contents := testMsg(llm.RoleUser, &llm.TextPart{Text: "read it"})
	contents = append(contents, testMsg(llm.RoleAssistant, inputs...)...)
	contents = append(contents, testMsg(llm.RoleTool, toParts(toolOutputs)...)...)
	return contents
}

func toParts(outputs []*llm.ToolOutputPart) []llm.ContentPart {
	parts := make([]llm.ContentPart, 0, len(outputs))
	for _, o := range outputs {
		parts = append(parts, o)
	}
	return parts
}

// TestOpenAIToolResultImageIsPromoted locks the core capability: an image
// returned by a tool reaches the model as pixels, on a user message that comes
// AFTER the tool message.
func TestOpenAIToolResultImageIsPromoted(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.TextPart{Text: "Read a.png (1.2KB, image/png)"}, &llm.ImagePart{URI: testImageURI}},
	}), nil)

	if got, want := roles(msgs), []string{"user", "assistant", "tool", "user"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v (media must be promoted after every tool message)", got, want)
	}

	// The tool message stays a plain string and points forward, so the model
	// does not read "[Image (image/png)]" as "pixels unavailable".
	toolContent, ok := msgs[2]["content"].(string)
	if !ok {
		t.Fatalf("tool content = %T, want string", msgs[2]["content"])
	}
	for _, want := range []string{`"status":"success"`, "[Image (image/png)]", "attached to the next message"} {
		if !strings.Contains(toolContent, want) {
			t.Errorf("tool content missing %q: %s", want, toolContent)
		}
	}

	promoted := contentBlocks(t, msgs[3])
	if got, want := blockTypes(promoted), []string{"text", "image_url"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("promoted block types = %v, want %v", got, want)
	}
	if !strings.Contains(promoted[0]["text"].(string), "call-1") {
		t.Errorf("promoted intro missing tool_call id: %s", promoted[0]["text"])
	}
	img, ok := promoted[1]["image_url"].(map[string]any)
	if !ok || img["url"] != testImageURI {
		t.Errorf("promoted image_url = %v, want url %s", promoted[1]["image_url"], testImageURI)
	}
}

// TestOpenAIToolMediaAggregatedIntoOneMessage checks that a parallel batch
// produces exactly one promoted message, and that it names only the calls that
// actually returned media.
func TestOpenAIToolMediaAggregatedIntoOneMessage(t *testing.T) {
	msgs := captureMessages(t, toolRound(
		&llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{&llm.ImagePart{URI: testImageURI}}},
		&llm.ToolOutputPart{ID: "call-2", Output: []llm.ContentPart{&llm.TextPart{Text: "plain result"}}},
		&llm.ToolOutputPart{ID: "call-3", Output: []llm.ContentPart{&llm.VideoPart{URI: testVideoURI}}},
	), nil)

	if got, want := roles(msgs), []string{"user", "assistant", "tool", "tool", "tool", "user"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v (one promoted message, after all tool messages)", got, want)
	}

	promoted := contentBlocks(t, msgs[5])
	if got, want := blockTypes(promoted), []string{"text", "image_url", "video_url"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("promoted block types = %v, want %v (order follows return order)", got, want)
	}
	intro := promoted[0]["text"].(string)
	if !strings.Contains(intro, "call-1") || !strings.Contains(intro, "call-3") {
		t.Errorf("intro should name call-1 and call-3: %s", intro)
	}
	if strings.Contains(intro, "call-2") {
		t.Errorf("call-2 returned no media, intro should not name it: %s", intro)
	}
}

// TestOpenAIToolResultVideoPromotedWithConfig verifies video carries the
// fps/media_resolution settings (a non-standard extension the video-capable
// gateways expect).
func TestOpenAIToolResultVideoPromotedWithConfig(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.VideoPart{URI: testVideoURI}},
	}), func(p *providers.OpenAIProvider) { p.SetVideoConfig(8, 1) })

	promoted := contentBlocks(t, msgs[3])
	v := promoted[1]
	if v["fps"] != float64(8) {
		t.Errorf("fps = %v, want 8", v["fps"])
	}
	if v["media_resolution"] != "max" {
		t.Errorf("media_resolution = %v, want max", v["media_resolution"])
	}
	if url := v["video_url"].(map[string]any)["url"]; url != testVideoURI {
		t.Errorf("video url = %v, want %s", url, testVideoURI)
	}
}

// TestOpenAIToolResultAudioPromoted checks audio becomes input_audio with raw
// base64 plus a format derived from the MIME type.
func TestOpenAIToolResultAudioPromoted(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.AudioPart{URI: testWavURI}},
	}), nil)

	promoted := contentBlocks(t, msgs[3])
	if got, want := blockTypes(promoted), []string{"text", "input_audio"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("promoted block types = %v, want %v", got, want)
	}
	audio := promoted[1]["input_audio"].(map[string]any)
	if audio["data"] != "UklGRiQ=" || audio["format"] != "wav" {
		t.Errorf("input_audio = %v, want base64 data + format wav", audio)
	}
}

// TestOpenAIToolMediaNotPromoted covers the shapes Chat Completions cannot
// carry as blocks: documents (no block type) and audio behind a remote URL
// (server will not fetch it). These must keep being flattened to text and must
// not spawn a promoted message, or we would send a user message with nothing
// but a label in it.
func TestOpenAIToolMediaNotPromoted(t *testing.T) {
	cases := []struct {
		name     string
		part     llm.ContentPart
		wantText string
	}{
		{"document", &llm.DocumentPart{URI: testPDFURI}, "[Document (application/pdf)]"},
		{"remote audio url", &llm.AudioPart{URI: "https://example.com/a.mp3"}, "[Audio: https://example.com/a.mp3]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
				ID:     "call-1",
				Output: []llm.ContentPart{tc.part},
			}), nil)

			if len(msgs) != 3 {
				t.Fatalf("got %d messages %v, want 3 (nothing to promote)", len(msgs), roles(msgs))
			}
			content, ok := msgs[2]["content"].(string)
			if !ok {
				t.Fatalf("tool content = %T, want string", msgs[2]["content"])
			}
			if !strings.Contains(content, tc.wantText) {
				t.Errorf("tool content missing %q: %s", tc.wantText, content)
			}
			if strings.Contains(content, "attached to the next message") {
				t.Errorf("non-promotable media must not point forward: %s", content)
			}
		})
	}
}

// TestOpenAINoMediaAddsNoUserMessage is the regression guard: text-only tool
// results must produce exactly one tool message, as before promotion existed.
func TestOpenAINoMediaAddsNoUserMessage(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.TextPart{Text: "Result A"}},
	}), nil)

	if got, want := roles(msgs), []string{"user", "assistant", "tool"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if c := msgs[2]["content"].(string); !strings.Contains(c, "Result A") {
		t.Errorf("tool text lost: %s", c)
	}
}

// TestOpenAIErrorToolResultMediaPromoted: screenshots and frames can come back
// on a failed run (a test harness returning the captured screen). The error
// wrapper must survive and the media must still be visible.
func TestOpenAIErrorToolResultMediaPromoted(t *testing.T) {
	msgs := captureMessages(t, toolRound(&llm.ToolOutputPart{
		ID:      "call-1",
		IsError: true,
		Output:  []llm.ContentPart{&llm.ImagePart{URI: testImageURI}},
	}), nil)

	if c := msgs[2]["content"].(string); !strings.Contains(c, `"status":"error"`) {
		t.Errorf("error status wrapper lost: %s", c)
	}
	if got, want := blockTypes(contentBlocks(t, msgs[3])), []string{"text", "image_url"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("promoted block types = %v, want %v", got, want)
	}
}

// TestOpenAISeparateRoundsPromoteSeparately checks each tool round gets its own
// promoted message right after its own tool messages — interleaving them all
// into one trailing message would detach media from the round that produced it.
func TestOpenAISeparateRoundsPromoteSeparately(t *testing.T) {
	contents := toolRound(&llm.ToolOutputPart{
		ID:     "call-1",
		Output: []llm.ContentPart{&llm.ImagePart{URI: testImageURI}},
	})
	contents = append(contents, testMsg(llm.RoleAssistant,
		&llm.TextPart{Text: "one more"},
		&llm.ToolInputPart{ID: "call-2", Name: "read_file", Input: json.RawMessage(`{}`)},
	)...)
	contents = append(contents, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "call-2", Output: []llm.ContentPart{&llm.ImagePart{URI: testImageURI}}},
	)...)

	msgs := captureMessages(t, contents, nil)
	wantRoles := []string{"user", "assistant", "tool", "user", "assistant", "tool", "user"}
	if got := roles(msgs); strings.Join(got, ",") != strings.Join(wantRoles, ",") {
		t.Fatalf("roles = %v, want %v", got, wantRoles)
	}
	for _, i := range []int{3, 6} {
		if got, want := blockTypes(contentBlocks(t, msgs[i])), []string{"text", "image_url"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("message %d block types = %v, want %v", i, got, want)
		}
	}
}

// TestOpenAIUserAttachedMediaUnchanged guards the refactor that made promotion
// share openaiMediaBlock with the regular content path: user-attached media
// must keep its exact wire shape.
func TestOpenAIUserAttachedMediaUnchanged(t *testing.T) {
	contents := testMsg(llm.RoleUser,
		&llm.TextPart{Text: "look"},
		&llm.ImagePart{URI: testImageURI},
		&llm.AudioPart{URI: testWavURI},
		&llm.VideoPart{URI: testVideoURI},
		&llm.DocumentPart{URI: testPDFURI},
	)

	msgs := captureMessages(t, contents, nil)
	blocks := contentBlocks(t, msgs[0])
	want := []string{"text", "image_url", "input_audio", "video_url", "text"}
	if got := blockTypes(blocks); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("block types = %v, want %v (PDF stays a text placeholder)", got, want)
	}
	if !strings.Contains(blocks[4]["text"].(string), "PDF not supported") {
		t.Errorf("document placeholder changed: %s", blocks[4]["text"])
	}
	if v := blocks[3]; v["fps"] != float64(2) || v["media_resolution"] != "default" {
		t.Errorf("video defaults = %v/%v, want 2/default", v["fps"], v["media_resolution"])
	}
}
