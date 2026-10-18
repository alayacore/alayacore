package terminal

// Window renderers: type-specific content management and rendering.
//
// Each renderer implements WindowRendering and owns its content storage and
// caching, and the Window struct delegates to it for everything that varies by
// window type — window.go lists them and the frames each one serves. One file
// each, because a renderer is one thing:
//
//	text_renderer.go   textRenderer: the one that can re-read content as markdown
//	user_renderer.go   userRenderer, and the attachment badges it draws
//	tool_renderer.go   toolRenderer, and the leading-"…" previews only it cuts
//	folded_summary.go  fitting any of them into one folded row
//
// What is left here is the one helper two of them share: styleBodyLines.

import (
	"strings"
)

// styleBodyLines applies styles.Body to the plain (ANSI-free) rows of a
// window's inner content. Rows already carrying SGR styling (separators,
// diff rows, media badges) are left untouched — under an overlay those
// styles already resolve to ColorDim via Styles.Dimmed, so applying Body
// again would only nest redundant sequences. When styles.Body has no
// foreground (normal mode) the lines are returned unchanged, keeping
// body text in the terminal's default color.
func styleBodyLines(lines []visualLine, styles *Styles) []visualLine {
	if styles == nil || styles.Body.GetForeground() == nil {
		return lines
	}
	out := make([]visualLine, len(lines))
	for i, l := range lines {
		if strings.Contains(l.Text, "\x1b[") {
			out[i] = l
			continue
		}
		out[i] = visualLine{Text: styles.Body.Render(l.Text), Pad: l.Pad, Cont: l.Cont}
	}
	return out
}
