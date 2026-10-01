package agent

// Tests for the user-input ingest hook in runTaskNormal: images are fitted
// before they are numbered and echoed, so the adapter, the session and the
// model all see the same part — and a note is never stored in a user's place.

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// testMaxEdge mirrors llm's unexported maxImageEdge.
const testMaxEdge = 4096

func fitTestDataURI(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func fitTestDims(t *testing.T, uri string) (int, int) {
	t.Helper()
	_, b64, ok := llm.ParseDataURI(uri)
	if !ok {
		t.Fatalf("not a data URI: %.60s", uri)
	}
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	return cfg.Width, cfg.Height
}

// TestRunTaskNormalFitsOversizeImagesBeforeStoring pins the user-input ingest
// hook.
//
// It asserts what lands in the session, not what goes on the wire: deleting the
// hook would still pass every wire test, because the send-time gate covers the
// request anyway. What it would hide is the cost — the shrink would be paid on
// every send instead of once.
func TestRunTaskNormalFitsOversizeImagesBeforeStoring(t *testing.T) {
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: &mockProviderStepFail{responses: []stepResponse{{text: "ok"}}},
		MaxSteps: 10,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, Output: io.Discard},
		},
		sharedState: sharedState{
			histCounter:  200,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.taskResultCh = make(chan []llm.ContentPart, 1)

	oversize := fitTestDataURI(t, testMaxEdge+1, 3)
	session.runTaskNormal(context.Background(), []llm.ContentPart{&llm.ImagePart{URI: oversize}})
	result := <-session.taskResultCh

	var stored *llm.ImagePart
	for _, p := range result {
		if img, ok := p.(*llm.ImagePart); ok {
			stored = img
		}
	}
	if stored == nil {
		t.Fatal("the image vanished from the stored conversation")
	}
	if stored.URI == oversize {
		t.Fatal("the oversized image was stored as-is — the ingest hook is gone")
	}
	if w, h := fitTestDims(t, stored.URI); w > testMaxEdge || h > testMaxEdge {
		t.Fatalf("stored image is %dx%d, still over the limit", w, h)
	}
}

// frameTags returns the TLV tags a session wrote to its output, in order.
func frameTags(t *testing.T, raw []byte) []string {
	t.Helper()
	var tags []string
	r := bytes.NewReader(raw)
	for {
		tag, _, err := tlv.ReadTLV(r)
		if err != nil {
			return tags
		}
		tags = append(tags, tag)
	}
}

// TestRunTaskNormalKeepsUndecodableImageAndEchoesItAsMedia pins the
// user-visible half of the ingest/send split.
//
// Attaching an image this client cannot decode (webp, heic) must leave the
// session holding that image and must echo a media frame. If ingest substituted
// the note instead, the user's own message would render as a wall of
// "[Unreadable image…]" text rather than as the attachment they sent — the
// substitution belongs on the wire, per request, not in the record.
func TestRunTaskNormalKeepsUndecodableImageAndEchoesItAsMedia(t *testing.T) {
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: &mockProviderStepFail{responses: []stepResponse{{text: "ok"}}},
		MaxSteps: 10,
	})
	var out bytes.Buffer
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, Output: &out},
		},
		sharedState: sharedState{
			histCounter:  200,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.taskResultCh = make(chan []llm.ContentPart, 1)

	webp := &llm.ImagePart{URI: "data:image/webp;base64,AAAA"}
	session.runTaskNormal(context.Background(), []llm.ContentPart{webp})
	result := <-session.taskResultCh

	var stored *llm.ImagePart
	for _, p := range result {
		if img, ok := p.(*llm.ImagePart); ok {
			stored = img
		}
	}
	if stored == nil {
		t.Fatal("the attachment vanished from the conversation")
	}
	if stored.URI != webp.URI {
		t.Fatalf("stored URI = %.40s, want the original untouched", stored.URI)
	}

	var hasMedia, hasUserText bool
	for _, tag := range frameTags(t, out.Bytes()) {
		switch tag {
		case tlv.TagUserI:
			hasMedia = true
		case tlv.TagUserT:
			hasUserText = true
		}
	}
	if !hasMedia {
		t.Error("the echo carried no media frame — the attachment did not come back as media")
	}
	if hasUserText {
		t.Error("the echo carried a user text frame — the attachment was shown as text")
	}
}
