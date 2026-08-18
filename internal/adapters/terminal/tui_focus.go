package terminal

// Focus management: input/display focus switching, blur/focus handling,
// and the Terminal-level paste dispatcher that routes PasteMsg into the
// focused input field. Per-input-field paste handling lives in
// input_field.go's InputField.handlePaste.
//
// Extracted from tui.go.

// toggleFocus switches between display and input windows.
func (m Terminal) toggleFocus() Terminal {
	if m.focusedWindow == focusDisplay {
		m = m.focusInput()
	} else {
		m = m.focusDisplay()
	}
	m.display = m.display.updateContent()
	return m
}

// focusInput switches focus to the input window.
func (m Terminal) focusInput() Terminal {
	m.focusedWindow = focusInput
	m.display = m.display.WithDisplayFocused(false)
	m.input = m.input.Focus()
	return m
}

// focusDisplay switches focus to the display window.
func (m Terminal) focusDisplay() Terminal {
	m.focusedWindow = focusDisplay
	m.display = m.display.WithDisplayFocused(true)
	m.input = m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display = m.display.WithCursorToLastWindow()
	}
	return m
}

// openModelSelector opens the model selector UI.
func (m Terminal) openModelSelector() Terminal {
	m.modelSelector = m.modelSelector.Open()
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m Terminal) restoreFocus() Terminal {
	// Sync blocked state — overlay just closed, so isBlocked() is likely false now.
	m.display = m.display.WithBlocked(m.isBlocked())
	if m.focusedWindow == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// openThemeSelector opens the theme selector UI.
func (m Terminal) openThemeSelector() Terminal {
	if m.themeManager == nil {
		return m
	}
	snap := m.out.SnapshotStatus()
	m.themeSelector = m.themeSelector.Open(snap.CachedThemes, m.activeTheme)
	m.selectorOriginalTheme = nil
	m.previewAppliedTheme = nil
	for _, t := range snap.CachedThemes {
		if t.Name == m.activeTheme && t.Theme != nil {
			m.selectorOriginalTheme = t.Theme
			m.previewAppliedTheme = t.Theme
			break
		}
	}
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openHelpWindow opens the help window UI.
func (m Terminal) openHelpWindow() Terminal {
	m.helpWindow = m.helpWindow.Open()
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openAttachmentWindow opens the attachment picker overlay.
func (m Terminal) openAttachmentWindow() Terminal {
	m.attachmentWindow = m.attachmentWindow.Open()
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmQuit opens the quit confirmation dialog.
func (m Terminal) openConfirmQuit() Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenQuit()
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m Terminal) openConfirmCancel() Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenCancel()
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m Terminal) openConfirmTool(id, toolName, toolInput string) Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenTool(id, toolName, toolInput)
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

func (m Terminal) openConfirmMCPAuth(server, url string) Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenMCPAuth(server, url)
	m.input = m.input.Blur()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// handleBlur handles loss of application focus.
func (m Terminal) handleBlur() Terminal {
	m.hasFocus = false
	m.display = m.display.WithBlocked(m.isBlocked())
	m.display = m.display.WithDisplayFocused(false)
	m.input = m.input.Blur()
	m.modelSelector = m.modelSelector.WithFocus(false)
	m.themeSelector = m.themeSelector.WithFocus(false)
	m.helpWindow = m.helpWindow.WithFocus(false)
	m.confirmOverlay = m.confirmOverlay.WithFocus(false)
	m.mcpInitOverlay = m.mcpInitOverlay.WithFocus(false)
	m.attachmentWindow = m.attachmentWindow.WithFocus(false)
	m.display = m.display.updateContent()
	return m
}

// handleFocus handles gain of application focus.
func (m Terminal) handleFocus() Terminal {
	m.hasFocus = true
	m.display = m.display.WithBlocked(m.isBlocked())
	m.modelSelector = m.modelSelector.WithFocus(true)
	m.themeSelector = m.themeSelector.WithFocus(true)
	m.helpWindow = m.helpWindow.WithFocus(true)
	m.confirmOverlay = m.confirmOverlay.WithFocus(true)
	m.mcpInitOverlay = m.mcpInitOverlay.WithFocus(true)
	m.attachmentWindow = m.attachmentWindow.WithFocus(true)

	if m.modelSelector.IsOpen() ||
		m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen() ||
		m.attachmentWindow.IsOpen() ||
		m.confirmOverlay.IsOpen() ||
		m.mcpInitOverlay.IsOpen() {
		m.display = m.display.updateContent()
		return m
	}

	if m.focusedWindow == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// handlePaste handles clipboard paste events.
func (m Terminal) handlePaste(msg PasteMsg) (Terminal, Cmd) {
	if m.attachmentWindow.IsOpen() {
		aw, cmd := m.attachmentWindow.Update(msg)
		m.attachmentWindow = aw
		return m, cmd
	}
	var cmd Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
