package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// GetAll returns the concatenated rendered windows as a single string.
// Uses virtual rendering when viewportHeight > 0, otherwise falls back to full render.
func (wb *WindowBuffer) GetAll(cursorIndex int) string {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Use virtual rendering if enabled
	if wb.viewportHeight > 0 {
		return wb.getVirtualRender(cursorIndex)
	}

	// Fallback: full render (for backwards compatibility)
	if wb.dirtyIndex != -1 {
		if wb.dirtyIndex == fullRebuild {
			wb.rebuildCache()
		} else {
			wb.rebuildOneWindow(wb.dirtyIndex)
		}
		wb.dirtyIndex = -1
	}

	// If no cursor or cursor out of range, return cached render
	if cursorIndex < 0 || cursorIndex >= len(wb.Windows) {
		return wb.cachedRender
	}

	// Cursor is active - rebuild with cursor highlighting on just that window
	// We use the cached wrapped content but apply different border style
	return wb.renderWithCursor(cursorIndex)
}

// rebuildCache rebuilds the cached render for all windows (without cursor)
func (wb *WindowBuffer) rebuildCache() {
	var sb strings.Builder
	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}
		styled := wb.renderAndCacheWindow(i, w)
		sb.WriteString(styled)
	}
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}
	wb.cachedRender = sb.String()
}

// rebuildOneWindow re-renders only the window at idx and updates the full cached string.
// Uses incremental update for totalLines (O(1)) instead of summing all line heights.
func (wb *WindowBuffer) rebuildOneWindow(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	// Ensure lineHeights has right length (new window case)
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Save old line height for incremental update
	oldLineHeight := wb.lineHeights[idx]

	// Re-render the dirty window
	styled := wb.renderAndCacheWindow(idx, w)
	newLineHeight := strings.Count(styled, "\n") + 1
	wb.lineHeights[idx] = newLineHeight

	// Incremental update of totalLines (O(1))
	wb.totalLines += newLineHeight - oldLineHeight

	// Rebuild cachedRender by concatenating: [before] + [new] + [after]
	var sb strings.Builder
	for i := 0; i < len(wb.Windows); i++ {
		if i > 0 {
			sb.WriteString("\n")
		}
		if i == idx {
			sb.WriteString(styled)
		} else {
			sb.WriteString(wb.Windows[i].cachedRender)
		}
	}
	wb.cachedRender = sb.String()
}

// renderAndCacheWindow renders a window, updates its cache and lineHeights[i], returns styled string.
// Stores cachedInnerContent for cursor border swap (avoid re-calling renderWindowContent).
func (wb *WindowBuffer) renderAndCacheWindow(i int, w *Window) string {
	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)
	styled := w.Style.Width(wb.width).Render(innerContent)
	lineCount := strings.Count(styled, "\n") + 1

	if i < len(wb.lineHeights) {
		wb.lineHeights[i] = lineCount
	}
	w.cachedRender = styled
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped
	return styled
}

// isCacheValid checks if a window's cache is valid for the current width
func (wb *WindowBuffer) isCacheValid(w *Window) bool {
	if w.cachedWidth != wb.width {
		return false
	}
	if w.IsDiffWindow() {
		return w.Wrapped == w.lastWrapped
	}
	return len(w.Content) == w.lastContentLen
}

// renderWindowWithStyle renders a window's content and caches it with the given style
func (wb *WindowBuffer) renderWindowWithStyle(w *Window, style lipgloss.Style) string {
	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)
	styled := style.Width(wb.width).Render(innerContent)

	w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	w.lastWrapped = w.Wrapped

	return styled
}

// renderNonCursorWindow renders a non-cursor window, using cache if valid
func (wb *WindowBuffer) renderNonCursorWindow(w *Window) string {
	if w.cachedRender != "" && wb.isCacheValid(w) {
		return w.cachedRender
	}
	return wb.renderWindowWithStyle(w, w.Style)
}

// renderCursorWindow renders the cursor-highlighted window
func (wb *WindowBuffer) renderCursorWindow(w *Window) string {
	if w.cachedInnerContent != "" && wb.isCacheValid(w) {
		return wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent)
	}
	return wb.renderWindowWithStyle(w, wb.cursorStyle)
}

// renderWithCursor renders all windows with cursor highlighting on the specified window.
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		if i != cursorIndex {
			sb.WriteString(wb.renderNonCursorWindow(w))
		} else {
			sb.WriteString(wb.renderCursorWindow(w))
		}
	}
	return sb.String()
}

// renderWindowContent renders the content of a window (wrapping, truncation for wrapped mode)
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	// Handle diff windows
	if w.IsDiffWindow() {
		// Render diff content
		fullContent := wb.renderDiffContent(w.Diff, innerWidth, w.Status)

		// Apply folding if window is wrapped
		if w.Wrapped {
			lines := strings.Split(fullContent, "\n")
			if len(lines) > 5 {
				// Show: first line, tricolon separator, last 3 lines (5 lines total)
				firstLine := lines[0]
				lastThreeLines := lines[len(lines)-3:]

				// Create full-width tricolon separator with border color
				wrapIndicator := lipgloss.NewStyle().
					Foreground(wb.styles.ColorBase).
					Render(strings.Repeat("⁝", innerWidth))

				// Show first line, separator, last 3 lines
				return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
			}
		}
		return fullContent
	}

	// Build content with optional status indicator
	content := w.Content
	if w.Tag == stream.TagFunctionNotify {
		// Tool windows always have a status indicator
		var indicator string
		switch w.Status {
		case statusSuccess:
			// Green filled dot
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorSuccess).
				Render("• ")
		case statusError:
			// Red filled dot
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorError).
				Render("• ")
		case statusPending:
			// Dimmed filled dot for pending
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorDim).
				Render("• ")
		default:
			// Default: dimmed hollow dot (for loaded sessions without status)
			indicator = lipgloss.NewStyle().
				Foreground(wb.styles.ColorDim).
				Render("· ")
		}
		content = indicator + content
	}

	// Get or build wrapped lines (incremental wrap optimization)
	lines := w.getOrBuildLines(content, innerWidth)

	if w.Wrapped && len(lines) > 5 {
		// In wrapped mode with > 5 lines, show: first line, separator, last 3 lines
		firstLine := lines[0]
		lastThreeLines := lines[len(lines)-3:]

		// Create full-width tricolon separator with border color
		wrapIndicator := lipgloss.NewStyle().
			Foreground(wb.styles.ColorBase).
			Render(strings.Repeat("⁝", innerWidth))

		return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
	}

	// Normal mode or fits in 5 lines: return all lines joined
	return strings.Join(lines, "\n")
}
