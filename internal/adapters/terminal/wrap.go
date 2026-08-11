package terminal

// Line wrapping and truncation utilities for window content rendering.
// These functions handle wrapping styled content at display width
// boundaries while preserving ANSI styles across line breaks, and
// display-width-aware truncation with a "…" suffix.
//
// Used by Window.renderer.BuildInner, tool_render.go
// (RenderDiffContent), model_selector.go, help_window.go,
// theme_selector.go, prompt_input.go, and tests.

import (
	"bytes"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// wrapContent wraps styled content at the given display width, preserving
// ANSI styles across line breaks. Updates the wrapping strategy here to
// change how all window content is wrapped.
func wrapContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Step 1: hard-wrap at character boundaries (like a terminal)
	s = ansi.Hardwrap(s, width, true)
	// Step 2: re-apply ANSI styles after each inserted newline
	var buf bytes.Buffer
	w := lipgloss.NewWrapWriter(&buf)
	defer w.Close()
	_, _ = io.WriteString(w, s) // bytes.Buffer.Write never fails
	return buf.String()
}

// wrapLines wraps content into lines at the given width.
func wrapLines(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := wrapContent(content, width)
	return strings.Split(wrapped, "\n")
}

// appendDeltaToLines incrementally wraps a delta onto existing lines.
func appendDeltaToLines(lines []string, delta string, width int) []string {
	if len(lines) == 0 {
		return wrapLines(delta, width)
	}
	if width <= 0 {
		lines[len(lines)-1] += delta
		return lines
	}

	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlines(lines, delta, width)
	}

	// Append to last line and rewrap
	lastLine := lines[len(lines)-1]
	combined := lastLine + delta
	newLines := wrapLines(combined, width)
	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	parts := strings.Split(delta, "\n")
	for i, part := range parts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastIdx := len(lines) - 1
				combined := lines[lastIdx] + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:lastIdx], newLines...)
			}
		} else {
			lines = append(lines, wrapLines(part, width)...)
		}
	}
	return lines
}

// styleMultiline applies a style to each line of text.
func styleMultiline(content string, style lipgloss.Style) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// wrapLabels wraps a list of labels at word boundaries (separator "  "),
// keeping each label intact. Each resulting line is styled with the given style.
func wrapLabels(labels []string, width int, style lipgloss.Style) string {
	if len(labels) == 0 {
		return ""
	}
	var lines []string
	var currentLine strings.Builder

	for i, label := range labels {
		if label == "" {
			continue
		}
		labelWidth := ansi.StringWidth(label)
		if currentLine.Len() > 0 {
			currentWidth := ansi.StringWidth(currentLine.String())
			sepWidth := 2 // "  "
			if currentWidth+sepWidth+labelWidth > width {
				lines = append(lines, style.Render(currentLine.String()))
				currentLine.Reset()
				currentLine.WriteString(label)
			} else {
				currentLine.WriteString("  ")
				currentLine.WriteString(label)
			}
		} else {
			currentLine.WriteString(label)
		}
		// Flush last label
		if i == len(labels)-1 && currentLine.Len() > 0 {
			lines = append(lines, style.Render(currentLine.String()))
		}
	}

	return strings.Join(lines, "\n")
}

// truncateWithSuffix truncates content to fit within maxWidth, appending "…"
// to indicate content has been cut. The result is guaranteed to be at most
// maxWidth display columns wide, provided the input contains no unexpanded
// tabs — ansi.Hardwrap counts a tab as 0 width while terminals render it as
// TabWidth columns. Callers must expandTabs (see tool_render.go) before
// truncating content that may contain tabs.
func truncateWithSuffix(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	truncated := ansi.Hardwrap(content, maxWidth, true)
	if truncated == content {
		return content
	}

	if maxWidth == 1 {
		return "…"
	}

	inner := ansi.Hardwrap(content, maxWidth-1, true)
	return strings.SplitN(inner, "\n", 2)[0] + "…"
}
