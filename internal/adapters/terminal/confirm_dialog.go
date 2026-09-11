package terminal

// ConfirmDialog renders a centered floating overlay for confirmation dialogs.
// Used for quit, cancel, tool-confirm, MCP OAuth auth, and MCP-init prompts.
//
// Rendering follows the shared overlay pattern (see ModelSelector):
//   - SetSize stores the terminal dimensions
//   - View renders with RenderOpenBox
//   - RenderOverlay CUP-anchors the box rows over the base content, joining
//     a long single-line description preview into one soft-wrap run when the
//     box spans the full terminal width (see RenderOverlay)
//
// Key handling: y/Y = confirm, n/N/esc = cancel, e (tool confirms with
// input) = open the full tool input in $EDITOR (view-only).

import (
	"fmt"
	"strings"

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

// ConfirmDialog manages a floating confirmation overlay: quit/cancel
// prompts, tool-execution confirmations, MCP OAuth authorization, and the
// MCP-initialization progress dialog.
//
// Field groups:
//
//	Elm UI state  — value types / primitives (copied on every WithXxx).
//	Dependencies  — pointers to shared data (Styles).
type ConfirmDialog struct {
	// ── Elm UI state (value types, copied on every WithXxx) ─
	state  FilteredListState
	kind   ConfirmKind
	Width  int
	Height int

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

	// ── View cache ── invalidated whenever the rendered content would
	// change: kind/toolName/toolInput (title, hint row) or Description
	// (body) or Width (wrap). See viewCacheKey.
	lastViewKey string
	lastViewBox string
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

// Kind returns the type of confirmation dialog currently active.
func (cd ConfirmDialog) Kind() ConfirmKind { return cd.kind }

// ToolName returns the tool name for tool confirmations.
func (cd ConfirmDialog) ToolName() string { return cd.toolName }

// ToolInput returns the tool input for tool confirmations.
func (cd ConfirmDialog) ToolInput() string { return cd.toolInput }

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

// HandleKeyMsg processes a key press and updates state.
// Returns the updated dialog and a result struct describing what happened.
func (cd ConfirmDialog) Update(msg Msg) (ConfirmDialog, Cmd) {
	if !cd.IsOpen() {
		return cd, nil
	}

	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return cd, nil
	}
	key := keyMsg.Chord()

	if cd.kind == ConfirmMCPInit {
		if key == keyCtrlG {
			result, r := cd.buildResult()
			r.CtrlGCanceled = true
			result.canceled = true
			result.state = FilteredListClosed
			return result, func() Msg { return ConfirmResultMsg{Result: r} }
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
			return cd, func() Msg {
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
func (cd ConfirmDialog) closeWithResult() (ConfirmDialog, Cmd) {
	cd.state = FilteredListClosed
	_, r := cd.buildResult()
	return cd, func() Msg { return ConfirmResultMsg{Result: r} }
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

// View returns the rendered dialog.
func (cd ConfirmDialog) View() View {
	if !cd.IsOpen() {
		return NewView("")
	}

	// Cache the rendered content when neither the description nor the
	// width has changed since the last View() call. UpdateMCPInitProgress
	// fires ~4×/sec during MCP init; without this cache the wrap/center
	// pipeline runs every tick even when nothing changed.
	if cd.lastViewKey == cd.viewCacheKey() {
		return NewView(cd.lastViewBox)
	}

	msgLines := cd.buildContentLines()
	for len(msgLines) < ConfirmContentRows {
		msgLines = append(msgLines, "")
	}
	content := strings.Join(msgLines, "\n")
	box := cd.styles.RenderOpenBox(content, cd.Width, cd.styles.ColorWarning, ConfirmContentRows)
	cd.lastViewKey = cd.viewCacheKey()
	cd.lastViewBox = box
	return NewView(box)
}

// viewCacheKey produces a key that changes whenever the rendered output
// would. Description drives the body preview; Width drives the wrap; kind
// and toolName drive the title; the 'e' hint row appears for tool
// confirms that carry input (Description, derived from toolInput, stands
// in for whether input is present).
func (cd ConfirmDialog) viewCacheKey() string {
	return fmt.Sprintf("%d|%s|%s|%d", cd.kind, cd.toolName, cd.Description, cd.Width)
}

// buildContentLines returns the display lines for the dialog content.
//
// The rows above the kind-specific lines are fixed: blank, title, blank,
// the two description rows (renderDescriptionRows always returns exactly
// two), blank — six rows, i.e. ConfirmContentRows-2. The kind-specific
// lines below (y/n plus an optional 'e' hint, or the two MCP-init hints)
// bring the content to ConfirmContentRows; View() pads the remainder.
func (cd ConfirmDialog) buildContentLines() []string {
	innerWidth := max(0, cd.Width)

	titleText := cd.buildTitleText()
	if titleText == "" {
		return nil
	}

	titleLine := cd.renderTitleLine(titleText, innerWidth)
	descRows := cd.renderDescriptionRows(innerWidth)

	lines := []string{"", titleLine, "", descRows[0], descRows[1], ""}
	switch cd.kind {
	case ConfirmMCPInit:
		lines = append(lines, cd.wrapAndCenter("Press Ctrl+G to cancel MCP initialization.", cd.styles.System, innerWidth)[0])
		lines = append(lines, cd.wrapAndCenter("(this window will close automatically)", cd.styles.System, innerWidth)[0])
	default:
		lines = append(lines, cd.wrapAndCenter("y / n", cd.styles.Confirm, innerWidth)[0])
		// The tool-confirm dialog announces 'e' on its own centered hint
		// row (same pattern as the MCP-init hints above): the title stays
		// clean so a long tool name only ever truncates the title, never
		// the affordance, and the hint is a full sentence rather than a
		// cryptic "(e: ...)" tag.
		if cd.kind == ConfirmTool && cd.toolInput != "" {
			lines = append(lines, cd.wrapAndCenter("Press e to view the full input.", cd.styles.System, innerWidth)[0])
		} else {
			lines = append(lines, "")
		}
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
		msg += " to run?"
		return msg
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
	// Truncate the plain title first, then render with the Confirm style —
	// the "…" inserted by truncation inherits the style from the render
	// call (no escape-sequence handling needed).
	plainWrapped := ansi.Hardwrap(titleText, innerWidth, true)
	lines := strings.Split(plainWrapped, "\n")
	line := lines[0]
	if len(lines) > 1 {
		// The title wrapped onto further rows (a long tool name): keep
		// the first row and mark the cut with "…" — a
		// truncateWithSuffix call on an exactly-full row is a no-op, and
		// the overflow would otherwise be dropped silently.
		line = takeCells(line, max(0, innerWidth-1)) + "…"
	}
	line = cd.styles.Confirm.Render(line)
	w := Width(line)
	pad := max(0, (innerWidth-w)/2)
	return strings.Repeat(" ", pad) + line + strings.Repeat(" ", innerWidth-w-pad)
}

// renderDescriptionRows returns the 2-row preview of the description.
//
// The rows use the same character-boundary breaks as a message window's
// soft-wrap rows: a continuation runs exactly to the inner width and only
// a final row may be shorter. When the two rows continue the SAME
// original line (the usual single-line command), RenderOverlay emits them
// as one continuous soft-wrap run — no hard '\n' inside the command — and
// the Screen row diff tracks the run's wrapped span. When the description
// does not fit in the two rows, the last visible row ends with "…" (in
// its final cell, so the marker never overflows) — a silent drop of the
// remainder would make a cut look like the real end of the input.
func (cd ConfirmDialog) renderDescriptionRows(innerWidth int) []string {
	rawWrapped := ansi.Hardwrap(cd.Description, innerWidth, true)
	rawLines := strings.Split(rawWrapped, "\n")
	// A trailing '\n' produces an empty final row that is not content —
	// drop it so it neither fills a preview row nor triggers a false
	// "…" marker.
	for len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	rawDesc := rawLines
	if len(rawDesc) > 2 {
		// The preview cannot show everything: keep the first two rows
		// and reserve the last cell of the second for the marker.
		rawDesc = rawDesc[:2]
		rawDesc[1] = takeCells(rawDesc[1], max(0, innerWidth-1)) + "…"
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
		if w := Width(line); w > maxW {
			maxW = w
		}
	}
	pad := max(0, (innerWidth-maxW)/2)
	rows := make([]string, 2)
	for i, line := range styled {
		w := Width(line)
		rows[i] = strings.Repeat(" ", pad) + line + strings.Repeat(" ", maxW-w)
	}
	return rows
}

func (cd ConfirmDialog) wrapAndCenter(text string, style Style, width int) []string {
	styled := style.Render(text)
	wrapped := wrapContent(styled, width)
	rawLines := strings.Split(wrapped, "\n")
	maxLineWidth := 0
	for _, line := range rawLines {
		w := Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}
	blockPadding := max(0, (width-maxLineWidth)/2)
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		w := Width(line)
		rightPad := maxLineWidth - w
		lines = append(lines, strings.Repeat(" ", blockPadding)+line+strings.Repeat(" ", rightPad))
	}
	return lines
}

// descriptionRowsFormSoftRun reports whether the second description
// row shown by the box continues the FIRST original line of the
// description — a soft-wrap continuation in the message-window sense
// (same original line, broken only at the display width). It mirrors
// renderDescriptionRows' geometry (same width, same character-boundary
// wrap), so the flag matches the two rows the box actually displays.
// Multi-line descriptions whose second shown row starts a new original
// line return false: those rows are legitimately hard-separated, exactly
// as message windows separate original lines.
func (cd ConfirmDialog) descriptionRowsFormSoftRun() bool {
	if cd.Description == "" {
		return false
	}
	vlines := wrapVisualLines(cd.Description, max(1, cd.Width))
	return len(vlines) > 1 && vlines[1].Cont
}

// RenderOverlay renders the dialog as a centered overlay on top of base content.
//
// When the box spans the full terminal width, the two description rows of
// the SAME original line are emitted as ONE continuous soft-wrap run — no
// '\n', no CUP between them. Every box row ends exactly at the terminal
// width, so the terminal's own soft wrap moves the second row to the next
// terminal row and a selection copies the long command without fake
// newlines — the same byte semantics as a message window's soft-wrap
// fragment. The Screen row diff tracks the run's wrapped span (see
// positionedRows in screen.go), so repaints and the dialog close clear
// both rows. When the box is narrower than the terminal (centered — a
// stale width before the next resize message), the run cannot cross rows;
// fall back to the per-row absolute-position emission.
func (cd ConfirmDialog) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if !cd.IsOpen() {
		return baseContent
	}
	box := cd.View().Content
	x, y := overlayOrigin(box, screenWidth, screenHeight)
	// Shift up by 1 line to compensate for confirm's compact content
	// (no filter bar or list, unlike other overlays).
	y = max(0, y-1)

	boxWidth := Width(box)
	if x != 0 || boxWidth != screenWidth {
		return renderOverlay(baseContent, box, screenWidth, screenHeight, -1)
	}

	// Box row layout (always 10 rows): rule, blank, title, blank,
	// description ×2, blank, then kind-dependent rows — "y / n" plus
	// either a blank (quit/cancel/MCP auth) or the tool-confirm hint
	// "Press e to view the full input." (tool with input), or the two
	// MCP-init hint lines. The description always occupies rows 4 and 5,
	// which is what the soft-run logic below keys on.
	rows := strings.Split(box, "\n")
	descRun := cd.descriptionRowsFormSoftRun()

	var sb strings.Builder
	sb.Grow(len(baseContent) + len(box) + len(rows)*12)
	sb.WriteString(baseContent)
	for i, row := range rows {
		rowY := y + i
		if rowY >= screenHeight {
			break
		}
		if descRun && i == 5 {
			// Continuation of the description run: written straight after
			// its full-width predecessor (the terminal soft-wraps it to
			// the next terminal row). Not padded — a run tail carries no
			// trailing spaces in a selection — but a short tail must
			// erase the rest of the row so no dimmed base content shows
			// through beside it.
			sb.WriteString(row)
			if w := cellWidth(row); w < boxWidth {
				sb.WriteString(ansi.EraseLine(0))
			}
			continue
		}
		// Pad to the box width so the row fully covers the base content.
		if w := cellWidth(row); w < boxWidth {
			row += strings.Repeat(" ", boxWidth-w)
		}
		// Absolute cursor position (1-based rows/cols). The run's first
		// row (i == 4) is padded to the full width here — the terminal
		// then wraps exactly at the row boundary into the continuation.
		fmt.Fprintf(&sb, "\x1b[%d;%dH", rowY+1, x+1)
		sb.WriteString(row)
	}
	return sb.String()
}
