package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestLoadSessionLargeToolResultFrame is a regression guard for the bug where
// a session that saved successfully could not be loaded back: a tool result
// whose serialized TLV frame exceeds the old private 10MiB cap (for example
// read_file returning an 8-9MB image as a base64 data URI) was rejected by the
// persistence reader with "invalid length", even though tlv.EncodeTLV happily
// wrote it (MaxMessageSize ≈ 2GiB).
//
// The persistence reader must not impose a tighter bound than the writer.
func TestLoadSessionLargeToolResultFrame(t *testing.T) {
	// ~11MiB payload → a UF frame larger than the old 10MiB cap.
	const cap10MiB = 10 * 1024 * 1024
	big := strings.Repeat("A", 11*1024*1024)

	contents := []llm.ContentPart{
		&llm.ToolOutputPart{
			ID: "call_big",
			Output: []llm.ContentPart{
				&llm.TextPart{Text: "Read illus_sheet.png (8705.5KB, image/png)"},
				&llm.ImagePart{URI: "data:image/png;base64," + big},
			},
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool},
		},
	}

	data := &sessionData{
		sessionMeta: sessionMeta{MessageVersion: messageVersion, UpdatedAt: time.Now()},
		Contents:    contents,
	}

	raw, err := formatSessionMarkdown(data)
	if err != nil {
		t.Fatalf("formatSessionMarkdown: %v", err)
	}

	// Precondition: the payload must actually exceed the old cap, otherwise
	// this test would silently stop guarding the regression.
	if len(raw) <= cap10MiB {
		t.Fatalf("test payload too small: encoded session is %d bytes, want > %d", len(raw), cap10MiB)
	}

	loaded, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("parseSessionData rejected a valid >10MiB TLV frame: %v", err)
	}
	if len(loaded.Contents) != 1 {
		t.Fatalf("got %d content parts, want 1", len(loaded.Contents))
	}

	tr, ok := loaded.Contents[0].(*llm.ToolOutputPart)
	if !ok {
		t.Fatalf("content part is %T, want *llm.ToolOutputPart", loaded.Contents[0])
	}
	if len(tr.Output) != 2 {
		t.Fatalf("tool output has %d parts, want 2", len(tr.Output))
	}
	img, ok := tr.Output[1].(*llm.ImagePart)
	if !ok {
		t.Fatalf("tool output[1] is %T, want *llm.ImagePart", tr.Output[1])
	}
	if img.URI != "data:image/png;base64,"+big {
		t.Fatal("large image URI was not preserved through the round trip")
	}
}
