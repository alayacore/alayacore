package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// versionSamplePath is the adapter-guide's version frame — the first frame of
// every session, and the one an out-of-process adapter reads to decide whether
// it understands the rest.
//
// Nothing tied that file to the constant it documents. It sat at
// message_version 14 through a release that moved the wire to 15, and the
// well-formed-frames sweep next door could not see it: the frame is one line
// with a valid tag and valid JSON, which is all that check asks. The number
// inside is the whole content of the file, and only the package that owns the
// constant can compare the two.
//
// So this is the comparison. It is in this package rather than beside the other
// sample checks (internal/protocol) because agent owns messageVersion and the
// dependency runs one way: protocol must not import agent. Regenerate the sample
// by hand when the constant moves — this failing is the reminder.
const versionSamplePath = "../../adapter-guide/wire-samples/SM-message-version.txt"

func TestMessageVersionSampleMatchesTheConstant(t *testing.T) {
	raw, err := os.ReadFile(versionSamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", versionSamplePath, err)
	}

	tag, value, err := wire.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", versionSamplePath, err)
	}
	if tag != wire.TagSystemMsg {
		t.Fatalf("tag = %q, want %q", tag, wire.TagSystemMsg)
	}

	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("parse system message: %v", err)
	}
	if env.Type != string(protocol.MsgTypeVersion) {
		t.Fatalf("type = %q, want %q", env.Type, protocol.MsgTypeVersion)
	}

	var payload struct {
		MessageVersion int `json:"message_version"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("decode version payload: %v", err)
	}
	if payload.MessageVersion != messageVersion {
		t.Errorf("%s says message_version %d, but this build speaks %d — the guide is what an adapter implements from, so update the sample with the constant",
			versionSamplePath, payload.MessageVersion, messageVersion)
	}
}
