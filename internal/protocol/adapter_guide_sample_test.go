package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// samplePath is the adapter-guide's model_list export. Nothing regenerated it
// automatically, so when ModelInfo gained SerialToolCalls (a field with no
// omitempty) the committed sample silently stopped matching what the binary
// broadcasts — and the guide's schema table omitted the field with it. This
// check ties the sample back to the type: the sample must round-trip through
// ModelInfo byte for byte.
//
// To update the sample deliberately, change ModelInfo, then re-emit it with the
// same models the sample documents and replace the file; this test failing is
// the reminder, not a reason to hand-edit JSON.
const samplePath = "../../adapter-guide/tlv-samples/SM-model-list.bin"

func TestModelListSampleMatchesModelInfo(t *testing.T) {
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read %s: %v", samplePath, err)
	}

	// The sample is one TLV frame: tag "SM", value an envelope.
	reader := bytes.NewReader(raw)
	tag, value, err := tlv.ReadTLV(reader)
	if err != nil {
		t.Fatalf("decode TLV frame: %v", err)
	}
	if tag != tlv.TagSystemMsg {
		t.Fatalf("tag = %q, want %q", tag, tlv.TagSystemMsg)
	}

	env, err := ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("parse system message: %v", err)
	}
	if env.Type != string(MsgTypeModelList) {
		t.Fatalf("type = %q, want %q", env.Type, MsgTypeModelList)
	}

	var payload struct {
		Models []ModelInfo `json:"models"`
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
		t.Errorf("SM-model-list.bin no longer matches protocol.ModelInfo:\n  sample:      %s\n  regenerated: %s",
			env.Data, regenerated)
	}
}

// The guide ships one .bin per frame it documents, and nothing kept them in
// step with the code. Two had already drifted by the time this was written:
// SM-model-list.bin had lost a field (the file above), and
// UF-read-file-success.bin carried raw 0x0A bytes inside a JSON string — no
// parser accepts it, yet it was published as the reference for what a UF frame
// looks like.
//
// This guard checks every sample for what must hold for any frame drawn from
// this protocol: exactly one TLV frame and no trailing bytes, a tag this
// package defines, a filename that names that tag, an id prefix (where present)
// that decodes, and JSON where the tag carries JSON.
//
// What it deliberately does not check: whether the JSON's fields match the Go
// type. That needs a per-tag round-trip, and most payload structs (the SM
// message types) live in internal/agent. Model_list is the one with a
// full-fidelity check; see TestModelListSampleMatchesModelInfo.
func TestAdapterGuideSamplesAreWellFormedFrames(t *testing.T) {
	files, err := filepath.Glob("../../adapter-guide/tlv-samples/*.bin")
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no samples found — the path is wrong, not the samples")
	}

	known := map[string]bool{
		tlv.TagAssistantR: true, tlv.TagAssistantT: true, tlv.TagAssistantF: true,
		tlv.TagUserT: true, tlv.TagUserF: true, tlv.TagUserI: true, tlv.TagUserV: true,
		tlv.TagUserA: true, tlv.TagUserD: true, tlv.TagUserEnd: true,
		tlv.TagCommandIn: true, tlv.TagCommandOut: true, tlv.TagSystemMsg: true,
		tlv.TagAssistantRDelta: true, tlv.TagAssistantTDelta: true,
		tlv.TagAssistantFDelta: true, tlv.TagUserFDelta: true,
	}
	// Tags whose payload is JSON (after an optional id prefix). The text and
	// media tags carry text or a data URI, and carry no JSON by design.
	jsonBearing := map[string]bool{
		tlv.TagAssistantF: true, tlv.TagUserF: true,
		tlv.TagAssistantFDelta: true, tlv.TagUserFDelta: true,
		tlv.TagCommandIn: true, tlv.TagCommandOut: true, tlv.TagSystemMsg: true,
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			reader := bytes.NewReader(raw)
			tag, value, err := tlv.ReadTLV(reader)
			if err != nil {
				t.Fatalf("not a TLV frame: %v", err)
			}
			if reader.Len() != 0 {
				t.Errorf("%d trailing bytes after the frame", reader.Len())
			}
			if !known[tag] {
				t.Fatalf("tag %q is not a tag this package defines", tag)
			}

			// The name is "<tag>-<what>" (or just "<tag>"); the tag must match,
			// so a renamed or misfiled sample cannot go unnoticed.
			wantTag := strings.TrimSuffix(name, ".bin")
			if i := strings.IndexByte(wantTag, '-'); i >= 0 {
				wantTag = wantTag[:i]
			}
			if tag != wantTag {
				t.Errorf("frame tag %q does not match the sample name %q", tag, name)
			}

			// UnwrapID is a no-op without a prefix, so this is the payload
			// either way; a malformed prefix would make it disagree.
			_, content, _ := tlv.UnwrapID(value)
			if jsonBearing[tag] && !json.Valid([]byte(content)) {
				t.Errorf("payload of %s is not valid JSON:\n%s", tag, content)
			}
		})
	}
}
