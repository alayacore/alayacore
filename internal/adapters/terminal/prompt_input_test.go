package terminal

import (
	"strings"
	"testing"
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
	want := Height(styledMedia) + 1 // + separator line
	if off := p.AttachmentsOffset(); off != want {
		t.Fatalf("long attachment: got %d, want %d", off, want)
	}
}

// TestWrapLabelsSingleOversizeLabel guards against the regression where a
// single label wider than the box width was emitted on one line — the
// terminal would soft-wrap it on render, so the displayed row count
// exceeded what wrapLabels / Height() / AttachmentsOffset() reported,
// pushing the prompt input box and cursor out of position.
func TestWrapLabelsSingleOversizeLabel(t *testing.T) {
	styles := DefaultStyles()
	for _, tc := range []struct {
		name        string
		width       int
		labelLen    int
		wantRows    int
		wantEachRow int // max display width of each emitted row (≤ width)
	}{
		{"label fits", 40, 30, 1, 30},
		{"label equals width", 40, 40, 1, 40},
		{"label one over", 40, 41, 2, 40},
		{"label two cells over", 40, 42, 2, 40},
		{"label three cells over", 40, 43, 2, 40},
		{"label spans three rows", 40, 100, 3, 40},
		{"label spans many rows", 40, 400, 10, 40},
		{"narrow box, big label", 60, 200, 4, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label := strings.Repeat("x", tc.labelLen)
			got := wrapLabels([]string{label}, tc.width, styles.Attachment)
			gotRows := strings.Count(got, "\n") + 1
			if gotRows != tc.wantRows {
				t.Fatalf("rows: got %d, want %d (output=%q)", gotRows, tc.wantRows, got)
			}
			for i, line := range strings.Split(got, "\n") {
				w := cellWidth(line)
				if w > tc.wantEachRow {
					t.Fatalf("row %d width %d > %d: %q", i, w, tc.wantEachRow, line)
				}
			}
		})
	}
}

// TestPromptInputAttachmentOversizeAlignment verifies the end-to-end
// invariant: when an attachment path is wider than the input box, the
// attachment's rendered row count matches Height() and AttachmentsOffset()
// — the prompt input box top rule therefore lands on the row the cursor
// computation expects, even though the attachment path was longer than
// the box width (the case that previously caused misalignment).
func TestPromptInputAttachmentOversizeAlignment(t *testing.T) {
	styles := DefaultStyles()
	p := NewPromptInput(styles).WithWidth(40)

	longPath := "/very/long/path/that/exceeds/the/box/width/by/lots/file.txt"
	if len(longPath) <= 40 {
		t.Fatalf("test setup: path %d must exceed width 40", len(longPath))
	}
	p = p.WithAttachments([]string{longPath})

	// What the terminal will actually render: top rule + (hard-wrapped
	// media rows) + separator + input field + bottom rule.
	expectedTerminalRows := 1 + (len(longPath)+40-1)/40 + 1 + 1 + 1
	if h := p.Height(); h != expectedTerminalRows {
		t.Fatalf("Height(): got %d, want %d (oversize path must be pre-wrapped so "+
			"computed row count matches terminal row count)", h, expectedTerminalRows)
	}

	// And AttachmentsOffset must point at the input field row.
	wantOff := (len(longPath)+40-1)/40 + 1
	if off := p.AttachmentsOffset(); off != wantOff {
		t.Fatalf("AttachmentsOffset(): got %d, want %d", off, wantOff)
	}
}
