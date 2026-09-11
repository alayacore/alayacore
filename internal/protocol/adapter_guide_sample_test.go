package protocol

import (
	"bytes"
	"encoding/json"
	"os"
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
