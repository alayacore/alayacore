package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/tlv"
)

func TestCompactMediaSummary(t *testing.T) {
	labels := []string{
		tlv.MediaLabel(tlv.TagUserI),
		tlv.MediaLabel(tlv.TagUserA),
		tlv.MediaLabel(tlv.TagUserI),
		tlv.MediaLabel(tlv.TagUserD),
		tlv.MediaLabel(tlv.TagUserA),
	}

	got := compactMediaSummary(labels)
	want := "📷2 🎵2 📄1"
	if got != want {
		t.Errorf("compactMediaSummary() = %q, want %q", got, want)
	}
}

func TestUserPromptCollapsedMediaSummary(t *testing.T) {
	styles := DefaultStyles()
	ur := &userRenderer{
		textParts: []string{"analyze this"},
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserI),
		},
	}

	line, count := ur.BuildCollapsed(100, styles)
	if count != 1 {
		t.Fatalf("collapsed lineCount = %d, want 1", count)
	}
	want := "USER PROMPT 📷2 🎵1 analyze this"
	if got := stripANSI(line); got != want {
		t.Errorf("BuildCollapsed() = %q, want %q", got, want)
	}
}

func TestUserPromptCollapsedMediaOnlySummary(t *testing.T) {
	ur := &userRenderer{
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserV),
		},
	}

	line, _ := ur.BuildCollapsed(30, DefaultStyles())
	want := "USER PROMPT 📷2 🎵1 🎬1"
	if got := stripANSI(line); got != want {
		t.Errorf("BuildCollapsed() = %q, want %q", got, want)
	}
	if width := ansi.StringWidth(stripANSI(line)); width > 28 {
		t.Errorf("collapsed media summary width = %d, want <= 28", width)
	}
}

func TestUserPromptCollapsedMediaSummaryFitsAndPrioritizesMedia(t *testing.T) {
	ur := &userRenderer{
		textParts: []string{strings.Repeat("long text ", 10)},
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserV),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserD),
		},
	}

	line, _ := ur.BuildCollapsed(30, DefaultStyles())
	plain := stripANSI(line)
	if !strings.Contains(plain, "📷1") {
		t.Errorf("collapsed media summary should retain the image badge, got %q", plain)
	}
	if width := ansi.StringWidth(plain); width > 28 {
		t.Errorf("collapsed media summary width = %d, want <= 28", width)
	}
	if strings.Contains(plain, "\n") {
		t.Errorf("collapsed media summary must remain one line, got %q", plain)
	}
}
