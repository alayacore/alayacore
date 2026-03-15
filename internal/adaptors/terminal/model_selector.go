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
	ms.searchInputFocused = true
	ms.searchInput.Focus()
	ms.updateSearchInputStyles()
	ms.scrollIdx = 0
	ms.updateFilteredModels()
	ms.clampSelection()
}

func (ms *ModelSelector) Close() {
	ms.state = ModelSelectorClosed
}

func (ms *ModelSelector) SetSize(width, height int) {
	if width > 0 {
		ms.width = width * 4 / 5
		ms.searchInput.SetWidth(max(0, width-8))
	}
	ms.height = min(height-4, 30)
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
	ms.updateFilteredModels()
}

func (ms *ModelSelector) LoadModels(models []agentpkg.ModelInfo, activeID string) tea.Cmd {
	ms.lastModelCount = len(models)
	ms.models = make([]ModelConfig, len(models))

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
			ms.selectedIdx = i
		}
	}

	// Force filtered models update by resetting lastSearchValue
	ms.lastSearchValue = "\x00"
	ms.updateFilteredModels()
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

func (ms *ModelSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	// Search input with border
	borderColor := "#89d4fa"
	if !ms.searchInputFocused {
		borderColor = "#45475a"
	}
	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1).
		Render(ms.searchInput.View())

	sb.WriteString(searchBox)
	sb.WriteString("\n\n")

	// Show current model if set
	if ms.activeModel != nil {
		sb.WriteString(ms.styles.System.Render("Current: "))
		sb.WriteString(ms.styles.Text.Render(ms.activeModel.Name))
		sb.WriteString("\n\n")
	}

	// Model list
	sb.WriteString(ms.renderModelList(lipgloss.Width(searchBox)))
	sb.WriteString("\n")

	// Help text
	sb.WriteString(ms.styles.System.Render("─── Commands ───"))
	sb.WriteString("\n")
	if ms.searchInputFocused {
		sb.WriteString(ms.styles.System.Render("tab: list  enter: select  ctrl+c: clear  esc: close"))
	} else {
		sb.WriteString(ms.styles.System.Render("tab: search  j/k: navigate  e: edit  r: reload  enter: select  q/esc: close"))
	}

	return sb.String()
}

func (ms *ModelSelector) renderModelList(width int) string {
	var content strings.Builder
	listHeight := 15

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
				content.WriteString(fmt.Sprintf("> %s\n", ms.styles.Text.Render(m.Name)))
			} else {
				content.WriteString(fmt.Sprintf("  %s\n", ms.styles.System.Render(m.Name)))
			}
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#45475a")).
		Padding(0, 1).
		Width(width).
		Height(listHeight + 2).
		Render(content.String())
}

func (ms *ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.state == ModelSelectorClosed {
		return baseContent
	}

	box := ms.renderList()
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	x := max(0, (screenWidth-boxWidth)/2)
	y := max(0, (screenHeight-boxHeight)/2)

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
	if ms.searchInputFocused {
		styles = textinput.DefaultStyles(true)
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#89d4fa")).Bold(true)
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	} else {
		styles = textinput.DefaultStyles(false)
		styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
		styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
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
