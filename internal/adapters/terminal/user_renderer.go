package terminal

// userRenderer draws a user message: the prompt text, the media attachments that
// came with it, and the folded form of both.
//
// compactMediaSummary is what keeps a folded user window honest about its
// attachments — duplicate kinds are counted ("📷2") rather than merged into one
// badge. foldedRow is that folded line as it will be drawn: the runs that
// produced it travel with it, so the styler never has to re-derive where each
// run starts from a row truncation has already shortened.

import (
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/wire"
)

// compactMediaSummary renders attachment labels as a compact single-line
// summary. Duplicate media types are counted (for example, "📷1 🎵1") so a
// folded window does not hide the fact that more than one attachment is
// present.
func compactMediaSummary(mediaParts []string) string {
	counts := make(map[string]int, len(mediaParts))
	order := make([]string, 0, len(mediaParts))
	seen := make(map[string]struct{}, len(mediaParts))

	for _, label := range mediaParts {
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			counts[label]++
			continue
		}
		seen[label] = struct{}{}
		counts[label] = 1
		order = append(order, label)
	}

	parts := make([]string, 0, len(order))
	for _, label := range order {
		parts = append(parts, compactMediaIcon(label)+strconv.Itoa(counts[label]))
	}
	return strings.Join(parts, " ")
}

// compactMediaIcon returns the compact icon used by compactMediaSummary.
// Known labels are explicit; unknown labels use their first token.
func compactMediaIcon(label string) string {
	switch label {
	case wire.MediaLabel(wire.KindImage):
		return "📷"
	case wire.MediaLabel(wire.KindVideo):
		return "🎬"
	case wire.MediaLabel(wire.KindAudio):
		return "🎵"
	case wire.MediaLabel(wire.KindDocument):
		return "📄"
	}
	if i := strings.IndexByte(label, ' '); i >= 0 {
		return label[:i]
	}
	return label
}

// ============================================================================
// userRenderer — User messages with optional media attachments (UT)
// ============================================================================

// userRenderer handles user messages that may include media attachments.
// Text parts and media labels are stored separately and combined at render time.
type userRenderer struct {
	textParts  []string // user text, in order
	mediaParts []string // media labels, in order
	contentLen int
}

func (r *userRenderer) Tag() string { return tagUserText }

func (r *userRenderer) ToolInfo() *ToolInfo { return nil }

func (r *userRenderer) AppendFromFrame(tag string, value string) {
	value = sanitizeUTF8(value)
	switch tag {
	case tagUserText:
		if value != "" {
			r.textParts = append(r.textParts, value)
		}
	case tagUserImage, tagUserVideo, tagUserAudio, tagUserDoc:
		r.mediaParts = append(r.mediaParts, mediaLabel(tag))
	}
	r.contentLen += len(value)
}

func (r *userRenderer) Invalidate() {}

// BuildInner renders the user message as visual lines: media section
// first (on top), then text below. This matches the natural content
// order: media parts precede the text part. The two need no divider
// between them — the media block is the window's own header material
// (default color, bold, four fixed labels), the text under it is plain,
// and the window's line above already opened the block; a rule there would
// delimit a boundary nobody mistakes. Multiple text parts ARE separated
// with "───" (Separator) in System color, because two text parts carry
// identical styling and nothing else tells them apart. Each returned line
// is one terminal row (no '\n' inside); lineCount is the content rows plus
// the window's own line (len(lines) + 1).
//
// Wrapping is performed by wrapVisualLines (NOT by a pre-pass of
// wrapContent): a pre-pass would insert hard '\n' at wrap points and
// wrapVisualLines would then mistake them for original-line breaks,
// folding soft-wrap semantics. Only genuinely-long SINGLE lines
// use soft-wrap (their continuation rows join without '\n'); ordinary
// multi-line content stays multi-line.
func (r *userRenderer) BuildInner(width int, _ bool, styles *Styles) ([]visualLine, int) {
	innerWidth := max(0, width)

	var parts []string

	// Media portion — rendered first (on top)
	if len(r.mediaParts) > 0 {
		mediaBlockStr := wrapLabels(r.mediaParts, innerWidth, styles.Attachment)
		parts = append(parts, mediaBlockStr)
	}

	// Text portion: text parts separated by Separator ("───")
	if len(r.textParts) > 0 {
		var textBlock strings.Builder

		firstText := true
		for _, part := range r.textParts {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if !firstText {
				textBlock.WriteString("\n")
				textBlock.WriteString(styles.System.Render(Separator))
				textBlock.WriteString("\n")
			}
			textBlock.WriteString(trimmed) // user text is plain (no bold/color); overlay dimming is applied later via styleBodyLines
			firstText = false
		}

		if textBlock.Len() > 0 {
			parts = append(parts, textBlock.String())
		}
	}

	result := strings.Join(parts, "\n")

	// Wrap into visual rows with continuation marks: rows of the same
	// original line join without '\n' (soft wrap); rows starting a new
	// original line are separated by hard '\n'.
	lines := wrapVisualLines(result, innerWidth)

	// Body color for the plain text rows (overlay dimming); styled rows
	// (media badges, separators) keep their own styles.
	return styleBodyLines(lines, styles), len(lines) + 1
}

// BuildFolded returns the single-line folded form: the label column, then a
// head + "…" + tail summary of the content — "USER PROMPT 📷2 the head of the
// prompt…its tail" — cut to fit width minus the arrow. The content summarized is
// the media badges followed by the text (compactMediaSummary, "📷2 📄1"), so the
// badges land in the head and stay visible however long the prompt is. A media-only
// message has no text after them, and shows the attachment types and their counts
// in the available width.
//
// Truncation markers ("…") are rendered with styles.Status to visually
// separate them from the actual content (muted).
func (r *userRenderer) BuildFolded(width int, styles *Styles) (string, int) {
	textContent := prepareContent(strings.Join(r.textParts, "\n"))
	textContent = strings.TrimSpace(textContent)
	mediaSummary := compactMediaSummary(r.mediaParts)
	if textContent == "" && mediaSummary == "" {
		return "", 1
	}

	label := padLabel("USER PROMPT")
	room := max(0, width-foldedPrefixWidth-cellWidth(label))

	// Combine media + text into a single content string and run head+tail
	// truncation on it (same rule as all other non-delta text windows).
	// The "…" naturally falls between media and text when truncation cuts
	// there, and never appears at the line start — only streaming delta
	// content uses leading "…".
	var content string
	switch {
	case mediaSummary != "" && textContent != "":
		content = mediaSummary + " " + textContent
	case mediaSummary != "":
		content = mediaSummary
	default:
		content = textContent
	}
	head, tail, truncated := headAndTailParts(priceSummary(content), room)

	plainLine := label + head
	if tail != "" {
		plainLine += "…" + tail
	}
	line := truncateWithSuffix(plainLine, max(0, width-foldedPrefixWidth)) // safety net

	return foldedRow{
		line: line, label: label, head: head, tail: tail,
		truncated: truncated, media: mediaSummary,
	}.style(styles), 1
}

// foldedRow is a user window's folded line: the plain row as it will be
// drawn, plus the runs that produced it. userRenderer.BuildFolded decides
// where to cut, foldedRow.style decides which run each byte belongs to, and
// the second question can only be answered from the first one's result — so
// the row travels with its parts instead of the styler re-deriving them from
// the content. (Splitting the two is also what fits BuildFolded back into
// the complexity budget it had outgrown.)
type foldedRow struct {
	line      string // plain, already truncated to the row's width
	label     string // the padded label column; a prefix of line
	head      string // content kept from the front, after the label
	tail      string // content kept from the back; "" when nothing was cut
	truncated bool   // head+tail truncation cut into the content
	media     string // the badge summary as it stood before truncation
}

// style paints the row's runs: the label in the window line's register, the
// badge run in the attachment style, the text muted, "…" dim.
//
// The badges take styles.Attachment — the one style the unfolded body draws
// them in — because the folded row and the unfolded block show the same
// tokens, and folding must not restyle them (toolNameStyle, window.go, is the
// same lesson from the other side). The summary is a prefix of the content, so
// a prefix test on the plain line locates it exactly; when truncation cut into
// the badge run itself there is no intact run to paint, and the summary stays
// in the content's muted color rather than showing half a badge in bold.
func (row foldedRow) style(styles *Styles) string {
	labelStyle := lineStyleForTag(tagUserText, styles)
	if len(row.line) <= len(row.label) {
		return labelStyle.Render(row.line)
	}
	var rendered strings.Builder
	rendered.WriteString(labelStyle.Render(row.line[:len(row.label)]))

	rest := row.line[len(row.label):]
	mediaRun := 0
	if strings.HasPrefix(rest, row.media) {
		mediaRun = min(len(row.media), len(row.head))
	}
	if mediaRun > 0 {
		rendered.WriteString(styles.Attachment.Render(rest[:mediaRun]))
		rest = rest[mediaRun:]
		row.head = row.head[mediaRun:]
	}

	if !row.truncated || row.tail == "" {
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String()
	}
	// head + "…" + tail, now that the badge run is off the front. The "…"
	// might have been cut by the safety-net truncation.
	headEnd := len(row.head)
	if headEnd > len(rest) {
		// head was truncated mid-way — fall back to muted
		rendered.WriteString(styles.System.Render(rest))
		return rendered.String()
	}
	rendered.WriteString(styles.System.Render(rest[:headEnd]))
	ell := len("…")
	if headEnd+ell > len(rest) {
		// "…" was cut — render the rest as muted
		rendered.WriteString(styles.System.Render(rest[headEnd:]))
		return rendered.String()
	}
	rendered.WriteString(styles.Status.Render(rest[headEnd : headEnd+ell]))
	if headEnd+ell < len(rest) {
		rendered.WriteString(styles.System.Render(rest[headEnd+ell:]))
	}
	return rendered.String()
}

// ============================================================================
// toolRenderer — Tool calls and results (AF, UF)
// ============================================================================
