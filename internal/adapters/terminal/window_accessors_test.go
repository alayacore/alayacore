package terminal

// Accessors the tests need and the program does not.
//
// These live here rather than in window.go / window_buffer.go so that the
// shipped package carries no test-only surface: nothing outside a test calls
// them, and a reader of the production files should not have to work out which
// exported methods are real. A method may be declared for a type in any file of
// its package, and _test.go files are part of the package in the test build.

// RawStatus returns the tool status for testing.
func (w *Window) RawStatus() ToolStatus {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.status
	}
	return ToolStatusNone
}

// RawToolName returns the tool name for testing.
func (w *Window) RawToolName() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.name
	}
	return ""
}

// RawTag returns the frame tag for testing.
func (w *Window) RawTag() string {
	return w.Tag()
}

// RawDelta returns the current tool delta buffer for testing.
// Returns empty string for non-tool windows.
func (w *Window) RawDelta() string {
	if tr, ok := w.renderer.(*toolRenderer); ok {
		return tr.deltaBuffer
	}
	return ""
}

// RenderWindowContent renders the content of a window (for testing).
func (wb *WindowBuffer) RenderWindowContent(w *Window, innerWidth int) string {
	if w.renderer == nil {
		return ""
	}
	// Use BuildInner to get the rendered content lines — the body only,
	// without the window's own line (row 0) that Render composes above it.
	lines, _ := w.renderer.BuildInner(innerWidth, false, wb.styles)
	return joinVisualLines(lines)
}
