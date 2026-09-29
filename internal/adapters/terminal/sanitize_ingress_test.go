package terminal

// Every path by which content enters a window, driven with bytes that are not
// UTF-8, checking the frame that comes out is.
//
// sanitize.go states the invariant and the renderers enforce it at the four fields
// that hold drawn content. This is the check that the enforcement is complete: it
// does not know which method writes which field, so a path added later that assigns
// one directly fails here rather than in a terminal.

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ingressIllFormed carries one of every way a byte can fail to be UTF-8: a lone
// continuation, a 3-byte encoding cut short, a lone C1 control, a byte past
// U+10FFFF, an overlong encoding, and two bytes that are never UTF-8 at all.
const ingressIllFormed = "a\x80b\xe4\xb8z\x9b[m c\xf5d\xc0\x80e\xfe\xff"

// ingressRepaired is what a terminal draws for it, which is what sanitizeUTF8
// produces: one U+FFFD per ill-formed run, the bytes around them untouched.
const ingressRepaired = "a\uFFFDb\uFFFDz\uFFFD[m c\uFFFDd\uFFFD\uFFFDe\uFFFD\uFFFD"

// TestEveryContentIngressDrawsWellFormedUTF8 drives each entry point with
// ingressIllFormed and requires two things of the frame: that it is well-formed,
// and that the replacements are in it — the second so a path that quietly dropped
// the content cannot pass by emitting nothing.
func TestEveryContentIngressDrawsWellFormedUTF8(t *testing.T) {
	if got := sanitizeUTF8(ingressIllFormed); got != ingressRepaired {
		t.Fatalf("the fixture's repaired form is %q, not %q; the expectations below assume the latter", got, ingressRepaired)
	}

	paths := []struct {
		name string
		feed func(wb *WindowBuffer)
	}{
		{"AppendOrUpdate, assistant text (AT)", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagAssistantT, "at", ingressIllFormed)
		}},
		{"AppendOrUpdate, reasoning (AR)", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagAssistantR, "ar", ingressIllFormed)
		}},
		{"AppendOrUpdate, user text (UT)", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagUserT, "ut", ingressIllFormed)
		}},
		{"AppendUserContent", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagUserT, "uc", "")
			wb.AppendUserContent("uc", tlv.TagUserT, ingressIllFormed)
		}},
		{"HandleToolInputEvent, name and input (AF)", func(wb *WindowBuffer) {
			wb.HandleToolInputEvent(protocol.ToolInputData{
				ID:    "af",
				Name:  ingressIllFormed,
				Input: json.RawMessage(ingressIllFormed),
			}, 1)
		}},
		{"HandleToolInputDelta, name and delta (Af)", func(wb *WindowBuffer) {
			wb.HandleToolInputDelta("afd", ingressIllFormed, ingressIllFormed, 1)
		}},
		{"HandleToolOutputDelta, preview (Uf)", func(wb *WindowBuffer) {
			wb.HandleToolOutputDelta("ufd", ingressIllFormed, 1)
		}},
		{"HandleToolOutput, result (UF)", func(wb *WindowBuffer) {
			wb.HandleToolOutput("uf", ingressIllFormed, false, 1)
		}},
		{"HandleToolOutput, error result", func(wb *WindowBuffer) {
			wb.HandleToolOutput("ufe", ingressIllFormed, true, 1)
		}},
		{"SetRendererForTool, name and input", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagAssistantF, "srt", "")
			for i := range wb.windows {
				wb.WindowAt(i).SetRendererForTool(ingressIllFormed, ingressIllFormed)
			}
		}},
		{"Window.AppendFromTLV directly", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagAssistantT, "direct", "")
			wb.WindowAt(0).AppendFromTLV(tlv.TagAssistantT, ingressIllFormed)
		}},
		{"Window.AppendContent directly", func(wb *WindowBuffer) {
			wb.AppendOrUpdate(tlv.TagAssistantT, "direct2", "")
			wb.WindowAt(0).AppendContent(ingressIllFormed)
		}},
	}

	for _, p := range paths {
		for _, blocked := range []bool{false, true} {
			register := "normal"
			if blocked {
				register = "dimmed"
			}
			t.Run(register+"/"+p.name, func(t *testing.T) {
				styles := DefaultStyles()
				if blocked {
					styles = styles.Dimmed()
				}
				// Wide enough that the content is drawn rather than summarized away,
				// and narrow enough that it wraps: a row the wrap broke is the one
				// whose padding depends on the width being right.
				for _, width := range []int{24, 41, 80} {
					wb := NewWindowBuffer(width, styles)
					p.feed(wb)
					for i := range wb.windows {
						w := wb.WindowAt(i)
						w.Folded = false
						// A window created with empty content is not visible, and some
						// of these paths append to one rather than creating it with
						// content. Visibility is not what is under test here.
						w.Visible = true
						w.Invalidate()
					}
					frame := wb.GetAll(-1, blocked)
					if !utf8.ValidString(frame) {
						t.Errorf("width %d: the frame is not well-formed UTF-8:\n%q", width, frame)
						continue
					}
					if !strings.Contains(frame, replacementChar) {
						t.Errorf("width %d: the frame carries no replacement character, so the ill-formed content never reached it and this path proved nothing:\n%q", width, frame)
					}
				}
			})
		}
	}
}

// TestIngressRepairsRatherThanDrops is the other half: repairing must not lose the
// content around the bad bytes, and the window must report the repaired string as
// its own — which is what the summary's counts and the wrap are both reading.
func TestIngressRepairsRatherThanDrops(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "at", ingressIllFormed)
	if got := wb.WindowAt(0).RawContent(); got != ingressRepaired {
		t.Errorf("a text window's content is %q, want %q", got, ingressRepaired)
	}
	// And appending more does not disturb what was repaired: the counts cover the
	// repaired bytes, not the ones that arrived.
	wb.AppendOrUpdate(tlv.TagAssistantT, "at", " tail")
	if got := wb.WindowAt(0).RawContent(); got != ingressRepaired+" tail" {
		t.Errorf("after a further append the content is %q, want %q", got, ingressRepaired+" tail")
	}
}
