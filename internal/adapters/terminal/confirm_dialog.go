package terminal

// ConfirmDialog renders a centered floating overlay for confirmation dialogs.
// Used for quit, cancel, and tool_confirm prompts.
//
// The dialog uses the same rendering pattern as ModelSelector and ModelSelector:
//   - SetSize stores the terminal dimensions
//   - View renders with RenderOpenBox
//   - RenderOverlay delegates to the shared overlay renderer
//
// Key handling: y/Y = confirm, n/N/esc = cancel.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// ConfirmContentRows defines the fixed number of content lines inside
// the confirm dialog border. Matches SelectorListRows (8) used by
// ModelSelector, ModelSelector, ThemeSelector, and HelpWindow.
// If content exceeds this, it gets truncated with a "…" indicator —
// same pattern used by ModelSelector for items.
const ConfirmContentRows = 8

// ConfirmKind represents the type of active confirmation dialog.
type ConfirmKind int

const (
	ConfirmNone    ConfirmKind = iota // No dialog active
	ConfirmQuit                       // Confirm exit
	ConfirmCancel                     // Confirm cancel current request
	ConfirmTool                       // Confirm tool execution
	ConfirmMCPAuth                    // Confirm MCP OAuth authorization (temporary)
	ConfirmMCPInit                    // MCP servers initializing (persistent)
)

// ConfirmDialog manages a floating confirmation overlay.
// ConfirmDialog is an overlay dialog for confirming tool execution or quit.
//
// Field groups:
//
//	Elm UI state  — value types / primitives (copied on every WithXxx).
//	Dependencies  — pointers to shared data (Styles).
type ConfirmDialog struct {
	// ── Elm UI state (value types, copied on every WithXxx) ─
	state    FilteredListState
	kind     ConfirmKind
	hasFocus bool
	Width    int
	Height   int

	Description string

	// Tool confirm fields (only used for ConfirmTool kind)
	toolID    string
	toolName  string
	toolInput string

	// Result flags — consumed by Terminal after key handling.
	confirmed     bool
	canceled      bool
	ctrlGCanceled bool // true when canceled via Ctrl+G (MCP auth → cancel all)

	// ── Dependencies (pointer to shared data) ─
	styles *Styles
}

// NewConfirmDialog creates a new confirm dialog.
func NewConfirmDialog(styles *Styles) ConfirmDialog {
	return ConfirmDialog{styles: styles}
}

// SetStyles updates the styles used for rendering.
func (cd ConfirmDialog) WithStyles(styles *Styles) ConfirmDialog {
	cd.styles = styles
	return cd
}

// SetHasFocus sets the focus state for styling.
func (cd ConfirmDialog) WithFocus(focused bool) ConfirmDialog {
	cd.hasFocus = focused
	return cd
}

// Kind returns the type of confirmation dialog currently active.
func (cd ConfirmDialog) Kind() ConfirmKind { return cd.kind }

// ToolName returns the tool name for tool confirmations.
func (cd ConfirmDialog) ToolName() string { return cd.toolName }

// ToolInput returns the tool input for tool confirmations.
func (cd ConfirmDialog) ToolInput() string { return cd.toolInput }

// IsCtrlGCanceled returns true if the dialog was closed via Ctrl+G.
func (cd ConfirmDialog) IsCtrlGCanceled() bool { return cd.ctrlGCanceled }

// ToolID returns the tool call ID for tool confirmations.
func (cd ConfirmDialog) ToolID() string { return cd.toolID }

// SetSize updates the terminal dimensions for responsive sizing.
func (cd ConfirmDialog) WithSize(width, height int) ConfirmDialog {
	if width > 0 {
		cd.Width = width
	}
	cd.Height = height
	return cd
}

// IsOpen returns true if the dialog is currently shown.
func (cd ConfirmDialog) IsOpen() bool {
	return cd.state != FilteredListClosed
}

// ---- Open / Close ----

func (cd ConfirmDialog) open(kind ConfirmKind) ConfirmDialog {
	cd.state = FilteredListOpen
	cd.kind = kind
	cd.confirmed = false
	cd.canceled = false
	cd.ctrlGCanceled = false
	return cd
}

// OpenQuit opens the dialog for confirming application exit.
func (cd ConfirmDialog) OpenQuit() ConfirmDialog {
	cd = cd.open(ConfirmQuit)
	cd.Description = "All unsaved progress will be lost."
	return cd
}

// OpenCancel opens the dialog for confirming task cancellation.
func (cd ConfirmDialog) OpenCancel() ConfirmDialog {
	cd = cd.open(ConfirmCancel)
	cd.Description = "The current request will be stopped."
	return cd
}

// OpenMCPAuth opens the dialog for confirming MCP OAuth authorization.
func (cd ConfirmDialog) OpenMCPAuth(serverName, serverURL string) ConfirmDialog {
	cd = cd.open(ConfirmMCPAuth)
	cd.toolID = serverName
	cd.toolName = serverName
	cd.toolInput = serverURL
	cd.Description = serverURL
	return cd
}

// OpenMCPInit opens the dialog to show that MCP servers are initializing.
func (cd ConfirmDialog) OpenMCPInit() ConfirmDialog {
	cd = cd.open(ConfirmMCPInit)
	cd.Description = "Connecting to MCP servers and discovering tools."
	return cd
}

// UpdateMCPInitProgress updates the description with the current server list.
func (cd ConfirmDialog) UpdateMCPInitProgress(servers []string) ConfirmDialog {
	if cd.kind != ConfirmMCPInit {
		return cd
	}
	if len(servers) > 0 {
		cd.Description = strings.Join(servers, ", ")
	} else {
		cd.Description = "Discovering tools..."
	}
	return cd
}

// OpenTool opens the dialog for confirming a tool call.
func (cd ConfirmDialog) OpenTool(toolID, toolName, toolInput string) ConfirmDialog {
	cd = cd.open(ConfirmTool)
	cd.toolID = toolID
	cd.toolName = toolName
	cd.toolInput = toolInput
	parts := strings.SplitN(toolInput, "\n", 2)
	desc := strings.Join(parts, "\n")
	if toolName != "" && strings.HasPrefix(desc, toolName+": ") {
		desc = desc[len(toolName)+2:]
	}
	desc = strings.TrimRight(desc, "\n")
	cd.Description = desc
	return cd
}

// Close closes the dialog without committing any action.
func (cd ConfirmDialog) Close() ConfirmDialog {
	cd.state = FilteredListClosed
	cd.kind = ConfirmNone
	cd.Description = ""
	cd.toolID = ""
	cd.toolName = ""
	cd.toolInput = ""
	cd.confirmed = false
	cd.canceled = false
	cd.ctrlGCanceled = false
	return cd
}

// ---- Key Handling ----

// ConfirmDialogUpdate captures the outcome of a HandleKeyMsg call.

// HandleKeyMsg processes a key press and updates state.
// Returns the updated dialog and a result struct describing what happened.
func (cd ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, tea.Cmd) {
	if !cd.IsOpen() {
		return cd, nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return cd, nil
	}
	key := keyMsg.String()

	if cd.kind == ConfirmMCPInit {
		if key == keyCtrlG {
			result, r := cd.buildResult()
			r.CtrlGCanceled = true
			result.canceled = true
			result.state = FilteredListClosed
			return result, func() tea.Msg { return ConfirmResultMsg{Result: r} }
		}
		return cd, nil // handled but no result
	}

	switch key {
	case keyY, keyYCapital:
		cd.confirmed = true
		return cd.closeWithResult()

	case keyN, keyNCapital, keyEsc:
		cd.canceled = true
		return cd.closeWithResult()

	case keyCtrlG:
		if cd.kind == ConfirmMCPAuth {
			cd.ctrlGCanceled = true
			cd.canceled = true
			return cd.closeWithResult()
		}
		return cd, nil // handled but no result

	case keyE:
		if cd.kind == ConfirmTool && cd.toolInput != "" {
			content := cd.toolInput
			if cd.toolName != "" && strings.HasPrefix(content, cd.toolName+": ") {
				content = content[len(cd.toolName)+2:]
			}
			return cd, func() tea.Msg {
				return openEditorForDisplayMsg{content: content}
			}
		}
		return cd, nil
	}

	return cd, nil
}

// buildResult creates a ConfirmResult from the current state without resetting.
func (cd ConfirmDialog) buildResult() (ConfirmDialog, *ConfirmResult) {
	r := &ConfirmResult{
		Kind:          cd.kind,
		Confirmed:     cd.confirmed,
		Canceled:      cd.canceled,
		ToolID:        cd.toolID,
		ToolInput:     cd.toolInput,
		CtrlGCanceled: cd.ctrlGCanceled,
	}
	return cd, r
}

// closeWithResult closes the dialog and returns a Command that emits a ConfirmResultMsg.
// The caller should set flags (confirmed, canceled, etc.) on cd before calling this.
func (cd ConfirmDialog) closeWithResult() (ConfirmDialog, tea.Cmd) {
	cd.state = FilteredListClosed
	_, r := cd.buildResult()
	return cd, func() tea.Msg { return ConfirmResultMsg{Result: r} }
}

// ConfirmResult captures the complete result of a confirm dialog interaction.
type ConfirmResult struct {
	Kind          ConfirmKind
	Confirmed     bool
	Canceled      bool
	ToolID        string
	ToolInput     string
	CtrlGCanceled bool
}

// ---- Rendering ----

func (cd ConfirmDialog) View() tea.View {
	if !cd.IsOpen() {
		return tea.NewView("")
	}

	msgLines := cd.buildContentLines()
	for len(msgLines) < ConfirmContentRows {
		msgLines = append(msgLines, "")
	}
	content := strings.Join(msgLines, "\n")
	box := cd.styles.RenderOpenBox(content, cd.Width, cd.styles.ColorWarning, ConfirmContentRows)
	return tea.NewView(box)
}

// buildContentLines returns the display lines for the dialog content.
func (cd ConfirmDialog) buildContentLines() []string {
	innerWidth := max(0, cd.Width)
	maxBodyLines := max(0, ConfirmContentRows-2)

	titleText := cd.buildTitleText()
	if titleText == "" {
		return nil
	}

	titleLine := cd.renderTitleLine(titleText, innerWidth)
	descRows := cd.renderDescriptionRows(innerWidth)

	body := []string{""}
	body = append(body, titleLine)
	body = append(body, "")
	body = append(body, descRows...)
	body = append(body, "")

	if len(body) > maxBodyLines {
		body = body[:maxBodyLines-1]
		body = append(body, cd.wrapAndCenter("...", cd.styles.System, innerWidth)[0])
	}

	for len(body) < maxBodyLines {
		body = append(body, "")
	}

	lines := body
	switch cd.kind {
	case ConfirmMCPInit:
		lines = append(lines, cd.wrapAndCenter("Press Ctrl+G to cancel MCP initialization.", cd.styles.System, innerWidth)[0])
		lines = append(lines, cd.wrapAndCenter("(this window will close automatically)", cd.styles.System, innerWidth)[0])
	default:
		lines = append(lines, cd.wrapAndCenter("y / n", cd.styles.Confirm, innerWidth)[0])
		lines = append(lines, "")
	}

	return lines
}

func (cd ConfirmDialog) buildTitleText() string {
	switch cd.kind {
	case ConfirmQuit:
		return "Exit AlayaCore?"
	case ConfirmCancel:
		return "Cancel current task?"
	case ConfirmTool:
		msg := "Allow "
		if cd.toolName != "" {
			msg += fmt.Sprintf("%q", cd.toolName)
		} else {
			msg += "this tool"
		}
		return msg + " to run?"
	case ConfirmMCPAuth:
		msg := "Authorize MCP server "
		if cd.toolName != "" {
			msg += fmt.Sprintf("%q", cd.toolName)
		} else {
			msg += "?"
		}
		return msg + "?"
	case ConfirmMCPInit:
		return "Initializing MCP servers…"
	default:
		return ""
	}
}

func (cd ConfirmDialog) renderTitleLine(titleText string, innerWidth int) string {
	styled := cd.styles.Confirm.Render(titleText)
	wrapped := wrapContent(styled, innerWidth)
	lines := strings.Split(wrapped, "\n")
	line := lines[0]
	if len(lines) > 1 {
		line = truncateWithSuffix(line, innerWidth)
	}
	w := lipgloss.Width(line)
	pad := max(0, (innerWidth-w)/2)
	return strings.Repeat(" ", pad) + line + strings.Repeat(" ", innerWidth-w-pad)
}

func (cd ConfirmDialog) renderDescriptionRows(innerWidth int) []string {
	rawWrapped := ansi.Hardwrap(cd.Description, innerWidth, true)
	rawLines := strings.Split(rawWrapped, "\n")
	rawDesc := rawLines
	if len(rawDesc) > 2 {
		rawDesc = rawDesc[:2]
		rawDesc[1] = truncateWithSuffix(rawDesc[1], innerWidth)
	}
	for len(rawDesc) < 2 {
		rawDesc = append(rawDesc, "")
	}
	styled := make([]string, 2)
	for i, line := range rawDesc {
		styled[i] = cd.styles.System.Render(line)
	}
	maxW := 0
	for _, line := range styled {
		if w := lipgloss.Width(line); w > maxW {
			maxW = w
		}
	}
	pad := max(0, (innerWidth-maxW)/2)
	rows := make([]string, 2)
	for i, line := range styled {
		w := lipgloss.Width(line)
		rows[i] = strings.Repeat(" ", pad) + line + strings.Repeat(" ", maxW-w)
	}
	return rows
}

func (cd ConfirmDialog) wrapAndCenter(text string, style lipgloss.Style, width int) []string {
	styled := style.Render(text)
	wrapped := wrapContent(styled, width)
	rawLines := strings.Split(wrapped, "\n")
	maxLineWidth := 0
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}
	blockPadding := max(0, (width-maxLineWidth)/2)
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		rightPad := maxLineWidth - w
		lines = append(lines, strings.Repeat(" ", blockPadding)+line+strings.Repeat(" ", rightPad))
	}
	return lines
}

// RenderOverlay renders the dialog as a centered overlay on top of base content.
func (cd ConfirmDialog) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if !cd.IsOpen() {
		return baseContent
	}
	// Shift up by 1 line to compensate for confirm's compact content
	// (no filter bar or list, unlike other overlays).
	return renderOverlay(baseContent, cd.View().Content, screenWidth, screenHeight, -1)
}
