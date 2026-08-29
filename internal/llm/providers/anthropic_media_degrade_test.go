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

// The Messages API has no audio/video content block, and an unrecognized block
// type fails the entire request. These tests lock the degradation to text —
// including the case that motivated it: a model calling read_file on a video
// (which the tool description invites) must not take down its own next turn.

// captureAnthropicMessages runs the Anthropic provider against a stub server
// and returns the wire messages it received.
func captureAnthropicMessages(t *testing.T, contents []llm.ContentPart) []map[string]any {
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
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(server.Close)

	p, err := providers.NewAnthropic(providers.BaseConfig{APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := p.StreamMessages(context.Background(), contents, nil, "You are helpful", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	return captured
}

// allBlockTypes collects every "type" value anywhere in the request, including
// blocks nested inside tool_result content. A single flat scan is not enough:
// the media block we must not send lives one level deeper than usual.
func allBlockTypes(t *testing.T, messages []map[string]any) []string {
	t.Helper()
	enc, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(enc, &generic); err != nil {
		t.Fatal(err)
	}
	var types []string
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case []any:
			for _, item := range n {
				walk(item)
			}
		case map[string]any:
			if ty, ok := n["type"].(string); ok {
				types = append(types, ty)
			}
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(generic)
	return types
}

func contains(types []string, want string) bool {
	for _, ty := range types {
		if ty == want {
			return true
		}
	}
	return false
}

func TestAnthropicToolResultVideoDegradesToText(t *testing.T) {
	// Exactly what read_file returns for a clip: a text line plus a media part.
	contents := testMsg(llm.RoleUser, &llm.TextPart{Text: "watch clip.mp4"})
	contents = append(contents, testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: "call-v", Name: "read_file", Input: json.RawMessage(`{"path":"clip.mp4"}`)})...)
	out := &llm.ToolOutputPart{ID: "call-v", Output: []llm.ContentPart{
		&llm.TextPart{Text: "Read clip.mp4 (1200.0KB, video/mp4)"},
		&llm.VideoPart{URI: "data:video/mp4;base64,AAAAVERYLONGBASE64"},
	}}
	out.SetRole(llm.RoleTool)
	contents = append(contents, out)

	msgs := captureAnthropicMessages(t, contents)
	types := allBlockTypes(t, msgs)
	if contains(types, "video") || contains(types, "audio") {
		t.Fatalf("request carries a native audio/video block (%v) — the API would reject the whole request", types)
	}
	if !contains(types, "tool_result") || !contains(types, "text") {
		t.Fatalf("expected tool_result + text blocks, got %v", types)
	}

	// The base64 payload must not be echoed into the placeholder text.
	body, _ := json.Marshal(msgs)
	if strings.Contains(string(body), "AAAAVERYLONGBASE64") {
		t.Error("placeholder leaked the media payload into text")
	}
}

func TestAnthropicUserAttachedMediaDegradesToText(t *testing.T) {
	contents := testMsg(llm.RoleUser,
		&llm.TextPart{Text: "listen"},
		&llm.AudioPart{URI: "data:audio/wav;base64,UklGRiQ="},
		&llm.VideoPart{URI: "https://example.com/a.mp4"},
	)

	msgs := captureAnthropicMessages(t, contents)
	if types := allBlockTypes(t, msgs); contains(types, "audio") || contains(types, "video") {
		t.Fatalf("user-attached media produced a native block: %v", types)
	}

	blocks := contentBlocks(t, msgs[0])
	text := blocks[1]["text"].(string)
	if !strings.Contains(text, "audio (audio/wav)") {
		t.Errorf("data-URI placeholder should name the MIME type, got: %s", text)
	}
	// A remote URL has no MIME to parse, so the URL is the only identifier.
	if remote := blocks[2]["text"].(string); !strings.Contains(remote, "https://example.com/a.mp4") {
		t.Errorf("remote-URL placeholder should keep the URL, got: %s", remote)
	}
}

// TestAnthropicPlaceholderDeniesPerception guards the wording, which is the
// real substance of this change. "[video unsupported]" would be read by a
// model as an ordinary tool result, after which it will confidently describe
// frames it never received.
func TestAnthropicPlaceholderDeniesPerception(t *testing.T) {
	contents := testMsg(llm.RoleTool, &llm.ToolOutputPart{ID: "call-v", Output: []llm.ContentPart{
		&llm.VideoPart{URI: "data:video/mp4;base64,AAAA"},
	}})

	msgs := captureAnthropicMessages(t, contents)
	toolResult := contentBlocks(t, msgs[0])[0]
	// A tool_result whose content collapses to a single text block is emitted
	// as a plain string (anthropicPartToBlock keeps that simpler wire shape).
	// So a video-only tool result degrades to a string body, not a block array.
	var text string
	switch c := toolResult["content"].(type) {
	case string:
		text = c
	case []any:
		text = c[0].(map[string]any)["text"].(string)
	default:
		t.Fatalf("tool_result content = %T, want string or block array", toolResult["content"])
	}

	for _, want := range []string{"NOT delivered", "Do not describe or quote it", "extract a frame"} {
		if !strings.Contains(text, want) {
			t.Errorf("placeholder missing %q: %s", want, text)
		}
	}
}

// TestAnthropicImageDocumentStillNative is the regression guard: degrading
// audio/video must not disturb the two media types this provider serializes as
// native blocks, including inside a tool result (the path that makes
// tool-driven multimodal reading possible here at all). Note this locks *our
// serialization*; server acceptance of a document block nested in
// tool_result is not verified by this test — see docs/providers.md.
func TestAnthropicImageDocumentStillNative(t *testing.T) {
	contents := testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "call-i", Output: []llm.ContentPart{&llm.ImagePart{URI: "data:image/png;base64,iVBOR"}}, IsError: false},
		&llm.ToolOutputPart{ID: "call-d", Output: []llm.ContentPart{&llm.DocumentPart{URI: "data:application/pdf;base64,JVBERi0="}}},
	)

	msgs := captureAnthropicMessages(t, contents)
	types := allBlockTypes(t, msgs)
	if !contains(types, "image") {
		t.Fatalf("nested image must stay a native block (this is how tool multimodal works here), got %v", types)
	}
	if !contains(types, "document") {
		t.Fatalf("document must stay a native block, got %v", types)
	}
}

// TestAnthropicPlaceholderTextMatchesDocs pins the full placeholder sentence.
// docs/internal/data-mapping.md Example 5 quotes this string verbatim as wire
// format documentation; without a pin, rewording the message would leave the
// docs describing bytes no code ever produces. If this fails, fix both.
func TestAnthropicPlaceholderTextMatchesDocs(t *testing.T) {
	contents := testMsg(llm.RoleUser, &llm.AudioPart{URI: "data:audio/wav;base64,UklGRiQ="})
	msgs := captureAnthropicMessages(t, contents)
	got := contentBlocks(t, msgs[0])[0]["text"]

	const documented = "[Unreadable audio (audio/wav): this API has no audio input block, " +
		"so the content was NOT delivered to you and you have not perceived it. " +
		"Do not describe or quote it. To inspect it, transcribe it to text " +
		"(e.g. an available CLI via execute_command).]"
	if got != documented {
		t.Errorf("placeholder drifted from the text quoted in data-mapping.md:\n got: %v\nwant: %s", got, documented)
	}
}
