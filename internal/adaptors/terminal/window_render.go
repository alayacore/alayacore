package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
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
func (wb *WindowBuffer) rebuildOneWindow(idx int) {
	if idx < 0 || idx >= len(wb.Windows) {
		return
	}
	w := wb.Windows[idx]

	// Ensure lineHeights has right length (new window case)
	for len(wb.lineHeights) < len(wb.Windows) {
		wb.lineHeights = append(wb.lineHeights, 0)
	}

	// Re-render the dirty window (don't use totalLines from renderAndCacheWindow)
	styled := wb.renderAndCacheWindow(idx, w)
	wb.lineHeights[idx] = strings.Count(styled, "\n") + 1

	// Rebuild totalLines from all lineHeights
	wb.totalLines = 0
	for _, h := range wb.lineHeights {
		wb.totalLines += h
	}

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
	return styled
}

// renderWithCursor renders all windows with cursor highlighting on the specified window.
// For the cursor window: reuse cachedInnerContent, only swap border style (no lipgloss.Wrap).
func (wb *WindowBuffer) renderWithCursor(cursorIndex int) string {
	var sb strings.Builder

	for i, w := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Non-cursor window: use cached render if valid
		if i != cursorIndex {
			if w.cachedRender != "" && w.cachedWidth == wb.width &&
				(w.IsDiffWindow() || len(w.Content) == w.lastContentLen) {
				sb.WriteString(w.cachedRender)
				continue
			}
			// Fallback: re-render and cache
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := w.Style.Width(wb.width).Render(innerContent)
			w.cachedRender = styled
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			sb.WriteString(styled)
			continue
		}

		// Cursor window: border swap - reuse cachedInnerContent, avoid renderWindowContent
		if w.cachedInnerContent != "" && w.cachedWidth == wb.width &&
			(w.IsDiffWindow() || len(w.Content) == w.lastContentLen) {
			sb.WriteString(wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent))
		} else {
			innerWidth := max(0, wb.width-4)
			innerContent := wb.renderWindowContent(w, innerWidth)
			styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
			w.cachedRender = w.Style.Width(wb.width).Render(innerContent) // cache dimmed for next time
			w.cachedInnerContent = innerContent
			w.cachedWidth = wb.width
			w.lastContentLen = len(w.Content)
			sb.WriteString(styled)
		}
	}
	return sb.String()
}

// renderWindowContent renders the content of a window (wrapping, truncation for wrapped mode)
func (wb *WindowBuffer) renderWindowContent(w *Window, innerWidth int) string {
	// Handle diff windows
	if w.IsDiffWindow() {
		return wb.renderDiffContent(w.Diff, innerWidth)
	}

	if w.Wrapped {
		// In wrapped mode, show up to 5 lines
		wrappedContent := lipgloss.Wrap(w.Content, innerWidth, " ")

		// Check if content spans more than 5 lines (needs truncation)
		lines := strings.Split(wrappedContent, "\n")
		if len(lines) > 5 {
			// Show: first line, dotted separator, last 3 lines (5 lines total)
			firstLine := lines[0]
			lastThreeLines := lines[len(lines)-3:]

			// Create subtle dotted separator across full width
			wrapIndicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorDim)).
				Render(strings.Repeat("·", innerWidth))

			// Show first line, separator, last 3 lines
			return firstLine + "\n" + wrapIndicator + "\n" + strings.Join(lastThreeLines, "\n")
		}
		// Content fits in 5 lines or less, just show wrapped content
		return wrappedContent
	}
	return lipgloss.Wrap(w.Content, innerWidth, " ")
}
