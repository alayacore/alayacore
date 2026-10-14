package tools

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// TestMaxMediaFitsInAFrame holds the two bounds together by building the frame, not
// by comparing the numbers.
//
// Media travels as a base64 data URI inside a JSON wrapper, so MaxMediaBytes has to
// leave room for that expansion (×4/3) and the wrapper. The relation is arithmetic
// and the arithmetic is easy to get wrong quietly; what is not quiet is the
// encoder's answer when a frame is too large — and the failure it prevents is not a
// rejected request but an attachment that cannot be written at all.
func TestMaxMediaFitsInAFrame(t *testing.T) {
	uri := "data:application/octet-stream;base64," +
		base64.StdEncoding.EncodeToString(make([]byte, MaxMediaBytes))
	body, err := json.Marshal(protocol.ContentData{
		ContentItem: protocol.ContentItem{Type: protocol.TypeDocument, URI: uri},
	})
	if err != nil {
		t.Fatal(err)
	}

	frame, err := wire.Encode(wire.TagUserX, string(body))
	if err != nil {
		t.Fatalf("media at the limit does not fit a frame: %v", err)
	}
	if len(frame) > wire.MaxFrameSize {
		t.Errorf("frame of %d bytes over the %d bound", len(frame), wire.MaxFrameSize)
	}
	t.Logf("media at the limit (%dMB) → frame of %d bytes, %.0f%% of wire.MaxFrameSize",
		MaxMediaBytes>>20, len(frame), 100*float64(len(frame))/float64(wire.MaxFrameSize))
}
