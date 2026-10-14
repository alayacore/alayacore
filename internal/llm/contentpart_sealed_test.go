package llm_test

import (
	"reflect"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// Compile-time half of the seal: every part type must satisfy the interface, so
// dropping a marker from any of them stops this file from compiling.
var (
	_ llm.ContentPart = (*llm.TextPart)(nil)
	_ llm.ContentPart = (*llm.ReasoningPart)(nil)
	_ llm.ContentPart = (*llm.ImagePart)(nil)
	_ llm.ContentPart = (*llm.VideoPart)(nil)
	_ llm.ContentPart = (*llm.AudioPart)(nil)
	_ llm.ContentPart = (*llm.DocumentPart)(nil)
	_ llm.ContentPart = (*llm.ToolInputPart)(nil)
	_ llm.ContentPart = (*llm.ToolOutputPart)(nil)
)

// unmarkedPart embeds the metadata every part shares, so it inherits every
// accessor ContentPart requires — but it declares no marker. It exists to prove
// the seal holds: were the marker ever moved onto ContentPartMeta, this type
// would satisfy the interface and the test below would fail.
type unmarkedPart struct{ llm.ContentPartMeta }

// TestContentPartIsSealed pins the seal at runtime: only the marked parts are
// content parts, and embedding the shared metadata is not by itself enough.
func TestContentPartIsSealed(t *testing.T) {
	it := reflect.TypeOf((*llm.ContentPart)(nil)).Elem()
	if reflect.TypeOf(&unmarkedPart{}).Implements(it) {
		t.Fatal("ContentPart is not sealed: a type with no marker satisfies it")
	}
}
