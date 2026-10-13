package protocol_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// samplePath is the adapter-guide's model_list export. Nothing regenerated it
// automatically, so when ModelInfo gained SerialToolCalls (a field with no
// omitempty) the committed sample silently stopped matching what the binary
// broadcasts — and the guide's schema table omitted the field with it. This
// check ties the sample back to the type: the sample must round-trip through
// protocol.ModelInfo byte for byte.
//
// To update the sample deliberately, change ModelInfo, then re-emit it with the
// same models the sample documents and replace the file; this test failing is
// the reminder, not a reason to hand-edit JSON.
const samplePath = "../../adapter-guide/wire-samples/SM-model-list.txt"

func TestModelListSampleMatchesModelInfo(t *testing.T) {
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read %s: %v", samplePath, err)
	}

	// The sample is one frame: tag "SM", value an envelope.
	reader := bytes.NewReader(raw)
	tag, value, err := wiretest.NextFrame(reader)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if tag != wire.TagSystemMsg {
		t.Fatalf("tag = %q, want %q", tag, wire.TagSystemMsg)
	}

	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("parse system message: %v", err)
	}
	if env.Type != string(protocol.MsgTypeModelList) {
		t.Fatalf("type = %q, want %q", env.Type, protocol.MsgTypeModelList)
	}

	var payload struct {
		Models []protocol.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("decode model_list payload: %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatal("sample carries no models")
	}

	// A field added to (or removed from) ModelInfo changes this re-marshal,
	// which is the whole point: the sample must be regenerated with it.
	regenerated, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("re-marshal payload: %v", err)
	}
	if !bytes.Equal(regenerated, env.Data) {
		t.Errorf("SM-model-list.txt no longer matches protocol.ModelInfo:\n  sample:      %s\n  regenerated: %s",
			env.Data, regenerated)
	}
}

// themeListSamplePath is the adapter-guide's theme_list export: the frame the
// terminal caches its palettes from. It gets a full-fidelity check rather than
// the framing/JSON-only sweep below — theme_list's payload is a name plus a
// theme.Theme, and Theme is the one wire struct that lives outside
// internal/agent, so the round-trip needs no import this package cannot take. A
// palette role added to or dropped from Theme changes the re-marshal, which is
// the point: the schema table, the wire-value example, and this shipped frame
// must move together. They did not when `selection` went — the table and example
// were updated and the .bin was not, and the sweep below cannot see a field.
const themeListSamplePath = "../../adapter-guide/wire-samples/SM-theme-list.txt"

func TestThemeListSampleMatchesTheme(t *testing.T) {
	raw, err := os.ReadFile(themeListSamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", themeListSamplePath, err)
	}

	// The sample is one frame: tag "SM", value an envelope.
	reader := bytes.NewReader(raw)
	tag, value, err := wiretest.NextFrame(reader)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if tag != wire.TagSystemMsg {
		t.Fatalf("tag = %q, want %q", tag, wire.TagSystemMsg)
	}

	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("parse system message: %v", err)
	}
	if env.Type != string(protocol.MsgTypeThemeList) {
		t.Fatalf("type = %q, want %q", env.Type, protocol.MsgTypeThemeList)
	}

	var payload struct {
		Themes []struct {
			Name  string       `json:"name"`
			Theme *theme.Theme `json:"theme"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("decode theme_list payload: %v", err)
	}
	if len(payload.Themes) == 0 {
		t.Fatal("sample carries no themes")
	}

	// A field added to (or removed from) theme.Theme changes this re-marshal,
	// which is the whole point: the sample must be regenerated with it.
	regenerated, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("re-marshal payload: %v", err)
	}
	if !bytes.Equal(regenerated, env.Data) {
		t.Errorf("SM-theme-list.txt no longer matches theme.Theme:\n  sample:      %s\n  regenerated: %s",
			env.Data, regenerated)
	}
}

// The guide ships one .txt per frame it documents, and nothing kept them in
// step with the code. Two had already drifted by the time this was written:
// SM-model-list.txt had lost a field (the file above), and
// UF-read-file-success.txt carried raw 0x0A bytes inside a JSON string — no
// parser accepts it, yet it was published as the reference for what a UF frame
// looks like.
//
// This guard checks every sample for what must hold for any frame drawn from
// this protocol: exactly one frame and no trailing bytes, a tag this
// package defines, a filename that names that tag, an id prefix (where present)
// that decodes, and JSON where the tag carries JSON.
//
// What it deliberately does not check: whether the JSON's fields match the Go
// type. That needs a per-tag round-trip, and most payload structs (the SM
// message types) live in internal/agent. Model_list and theme_list carry
// full-fidelity checks; see TestModelListSampleMatchesModelInfo and
// TestThemeListSampleMatchesTheme.
func TestAdapterGuideSamplesAreWellFormedFrames(t *testing.T) {
	files, err := filepath.Glob("../../adapter-guide/wire-samples/*.txt")
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no samples found — the path is wrong, not the samples")
	}

	known := map[string]bool{
		wire.TagAssistantF: true, wire.TagUserF: true,
		wire.TagUserX: true, wire.TagAssistantX: true, wire.TagAssistantXDelta: true,
		wire.TagUserEnd: true, wire.TagInputEnd: true,
		wire.TagCommandIn: true, wire.TagCommandOut: true, wire.TagSystemMsg: true,
		wire.TagAssistantFDelta: true, wire.TagUserFDelta: true,
	}
	// Tags whose payload is JSON. With v14 that is every frame except the
	// zero-length control frames (UE/CE): text, media and tool frames all carry
	// a JSON object.
	jsonBearing := map[string]bool{
		wire.TagUserX: true, wire.TagAssistantX: true, wire.TagAssistantXDelta: true,
		wire.TagAssistantF: true, wire.TagAssistantFDelta: true,
		wire.TagUserF: true, wire.TagUserFDelta: true,
		wire.TagCommandIn: true, wire.TagCommandOut: true, wire.TagSystemMsg: true,
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			reader := bytes.NewReader(raw)
			tag, value, err := wiretest.NextFrame(reader)
			if err != nil {
				t.Fatalf("not a frame: %v", err)
			}
			if reader.Len() != 0 {
				t.Errorf("%d trailing bytes after the frame", reader.Len())
			}
			if !known[tag] {
				t.Fatalf("tag %q is not a tag this package defines", tag)
			}

			// The name is "<tag>-<what>" (or just "<tag>"); the tag must match,
			// so a renamed or misfiled sample cannot go unnoticed.
			wantTag := strings.TrimSuffix(name, ".txt")
			if i := strings.IndexByte(wantTag, '-'); i >= 0 {
				wantTag = wantTag[:i]
			}
			if tag != wantTag {
				t.Errorf("frame tag %q does not match the sample name %q", tag, name)
			}

			if jsonBearing[tag] && !json.Valid([]byte(value)) {
				t.Errorf("payload of %s is not valid JSON:\n%s", tag, value)
			}
		})
	}
}
