package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ModelConfig represents a model configuration for display in the selector.
type ModelConfig struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key,omitempty"`
	ModelName    string `json:"model_name"`
	ContextLimit int    `json:"context_limit"`

	// Pre-computed lowercase fields for fast search
	nameLower         string
	protocolTypeLower string
	modelNameLower    string
	baseURLLower      string
}

// ModelSelectorState represents the current state of the model selector.
type ModelSelectorState int

const (
	ModelSelectorClosed ModelSelectorState = iota
	ModelSelectorList
)

// ModelSelector manages model selection and configuration UI.
// It provides a searchable list of models with keyboard navigation.
type ModelSelector struct {
	state          ModelSelectorState
	models         []ModelConfig
	filteredModels []ModelConfig
	selectedIdx    int
	scrollIdx      int
	width          int
	height         int
	styles         *Styles

	// Search state
	searchInput        textinput.Model
	searchInputFocused bool
	lastSearchValue    string

	// Selection state
	activeModel       *ModelConfig
	modelJustSelected bool

	// Action flags (consumed by parent)
	openModelFile  bool
	reloadModels   bool
	lastModelCount int

	// App focus state (when app loses focus, dim all UI elements)
	hasFocus bool
}

// NewModelSelector creates a new model selector.
func NewModelSelector(styles *Styles) *ModelSelector {
	searchInput := textinput.New()
	searchInput.Placeholder = "Search models..."
	searchInput.Prompt = "/ "
	searchInput.SetWidth(50)

	return &ModelSelector{
		state:       ModelSelectorClosed,
		models:      []ModelConfig{},
		styles:      styles,
		width:       60,
		height:      20,
		hasFocus:    true,
		searchInput: searchInput,
	}
}

// --- State Management ---

func (ms *ModelSelector) IsOpen() bool              { return ms.state != ModelSelectorClosed }
func (ms *ModelSelector) State() ModelSelectorState { return ms.state }

func (ms *ModelSelector) Open() {
	ms.state = ModelSelectorList
	ms.searchInput.SetValue("")
	ms.lastSearchValue = "\x00" // Force update
	ms.searchInputFocused = false
	ms.searchInput.Blur()
	ms.updateSearchInputStyles()
	ms.scrollIdx = 0
	ms.updateFilteredModels()
	// Position cursor at the active model
	ms.selectActiveModel()
}

// selectActiveModel positions the cursor at the active model in the filtered list.
func (ms *ModelSelector) selectActiveModel() {
	if ms.activeModel == nil {
		ms.clampSelection()
		return
	}
	// Find active model in filtered list
	for i, m := range ms.filteredModels {
		if m.ID == ms.activeModel.ID {
			ms.selectedIdx = i
			// Ensure the active model is visible in the viewport
			listHeight := 8
			ms.ensureVisible(listHeight)
			return
		}
	}
	ms.clampSelection()
}

func (ms *ModelSelector) Close() {
	ms.state = ModelSelectorClosed
}

func (ms *ModelSelector) SetSize(width, height int) {
	if width > 0 {
		ms.width = width
		ms.searchInput.SetWidth(max(0, width-InputPaddingH))
	}
	ms.height = min(height-LayoutGap, SelectorMaxHeight)
}

// SetHasFocus sets the application focus state.
// When the app loses focus, all UI elements should be dimmed.
func (ms *ModelSelector) SetHasFocus(hasFocus bool) {
	ms.hasFocus = hasFocus
	ms.updateSearchInputStyles()
}

// --- Model Management ---

func (ms *ModelSelector) GetActiveModel() *ModelConfig  { return ms.activeModel }
func (ms *ModelSelector) SetActiveModel(m *ModelConfig) { ms.activeModel = m }
func (ms *ModelSelector) GetModels() []ModelConfig      { return ms.models }

func (ms *ModelSelector) SetModels(models []ModelConfig) {
	ms.models = models
	for i := range ms.models {
		ms.models[i].nameLower = strings.ToLower(ms.models[i].Name)
		ms.models[i].protocolTypeLower = strings.ToLower(ms.models[i].ProtocolType)
		ms.models[i].modelNameLower = strings.ToLower(ms.models[i].ModelName)
		ms.models[i].baseURLLower = strings.ToLower(ms.models[i].BaseURL)
	}
	// Reset lastSearchValue to force updateFilteredModels to run
	ms.lastSearchValue = "\x00"
	ms.updateFilteredModels()
}

func (ms *ModelSelector) LoadModels(models []agentpkg.ModelInfo, activeID string) tea.Cmd {
	// Skip update if model list hasn't changed
	if len(models) == ms.lastModelCount && len(models) == len(ms.models) {
		modelsChanged := false
		for i, m := range models {
			if i >= len(ms.models) || ms.models[i].ID != m.ID || ms.models[i].Name != m.Name {
				modelsChanged = true
				break
			}
		}
		if !modelsChanged {
			// Just update active model pointer if needed
			for i := range ms.models {
				if ms.models[i].ID == activeID {
					ms.activeModel = &ms.models[i]
					break
				}
			}
			return nil
		}
	}

	ms.lastModelCount = len(models)
	ms.models = make([]ModelConfig, len(models))

	// Preserve user's selection when selector is open
	savedSelectedIdx := ms.selectedIdx
	savedScrollIdx := ms.scrollIdx
	shouldPreserveSelection := ms.state != ModelSelectorClosed

	for i, m := range models {
		ms.models[i] = ModelConfig{
			ID:                m.ID,
			Name:              m.Name,
			ProtocolType:      m.ProtocolType,
			BaseURL:           m.BaseURL,
			ModelName:         m.ModelName,
			ContextLimit:      m.ContextLimit,
			nameLower:         strings.ToLower(m.Name),
			protocolTypeLower: strings.ToLower(m.ProtocolType),
			modelNameLower:    strings.ToLower(m.ModelName),
			baseURLLower:      strings.ToLower(m.BaseURL),
		}
		if m.ID == activeID {
			ms.activeModel = &ms.models[i]
			// Only set selectedIdx if selector is closed (initial load)
			if !shouldPreserveSelection {
				ms.selectedIdx = i
			}
		}
	}

	// Always update filtered models after model list changes
	// Reset lastSearchValue to force updateFilteredModels to run
	ms.lastSearchValue = "\x00"
	ms.updateFilteredModels()
	if shouldPreserveSelection {
		ms.selectedIdx = savedSelectedIdx
		ms.scrollIdx = savedScrollIdx
		ms.clampSelection()
	}
	return func() tea.Msg { return nil }
}

// --- Action Consumption ---

func (ms *ModelSelector) ConsumeModelSelected() bool {
	if ms.modelJustSelected {
		ms.modelJustSelected = false
		return true
	}
	return false
}

func (ms *ModelSelector) ConsumeOpenModelFile() bool {
	if ms.openModelFile {
		ms.openModelFile = false
		return true
	}
	return false
}

func (ms *ModelSelector) ConsumeReloadModels() bool {
	if ms.reloadModels {
		ms.reloadModels = false
		return true
	}
	return false
}

// --- Bubble Tea Interface ---

func (ms *ModelSelector) Init() tea.Cmd { return nil }

func (ms *ModelSelector) Update(_ tea.Msg) (tea.Model, tea.Cmd) {
	if ms.state == ModelSelectorClosed {
		return ms, nil
	}
	return ms, nil
}

func (ms *ModelSelector) View() tea.View {
	if ms.state == ModelSelectorClosed {
		return tea.NewView("")
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(ms.renderList()))
}

// --- Key Handling ---

func (ms *ModelSelector) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if ms.state == ModelSelectorClosed {
		return nil
	}
	return ms.handleListKeyMsg(msg)
}

func (ms *ModelSelector) handleListKeyMsg(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	// TAB: Toggle focus between search and list
	if key == "tab" {
		ms.searchInputFocused = !ms.searchInputFocused
		if ms.searchInputFocused {
			ms.searchInput.Focus()
		} else {
			ms.searchInput.Blur()
		}
		ms.updateSearchInputStyles()
		return nil
	}

	// Search input handling
	if ms.searchInputFocused {
		return ms.handleSearchInputKey(msg, key)
	}

	// List navigation
	return ms.handleListNavigationKey(key)
}

func (ms *ModelSelector) handleSearchInputKey(msg tea.KeyMsg, key string) tea.Cmd {
	if key == "esc" {
		ms.state = ModelSelectorClosed
		return nil
	}

	if key == "ctrl+c" {
		ms.searchInput.SetValue("")
		ms.updateFilteredModels()
		ms.clampSelection()
		return nil
	}

	if key == "enter" && len(ms.filteredModels) > 0 {
		ms.selectedIdx = 0
		ms.activeModel = &ms.filteredModels[0]
		ms.modelJustSelected = true
		ms.state = ModelSelectorClosed
		return nil
	}

	oldValue := ms.searchInput.Value()
	var cmd tea.Cmd
	ms.searchInput, cmd = ms.searchInput.Update(msg)

	if oldValue != ms.searchInput.Value() {
		ms.updateFilteredModels()
		ms.clampSelection()
	}

	return cmd
}

func (ms *ModelSelector) handleListNavigationKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		if ms.selectedIdx > 0 {
			ms.selectedIdx--
		}
	case "down", "j":
		if ms.selectedIdx < len(ms.filteredModels)-1 {
			ms.selectedIdx++
		}
	case "enter":
		if len(ms.filteredModels) > 0 && ms.selectedIdx >= 0 {
			ms.activeModel = &ms.filteredModels[ms.selectedIdx]
			ms.modelJustSelected = true
			ms.state = ModelSelectorClosed
		}
	case "e":
		ms.openModelFile = true
	case "r":
		ms.reloadModels = true
	case "esc", "q":
		ms.state = ModelSelectorClosed
	}
	return nil
}

// --- Rendering ---

func (ms *ModelSelector) renderList() string {
	var sb strings.Builder

	// When app doesn't have focus, dim all borders
	// Search input with border
	searchBorderColor := ColorAccent
	if !ms.hasFocus || !ms.searchInputFocused {
		searchBorderColor = ColorDim
	}
	searchBox := ms.styles.RenderBorderedBox(ms.searchInput.View(), ms.width, searchBorderColor)

	sb.WriteString(searchBox)
	sb.WriteString("\n")

	// Show current model if set
	if ms.activeModel != nil {
		sb.WriteString(ms.styles.System.Render("Current: "))
		sb.WriteString(ms.styles.Text.Render(ms.activeModel.Name))
		sb.WriteString("\n")
	}

	// Model list - bright border when list is focused
	listBorderColor := ColorAccent
	if !ms.hasFocus || ms.searchInputFocused {
		listBorderColor = ColorDim
	}
	sb.WriteString(ms.renderModelList(lipgloss.Width(searchBox), listBorderColor))

	// Compact command help
	sb.WriteString("\n")
	if ms.searchInputFocused {
		sb.WriteString(ms.styles.System.Render("tab: list │ enter: select │ esc: close"))
	} else {
		sb.WriteString(ms.styles.System.Render("tab: search │ j/k: navigate │ e: edit │ enter: select │ q/esc: close"))
	}

	return sb.String()
}

func (ms *ModelSelector) renderModelList(width int, borderColor string) string {
	var content strings.Builder
	listHeight := 8 // 8 content rows inside border

	if len(ms.models) == 0 {
		content.WriteString(ms.styles.System.Render("No models configured."))
		content.WriteString("\n")
		content.WriteString(ms.styles.System.Render("Press 'e' to edit the model config file."))
	} else if len(ms.filteredModels) == 0 {
		content.WriteString(ms.styles.System.Render("No models match your search."))
	} else {
		ms.ensureVisible(listHeight)
		for i := ms.scrollIdx; i < min(ms.scrollIdx+listHeight, len(ms.filteredModels)); i++ {
			m := ms.filteredModels[i]
			if i == ms.selectedIdx && !ms.searchInputFocused {
				content.WriteString(fmt.Sprintf("> %s", ms.styles.Text.Render(m.Name)))
			} else {
				content.WriteString(fmt.Sprintf("  %s", ms.styles.System.Render(m.Name)))
			}
			if i < min(ms.scrollIdx+listHeight, len(ms.filteredModels))-1 {
				content.WriteString("\n")
			}
		}
	}

	return ms.styles.RenderBorderedBox(content.String(), width, borderColor, listHeight)
}

func (ms *ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.state == ModelSelectorClosed {
		return baseContent
	}

	box := ms.renderList()
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Center horizontally
	x := max(0, (screenWidth-boxWidth)/2)

	// Position above the input box (input box is ~3 lines, status bar is 1 line)
	inputAreaHeight := LayoutGap // input box (3 lines) + status bar (1 line)
	y := max(0, screenHeight-boxHeight-inputAreaHeight)

	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(baseContent),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return c.Render()
}

// --- Helpers ---

// fuzzyMatch checks if all characters in the search term appear in order
// (but not necessarily consecutively) in the target string.
// Both strings should be lowercase for case-insensitive matching.
//
// Examples:
//   - fuzzyMatch("zhipuglm5", "zhipu / glm-5") → true (all chars appear in order)
//   - fuzzyMatch("glm5", "zhipu / glm-5") → true (partial match)
//   - fuzzyMatch("glmzhipu", "zhipu / glm-5") → false (wrong order)
func fuzzyMatch(search, target string) bool {
	if search == "" {
		return true
	}
	if len(search) > len(target) {
		return false
	}

	searchIdx := 0
	for i := 0; i < len(target) && searchIdx < len(search); i++ {
		if search[searchIdx] == target[i] {
			searchIdx++
		}
	}
	return searchIdx == len(search)
}

func (ms *ModelSelector) updateFilteredModels() {
	search := ms.searchInput.Value()
	if search == ms.lastSearchValue {
		return
	}
	ms.lastSearchValue = search

	if search == "" {
		ms.filteredModels = make([]ModelConfig, len(ms.models))
		copy(ms.filteredModels, ms.models)
	} else {
		term := strings.ToLower(search)
		ms.filteredModels = ms.filteredModels[:0]
		for _, m := range ms.models {
			if fuzzyMatch(term, m.nameLower) ||
				fuzzyMatch(term, m.protocolTypeLower) ||
				fuzzyMatch(term, m.modelNameLower) ||
				fuzzyMatch(term, m.baseURLLower) {
				ms.filteredModels = append(ms.filteredModels, m)
			}
		}
	}
	ms.scrollIdx = 0
	ms.clampSelection()
}

func (ms *ModelSelector) clampSelection() {
	if len(ms.filteredModels) == 0 {
		ms.selectedIdx = 0
	} else if ms.selectedIdx >= len(ms.filteredModels) {
		ms.selectedIdx = len(ms.filteredModels) - 1
	}
}

func (ms *ModelSelector) ensureVisible(listHeight int) {
	if ms.selectedIdx < ms.scrollIdx {
		ms.scrollIdx = ms.selectedIdx
	} else if ms.selectedIdx >= ms.scrollIdx+listHeight {
		ms.scrollIdx = ms.selectedIdx - listHeight + 1
	}
}

func (ms *ModelSelector) updateSearchInputStyles() {
	var styles textinput.Styles
	// When app doesn't have focus, always show blurred/dimmed styles
	if ms.searchInputFocused && ms.hasFocus {
		styles = textinput.DefaultStyles(true)
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorAccent)).Bold(true)
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted))
	} else {
		styles = textinput.DefaultStyles(false)
		styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted))
		styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorDim))
	}
	ms.searchInput.SetStyles(styles)
}

// OpenModelConfigFile opens the model config file in the user's editor.
func OpenModelConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("no model config file path configured")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Model configuration file
# Use "---" to separate multiple models

name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return err
		}
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var _ tea.Model = (*ModelSelector)(nil)
