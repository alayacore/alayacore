package terminal

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestPromptInputAttachmentsOffset verifies AttachmentsOffset reports the
// number of content lines above the input text (attachment lines + separator).
func TestPromptInputAttachmentsOffset(t *testing.T) {
	styles := DefaultStyles()
	p := NewPromptInput(styles)

	if off := p.AttachmentsOffset(); off != 0 {
		t.Fatalf("no attachments: got %d, want 0", off)
	}

	p = p.WithAttachments([]string{"a.txt"})
	if off := p.AttachmentsOffset(); off != 2 {
		t.Fatalf("one attachment: got %d, want 2 (1 media line + separator)", off)
	}

	p = p.WithAttachments([]string{"a.txt", "b.txt", "c.txt", "d.txt"})
	if off := p.AttachmentsOffset(); off != 2 {
		t.Fatalf("multiple short attachments: got %d, want 2", off)
	}

	// A long label wraps to multiple lines, increasing the offset.
	long := "very-long-attachment-name-0123456789-abcdefghijklmnopqrstuvwxyz.txt"
	p = p.WithAttachments([]string{long})
	innerWidth := max(0, p.width)
	styledMedia := wrapLabels(p.Attachments(), innerWidth, p.styles.Attachment)
	want := lipgloss.Height(styledMedia) + 1 // + separator line
	if off := p.AttachmentsOffset(); off != want {
		t.Fatalf("long attachment: got %d, want %d", off, want)
	}
}
