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

// captureRawMessages returns the wire messages exactly as bytes, in order.
// Byte-level rather than decoded: the properties under test are about
// serialization stability, and a decoded round-trip would hide key ordering.
func captureRawMessages(t *testing.T, contents []llm.ContentPart) []json.RawMessage {
	t.Helper()

	var captured []json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		captured = body.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	p, err := providers.NewOpenAI(providers.BaseConfig{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	// Non-default values on purpose: fps/media_resolution are embedded in every
	// video block, so a changing value rewrites earlier messages — the exact
	// kind of prefix invalidation this test is about.
	p.SetVideoConfig(4, 0)

	events, err := p.StreamMessages(context.Background(), contents, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	return captured
}

// mediaRound builds one assistant tool_call plus a tool result carrying both an
// image and a video, so the round produces a promoted user message.
func mediaRound(id string) []llm.ContentPart {
	out := &llm.ToolOutputPart{ID: id, Output: []llm.ContentPart{
		&llm.TextPart{Text: "Read " + id},
		&llm.ImagePart{URI: "data:image/png;base64,AAAA"},
		&llm.VideoPart{URI: "data:video/mp4;base64,BBBB"},
	}}
	out.SetRole(llm.RoleTool)
	contents := testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: id, Name: "read_file", Input: json.RawMessage(`{"path":"x"}`)})
	return append(contents, out)
}

// TestOpenAIPromotionIsDeterministicAndPrefixStable locks the property asserted
// in docs/providers.md → "Tool results": promoted media sits in the MIDDLE of
// the conversation (a user message after the tool messages), so it must be a
// pure function of history. If it were not, every turn would rewrite the prefix
// from that point and destroy provider-side prompt caching.
//
// This is the rare case where a regression is invisible in behavior: requests
// still succeed, answers stay correct — only cache hit rates (and the bill)
// move. A future "optimization" that memoizes, reorders, or lets request state
// leak into the promoted message would break it silently.
func TestOpenAIPromotionIsDeterministicAndPrefixStable(t *testing.T) {
	// Built as independent groups so neither history can alias the other's
	// backing array.
	concat := func(groups ...[]llm.ContentPart) []llm.ContentPart {
		var out []llm.ContentPart
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	start := testMsg(llm.RoleUser, &llm.TextPart{Text: "start"})
	turn1 := concat(start, mediaRound("call-1"))
	turn2 := concat(start, mediaRound("call-1"),
		testMsg(llm.RoleAssistant, &llm.TextPart{Text: "and again"}),
		mediaRound("call-2"))

	first := captureRawMessages(t, turn1)
	again := captureRawMessages(t, turn1)

	if len(first) != len(again) {
		t.Fatalf("same history serialized to %d then %d messages — not deterministic", len(first), len(again))
	}
	for i := range first {
		if string(first[i]) != string(again[i]) {
			t.Fatalf("message %d differs across identical requests:\n %s\n %s", i, first[i], again[i])
		}
	}

	// Non-vacuity: the property must be exercised on a promoted message, not
	// merely on a conversation that happens to have none.
	if len(first) != 4 {
		t.Fatalf("got %d messages, want 4 (user, assistant, tool, promoted user):\n%s", len(first), first)
	}
	promoted := string(first[3])
	if !strings.Contains(promoted, "Media returned by tool result") {
		t.Fatalf("message 3 is not the promoted media message, so the prefix claim below proves nothing: %s", promoted)
	}

	grown := captureRawMessages(t, turn2)
	if len(grown) <= len(first) {
		t.Fatalf("turn 2 produced %d messages, expected more than turn 1's %d", len(grown), len(first))
	}
	for i := range first {
		if string(first[i]) != string(grown[i]) {
			t.Fatalf("turn 1 is no longer an exact prefix of turn 2 at message %d — every provider cache from this point on would miss:\n turn1: %s\n turn2: %s",
				i, first[i], grown[i])
		}
	}
}
