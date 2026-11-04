package llm

import (
	"strings"
	"testing"
)

// mediaFixture is one part of each modality, with the meta that must survive a
// degradation.
func mediaFixture() (img *ImagePart, audio *AudioPart, doc *DocumentPart, text *TextPart) {
	img = &ImagePart{URI: "data:image/png;base64,QUJD"}
	img.HistoryID = 7
	img.Role = RoleUser
	audio = &AudioPart{URI: "data:audio/wav;base64,REVG"}
	audio.HistoryID = 8
	doc = &DocumentPart{URI: "https://example.com/report.pdf"}
	doc.HistoryID = 9
	text = &TextPart{Text: "hello"}
	text.HistoryID = 10
	return img, audio, doc, text
}

// videoPart is the fourth media modality, which takes no history ID because the
// test that uses it only checks the note.
func videoPart() *VideoPart { return &VideoPart{URI: "data:video/mp4;base64,R0hJ"} }

// TestFitInputModalitiesUndeclaredIsUntouched pins the nil case to the behavior
// every model.conf without the key has: the protocol decides, so nothing is
// rewritten — and not even copied.
func TestFitInputModalitiesUndeclaredIsUntouched(t *testing.T) {
	img, audio, doc, text := mediaFixture()
	in := []ContentPart{text, img, audio, doc}

	out := FitInputModalities(in, nil)

	if len(out) != len(in) {
		t.Fatalf("parts changed: %d in, %d out", len(in), len(out))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("part %d was rewritten by an undeclared model", i)
		}
	}
}

// TestFitInputModalitiesEmptyDeclarationDegradesEverything is the "declared
// nothing" case: an empty list is a declaration, not an absence, so every media
// part becomes a note and text is left alone.
func TestFitInputModalitiesEmptyDeclarationDegradesEverything(t *testing.T) {
	img, audio, doc, text := mediaFixture()
	video := videoPart()

	out := FitInputModalities([]ContentPart{text, img, video, audio, doc}, []string{})

	if got, ok := out[0].(*TextPart); !ok || got != text {
		t.Errorf("text part must pass through unchanged, got %T", out[0])
	}
	for i, want := range []string{"Image (image/png)", "Video (video/mp4)", "Audio (audio/wav)", "Document at https://example.com/report.pdf"} {
		note, ok := out[i+1].(*TextPart)
		if !ok {
			t.Fatalf("part %d: expected a text note, got %T", i+1, out[i+1])
		}
		if !strings.Contains(note.Text, want) {
			t.Errorf("part %d: note does not name %q: %s", i+1, want, note.Text)
		}
		if !strings.Contains(note.Text, "NOT delivered") || !strings.Contains(note.Text, "input_modalities") {
			t.Errorf("part %d: note must state that nothing was delivered and name the setting: %s", i+1, note.Text)
		}
	}
}

// TestFitInputModalitiesDeclaredSubset keeps what is declared and degrades what
// is not — the case the option exists for: a model on the OpenAI protocol that
// takes images but no audio.
func TestFitInputModalitiesDeclaredSubset(t *testing.T) {
	img, audio, _, text := mediaFixture()

	out := FitInputModalities([]ContentPart{text, img, audio}, []string{"text", "image"})

	if out[0] != ContentPart(text) {
		t.Errorf("text must pass through")
	}
	if out[1] != ContentPart(img) {
		t.Errorf("a declared image must be sent as it is, got %T", out[1])
	}
	note, ok := out[2].(*TextPart)
	if !ok {
		t.Fatalf("an undeclared audio part must degrade, got %T", out[2])
	}
	if !strings.Contains(note.Text, "audio") {
		t.Errorf("note does not name audio: %s", note.Text)
	}
}

// TestFitInputModalitiesNestedToolOutput covers the path media actually takes
// after read_file or an MCP server returns it: inside a tool result, which the
// provider serializes recursively.
func TestFitInputModalitiesNestedToolOutput(t *testing.T) {
	img, audio, _, text := mediaFixture()
	tr := &ToolOutputPart{ID: "call-1", Output: []ContentPart{text, img, audio}}

	out := FitInputModalities([]ContentPart{tr}, []string{"text", "image"})

	got, ok := out[0].(*ToolOutputPart)
	if !ok {
		t.Fatalf("tool result must stay a tool result, got %T", out[0])
	}
	if got == tr {
		t.Fatalf("nested parts changed, so the tool result must be copied, not aliased")
	}
	if got.ID != "call-1" {
		t.Errorf("tool result lost its call ID: %q", got.ID)
	}
	if got.Output[1] != ContentPart(img) {
		t.Errorf("declared nested image must be kept")
	}
	if _, ok := got.Output[2].(*TextPart); !ok {
		t.Errorf("undeclared nested audio must degrade, got %T", got.Output[2])
	}
	if _, ok := tr.Output[2].(*AudioPart); !ok {
		t.Errorf("the caller's parts must not be mutated")
	}
}

// TestFitInputModalitiesKeepsMeta pins that a degraded part keeps the role and
// history ID it had: the wire groups parts by role, and the replacement is the
// same slot in the record.
func TestFitInputModalitiesKeepsMeta(t *testing.T) {
	img := &ImagePart{URI: "data:image/png;base64,QUJD"}
	img.HistoryID = 7
	img.Role = RoleUser

	out := FitInputModalities([]ContentPart{img}, []string{"text"})

	note, ok := out[0].(*TextPart)
	if !ok {
		t.Fatalf("expected a text note, got %T", out[0])
	}
	if note.GetHistoryID() != 7 || note.GetRole() != RoleUser {
		t.Errorf("meta not preserved: id=%d role=%v", note.GetHistoryID(), note.GetRole())
	}
	if strings.Contains(note.Text, "QUJD") {
		t.Errorf("the note must not echo the base64 body: %s", note.Text)
	}
}
