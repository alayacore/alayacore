package terminal

import (
	"strings"
)

// ============================================================================
// Virtual Rendering - only render visible windows
// ============================================================================

// SetViewportPosition updates the viewport scroll position and height.
func (wb *WindowBuffer) SetViewportPosition(yOffset, height int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.viewportYOffset = yOffset
	wb.viewportHeight = height
}

// GetTotalLinesVirtual returns total lines, ensuring lineHeights are calculated.
func (wb *WindowBuffer) GetTotalLinesVirtual() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.ensureLineHeights()
	return wb.totalLines
}

// ensureLineHeights calculates lineHeights if needed (must be called with lock held).
func (wb *WindowBuffer) ensureLineHeights() {
	if wb.dirtyIndex == -1 && len(wb.lineHeights) == len(wb.Windows) {
		return // Already calculated and clean
	}

	wb.lineHeights = make([]int, len(wb.Windows))
	wb.totalLines = 0

	innerWidth := max(0, wb.width-4)
	for i, w := range wb.Windows {
		innerContent := wb.renderWindowContent(w, innerWidth)
		styled := w.Style.Width(wb.width).Render(innerContent)
		lineCount := strings.Count(styled, "\n") + 1

		wb.lineHeights[i] = lineCount
		wb.totalLines += lineCount

		// Cache for later use
		w.cachedRender = styled
		w.cachedInnerContent = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
	}
	wb.dirtyIndex = -1
}

// getVirtualRender returns rendered content using virtual rendering.
// Only renders windows in the visible range, using empty lines for others.
// Must be called with wb.mu locked.
func (wb *WindowBuffer) getVirtualRender(cursorIndex int) string {
	wb.ensureLineHeights()

	if len(wb.Windows) == 0 {
		return ""
	}

	// Calculate visible window range
	bufferWindows := 5 // Extra windows above/below viewport for smooth scrolling
	viewportLines := wb.viewportHeight
	if viewportLines < 10 {
		viewportLines = 10
	}

	startLine := wb.viewportYOffset - viewportLines
	if startLine < 0 {
		startLine = 0
	}
	endLine := wb.viewportYOffset + wb.viewportHeight + viewportLines

	startWindow := wb.findWindowAtLine(startLine)
	endWindow := wb.findWindowAtLine(endLine)

	// Extend range by buffer windows
	startWindow = max(0, startWindow-bufferWindows)
	endWindow = min(len(wb.Windows)-1, endWindow+bufferWindows)

	// Build output - need exactly totalLines lines for proper viewport scrolling
	var sb strings.Builder

	for i := range wb.Windows {
		if i > 0 {
			sb.WriteString("\n")
		}

		if i >= startWindow && i <= endWindow {
			// Render actual content
			styled := wb.renderWindowCached(i, cursorIndex == i)
			sb.WriteString(styled)
		} else {
			// Placeholder - empty line(s) to maintain line count
			lineCount := wb.lineHeights[i]
			for j := 0; j < lineCount; j++ {
				if j > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(" ")
			}
		}
	}

	return sb.String()
}

// findWindowAtLine returns the window index containing the given line.
func (wb *WindowBuffer) findWindowAtLine(line int) int {
	currentLine := 0
	for i, h := range wb.lineHeights {
		if currentLine+h > line {
			return i
		}
		currentLine += h
	}
	return len(wb.Windows) - 1
}

// renderWindowCached renders a single window, using cache if valid.
func (wb *WindowBuffer) renderWindowCached(i int, isCursor bool) string {
	w := wb.Windows[i]

	// Check cache validity
	cacheValid := w.cachedRender != "" && w.cachedWidth == wb.width &&
		(w.IsDiffWindow() || len(w.Content) == w.lastContentLen)

	if cacheValid {
		if isCursor {
			return wb.cursorStyle.Width(wb.width).Render(w.cachedInnerContent)
		}
		return w.cachedRender
	}

	// Re-render
	innerWidth := max(0, wb.width-4)
	innerContent := wb.renderWindowContent(w, innerWidth)

	if isCursor {
		styled := wb.cursorStyle.Width(wb.width).Render(innerContent)
		// Cache non-cursor version
		w.cachedRender = w.Style.Width(wb.width).Render(innerContent)
		w.cachedInnerContent = innerContent
		w.cachedWidth = wb.width
		w.lastContentLen = len(w.Content)
		return styled
	}

	styled := w.Style.Width(wb.width).Render(innerContent)
	w.cachedRender = styled
	w.cachedInnerContent = innerContent
	w.cachedWidth = wb.width
	w.lastContentLen = len(w.Content)
	return styled
}
