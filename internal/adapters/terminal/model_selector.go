package terminal

// ModelSelector manages model selection and configuration UI.
// It provides a searchable list of models with keyboard navigation.

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
)

type searchableModel struct {
	protocol.ModelInfo
	searchStr string
}

// ModelSelector is an overlay for selecting the active model.
//
// All fields are Elm UI state (value types, copied on every WithXxx).
// No external dependencies — model list is built from system info messages.
type ModelSelector struct {
	FilteredListCore

	models         []searchableModel
	filteredModels []searchableModel

	activeModel    *searchableModel
	lastModelCount int
}

func NewModelSelector(styles *Styles) ModelSelector {
	input := newFilterInput("Search models...")
	ms := ModelSelector{
		models: []searchableModel{},
	}
	ms.Width = 60
	ms.Height = 20
	ms.HasFocus = true
	ms.FilterInput = input
	ms.lastFilterValue = "\x00"
	ms.Styles = styles
	return ms
}

func newFilterInput(placeholder string) InputField {
	input := NewInputField()
	input.Placeholder = placeholder
	// No decorative prompt prefix — the filter box is a bare input.
	input.Prompt = ""
	input = input.WithWidth(50)
	return input
}

// --- Model Management ---

func (ms ModelSelector) GetActiveModel() *protocol.ModelInfo {
	if ms.activeModel == nil {
		return nil
	}
	return &ms.activeModel.ModelInfo
}

func (ms ModelSelector) WithActiveModel(m *searchableModel) ModelSelector {
	ms.activeModel = m
	return ms
}

func (ms ModelSelector) GetModels() []protocol.ModelInfo {
	result := make([]protocol.ModelInfo, len(ms.models))
	for i := range ms.models {
		result[i] = ms.models[i].ModelInfo
	}
	return result
}

func (ms ModelSelector) WithModels(models []searchableModel) ModelSelector {
	ms.models = models
	for i := range ms.models {
		ms.models[i].searchStr = buildSearchStr(&ms.models[i])
	}
	ms.lastFilterValue = "\x00"
	return ms.updateFilteredModels()
}

func (ms ModelSelector) LoadModels(models []protocol.ModelInfo, activeID int) (ModelSelector, Cmd) {
	if ms.modelsUnchangedSinceLastLoad(models) {
		for i := range ms.models {
			if ms.models[i].ID == activeID {
				ms.activeModel = &ms.models[i]
				break
			}
		}
		return ms, nil
	}

	prevModelCount := ms.lastModelCount
	ms.lastModelCount = len(models)
	ms.models = make([]searchableModel, len(models))

	savedSelectedIdx := ms.SelectedIdx
	savedScrollIdx := ms.ScrollIdx
	shouldPreserveSelection := ms.State != FilteredListClosed

	for i, m := range models {
		ms.models[i] = searchableModel{
			ModelInfo: m,
			searchStr: buildSearchStr(&searchableModel{ModelInfo: m}),
		}
		if m.ID == activeID {
			ms.activeModel = &ms.models[i]
		}
	}

	ms.lastFilterValue = "\x00"
	ms = ms.updateFilteredModels()
	if shouldPreserveSelection && prevModelCount > 0 {
		ms.SelectedIdx = savedSelectedIdx
		ms.ScrollIdx = savedScrollIdx
		ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
	}
	return ms, func() Msg { return nil }
}

func (ms ModelSelector) modelsUnchangedSinceLastLoad(models []protocol.ModelInfo) bool {
	if len(models) != len(ms.models) || len(models) != ms.lastModelCount {
		return false
	}
	for i, m := range models {
		if i >= len(ms.models) || ms.models[i].ID != m.ID || ms.models[i].Name != m.Name {
			return false
		}
	}
	return true
}

// --- Open / Close ---

func (ms ModelSelector) WithSize(width, height int) ModelSelector {
	ms.FilteredListCore = ms.FilteredListCore.WithSize(width, height)
	return ms
}

func (ms ModelSelector) WithStyles(styles *Styles) ModelSelector {
	ms.FilteredListCore = ms.FilteredListCore.WithStyles(styles)
	return ms
}

func (ms ModelSelector) WithFocus(focused bool) ModelSelector {
	ms.FilteredListCore = ms.FilteredListCore.WithFocus(focused)
	return ms
}

func (ms ModelSelector) Open() ModelSelector {
	ms.State = FilteredListOpen
	ms.FilterInput = ms.FilterInput.WithValue("")
	ms.lastFilterValue = "\x00"
	ms.FilterInputFocused = true
	ms.FilterInput = ms.FilterInput.Focus()
	ms.FilteredListCore = ms.FilteredListCore.updateFilterInputStyles()
	ms.ScrollIdx = 0
	ms = ms.updateFilteredModels()
	return ms
}

// ModelSelectorUpdate captures the outcome of a HandleKeyMsg call.

// --- Key Handling ---

//nolint:gocyclo
func (ms ModelSelector) Update(msg Msg) (ModelSelector, Cmd) {
	if ms.State == FilteredListClosed {
		return ms, nil
	}

	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return ms, nil
	}
	key := keyMsg.String()

	fl, result := ms.FilteredListCore.HandleKey(keyMsg)
	ms.FilteredListCore = fl

	// Handle Ctrl+R reload regardless of focus
	if key == keyCtrlR {
		return ms, func() Msg { return ReloadModelsMsg{} }
	}

	// Handle Enter selection in the list.
	if key == keyEnter && result.Handled && !fl.FilterInputFocused {
		if len(ms.filteredModels) > 0 && fl.SelectedIdx >= 0 {
			ms.activeModel = &ms.filteredModels[fl.SelectedIdx]
			fl = fl.Close()
			ms.FilteredListCore = fl
			return ms, func() Msg { return ModelSelectedMsg{ID: ms.activeModel.ID} }
		}
	}

	if result.Handled {
		if result.FilterChanged && ms.FilterInputFocused {
			ms = ms.updateFilteredModels()
		}
		if !ms.FilterInputFocused {
			ms = ms.handleListKeys(key)
		}
		if ms.FilterInputFocused && key == keyEnter && len(ms.filteredModels) > 0 {
			ms = ms.handleSearchEnter()
			ms.FilteredListCore = fl.Close()
			return ms, func() Msg { return ModelSelectedMsg{ID: ms.activeModel.ID} }
		}
		return ms, nil
	}

	if !ms.FilterInputFocused {
		ms = ms.handleListKeys(key)
	}
	return ms, nil
}

func (ms ModelSelector) handleSearchEnter() ModelSelector {
	ms.SelectedIdx = 0
	ms.activeModel = &ms.filteredModels[0]
	ms.FilteredListCore = ms.FilteredListCore.Close()
	return ms
}

func (ms ModelSelector) handleListKeys(key string) ModelSelector {
	switch key {
	case keyJ, keyDown:
		if ms.SelectedIdx < len(ms.filteredModels)-1 {
			ms.SelectedIdx++
		}
	case keyK, keyUp:
		if ms.SelectedIdx > 0 {
			ms.SelectedIdx--
		}
	}
	return ms
}

// --- Rendering ---

func (ms ModelSelector) renderList() string {
	var sb strings.Builder

	titleStyle := NewStyle().Background(ms.Styles.ColorDim).Foreground(ms.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", ms.Width, "Model Selector")))
	sb.WriteString("\n")

	searchBox := ms.Styles.RenderOpenBox(ms.FilterInput.View(), ms.Width, ms.FilterBorderColor())
	sb.WriteString(searchBox)
	sb.WriteString("\n")

	if ms.activeModel != nil {
		sb.WriteString(ms.Styles.System.Render("Current: "))
		sb.WriteString(ms.Styles.Text.Render(ms.activeModel.Name))
		sb.WriteString("\n")
	}

	listBorderColor := ms.ListBorderColor()
	boxWidth := Width(searchBox)
	sb.WriteString(ms.renderModelList(boxWidth, listBorderColor))

	helpStyle := NewStyle().Background(ms.Styles.ColorDim).Foreground(ms.Styles.ColorMuted)
	var help string
	if ms.FilterInputFocused {
		help = "  tab: list │ ctrl+r: reload │ enter: select │ esc: close"
	} else {
		help = "  tab: search │ j/k: navigate │ ctrl+r: reload │ enter: select │ q/esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", boxWidth, help)))

	return sb.String()
}

func (ms ModelSelector) renderModelList(width int, borderColor color.Color) string {
	var content strings.Builder
	listHeight := SelectorListRows
	innerWidth := max(0, width)

	switch {
	case len(ms.filteredModels) == 0:
		content.WriteString(ms.Styles.System.Render("No models match your search."))
	default:
		ms.FilteredListCore = ms.FilteredListCore.EnsureVisible()
		idWidth := ms.maxIDWidth()
		nameMaxWidth, ctxColWidth, provColWidth := ms.measureColumns(listHeight, innerWidth, idWidth)

		for i := ms.ScrollIdx; i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels)); i++ {
			line := ms.renderModelRow(i, idWidth, nameMaxWidth, ctxColWidth, provColWidth)
			content.WriteString(line)
			if i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels))-1 {
				content.WriteString("\n")
			}
		}
	}

	return ms.Styles.RenderOpenBox(content.String(), width, borderColor, listHeight)
}

func (ms ModelSelector) maxIDWidth() int {
	maxID := 0
	for _, m := range ms.filteredModels {
		if m.ID > maxID {
			maxID = m.ID
		}
	}
	return len(fmt.Sprintf("%d", maxID))
}

func (ms ModelSelector) measureColumns(listHeight, innerWidth, idWidth int) (nameMaxWidth, ctxColWidth, provColWidth int) {
	longestName := 0
	naturalCtx := 0
	naturalProv := 0
	for i := ms.ScrollIdx; i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels)); i++ {
		m := ms.filteredModels[i]
		if w := Width(m.Name); w > longestName {
			longestName = w
		}
		ctx := formatContextLimit(int64(m.ContextLimit))
		if w := Width(ctx); w > naturalCtx {
			naturalCtx = w
		}
		provider := capitalize(m.ProtocolType)
		if w := Width(provider); w > naturalProv {
			naturalProv = w
		}
	}
	naturalCtx = max(1, naturalCtx)
	naturalProv = max(1, naturalProv)

	prefixWidth := 4 + idWidth
	minName := max(10, longestName)
	nameMaxWidth = innerWidth - prefixWidth

	minCol := 2
	extraCtx := nameMaxWidth - minName
	switch {
	case extraCtx >= 1+naturalCtx:
		ctxColWidth = naturalCtx
		nameMaxWidth -= 1 + naturalCtx
	case extraCtx >= minCol:
		ctxColWidth = extraCtx - 1
		nameMaxWidth = minName
	}

	extraProv := nameMaxWidth - minName
	switch {
	case extraProv >= 1+naturalProv:
		provColWidth = naturalProv
		nameMaxWidth -= 1 + naturalProv
	case extraProv >= minCol:
		provColWidth = extraProv - 1
		nameMaxWidth = minName
	}

	return max(1, nameMaxWidth), ctxColWidth, provColWidth
}

func (ms ModelSelector) renderModelRow(i, idWidth, nameMaxWidth, ctxColWidth, provColWidth int) string {
	m := ms.filteredModels[i]
	isSelected := i == ms.SelectedIdx

	idxStr := fmt.Sprintf("%0*d", idWidth, m.ID)
	leftRaw := idxStr // flush left — no indent, no "> " marker

	ctx := formatContextLimit(int64(m.ContextLimit))
	if ctxColWidth > 0 {
		ctx = truncateWithSuffix(ctx, ctxColWidth)
	}
	ctxRaw := fmt.Sprintf("%*s", ctxColWidth, ctx)

	provider := capitalize(m.ProtocolType)
	if provColWidth > 0 {
		provider = truncateWithSuffix(provider, provColWidth)
	}
	provRaw := fmt.Sprintf("%-*s", provColWidth, provider)

	name := m.Name
	if nameMaxWidth > 0 {
		name = truncateWithSuffix(name, nameMaxWidth)
	}

	padding := max(0, nameMaxWidth-Width(name))
	namePadded := name + strings.Repeat(" ", padding)
	line := leftRaw + "  " + namePadded
	if ctxColWidth > 0 {
		line += " " + ctxRaw
	}
	if provColWidth > 0 {
		line += " " + provRaw
	}

	if isSelected {
		return ms.Styles.Text.Render(line)
	}
	return ms.Styles.System.Render(line)
}

func formatContextLimit(n int64) string {
	if n <= 0 {
		return "∞"
	}
	if n >= 1_000_000 {
		v := float64(n) / 1_000_000
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0fMB", v)
		}
		return fmt.Sprintf("%.1fMB", v)
	}
	if n >= 1_000 {
		v := float64(n) / 1_000
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0fKB", v)
		}
		return fmt.Sprintf("%.1fKB", v)
	}
	return fmt.Sprintf("%d", n)
}

func buildSearchStr(m *searchableModel) string {
	ctx := formatContextLimit(int64(m.ContextLimit))
	provider := capitalize(m.ProtocolType)
	return strings.ToLower(fmt.Sprintf("%d %s %s %s", m.ID, m.Name, ctx, provider))
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	if s == "openai" {
		return "OpenAI"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (ms ModelSelector) View() View {
	if ms.State == FilteredListClosed {
		return NewView("")
	}
	return NewView(ms.renderList())
}

func (ms ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ms.View().Content, screenWidth, screenHeight, 0)
}

// CursorPosition returns the screen position of the filter input's real
// terminal cursor when the overlay is open and the filter has focus.
func (ms ModelSelector) CursorPosition(screenWidth, screenHeight int) (x, y int, ok bool) {
	return ms.FilteredListCore.CursorPosition(ms.View().Content, screenWidth, screenHeight)
}

func (ms ModelSelector) updateFilteredModels() ModelSelector {
	search := ms.FilterInput.Value()
	if search == ms.lastFilterValue {
		return ms
	}

	var prevSelectedID = -1
	if !ms.FilterInputFocused && ms.SelectedIdx >= 0 && ms.SelectedIdx < len(ms.filteredModels) {
		prevSelectedID = ms.filteredModels[ms.SelectedIdx].ID
	}

	ms.lastFilterValue = search

	if search == "" {
		ms.filteredModels = make([]searchableModel, len(ms.models))
		copy(ms.filteredModels, ms.models)
	} else {
		term := strings.ToLower(search)
		ms.filteredModels = ms.filteredModels[:0]
		for _, m := range ms.models {
			if FuzzyMatch(term, m.searchStr) {
				ms.filteredModels = append(ms.filteredModels, m)
			}
		}
	}

	if prevSelectedID >= 0 {
		found := false
		for i, m := range ms.filteredModels {
			if m.ID == prevSelectedID {
				ms.SelectedIdx = i
				found = true
				break
			}
		}
		if found {
			ms.FilteredListCore = ms.FilteredListCore.EnsureVisible()
			ms.FilteredListCore = ms.FilteredListCore.ClampScroll(len(ms.filteredModels))
		} else {
			ms.SelectedIdx = 0
			ms.ScrollIdx = 0
			ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
		}
	} else {
		ms.SelectedIdx = 0
		ms.ScrollIdx = 0
		ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
	}
	return ms
}
