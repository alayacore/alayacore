package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/wallacegibbon/alayacore/internal/agent"
)

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID           string `json:"id,omitempty"` // Runtime ID (not persisted)
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key,omitempty"` // Omitted in responses for security
	ModelName    string `json:"model_name"`
}

// ModelSelectorState represents the current state of the model selector
type ModelSelectorState int

const (
	ModelSelectorClosed ModelSelectorState = iota
	ModelSelectorList                      // Showing list of models
)

// ModelSelector manages model selection and configuration
type ModelSelector struct {
	state             ModelSelectorState
	models            []ModelConfig
	selectedIdx       int // Selected model in list
	width             int
	height            int
	styles            *Styles
	activeModel       *ModelConfig // Currently active model
	modelJustSelected bool         // True if model was just selected this frame
	openModelFile     bool         // True if user requested to open model file
	reloadModels      bool         // True if user requested to reload models
}

// NewModelSelector creates a new model selector
func NewModelSelector(styles *Styles) *ModelSelector {
	ms := &ModelSelector{
		state:       ModelSelectorClosed,
		models:      []ModelConfig{},
		selectedIdx: 0,
		styles:      styles,
		width:       60,
		height:      20,
	}
	_ = ms.LoadModels() // Load saved models
	return ms
}

// IsOpen returns true if the model selector is open
func (ms *ModelSelector) IsOpen() bool {
	return ms.state != ModelSelectorClosed
}

// Open opens the model selector in list mode
func (ms *ModelSelector) Open() {
	ms.state = ModelSelectorList
	if len(ms.models) == 0 {
		ms.selectedIdx = 0
	} else if ms.selectedIdx >= len(ms.models) {
		ms.selectedIdx = len(ms.models) - 1
	}
}

// Close closes the model selector
func (ms *ModelSelector) Close() {
	ms.state = ModelSelectorClosed
}

// State returns the current state
func (ms *ModelSelector) State() ModelSelectorState {
	return ms.state
}

// SetSize sets the dimensions
func (ms *ModelSelector) SetSize(width, height int) {
	ms.width = min(width-4, 80)
	ms.height = min(height-4, 30)
}

// Init initializes the model
func (ms *ModelSelector) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (ms *ModelSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch ms.state {
	case ModelSelectorList:
		return ms.updateList(msg)
	}
	return ms, nil
}

// updateList handles list view updates
func (ms *ModelSelector) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if ms.selectedIdx > 0 {
				ms.selectedIdx--
			}
		case "down", "j":
			if ms.selectedIdx < len(ms.models)-1 {
				ms.selectedIdx++
			}
		case "enter":
			if len(ms.models) > 0 && ms.selectedIdx >= 0 {
				ms.activeModel = &ms.models[ms.selectedIdx]
				ms.modelJustSelected = true
				ms.state = ModelSelectorClosed
			}
		case "e":
			// Open model file with $EDITOR
			ms.openModelFile = true
		case "r":
			// Reload models from file
			ms.reloadModels = true
		case "esc", "q":
			ms.state = ModelSelectorClosed
		}
	}
	return ms, nil
}

// View renders the model selector
func (ms *ModelSelector) View() tea.View {
	if ms.state == ModelSelectorClosed {
		return tea.NewView("")
	}

	content := ms.renderList()

	// Create centered overlay
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#89b4fa")).
		Padding(1, 2).
		Width(ms.width).
		MaxHeight(ms.height)

	return tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(boxStyle.Render(content)))
}

// renderList renders the model list
func (ms *ModelSelector) renderList() string {
	var sb strings.Builder

	title := ms.styles.TodoHeader.Render("SELECT MODEL")
	sb.WriteString(title)
	sb.WriteString("\n\n")

	if len(ms.models) == 0 {
		sb.WriteString(ms.styles.Pending.Render("No models configured."))
		sb.WriteString("\n")
		sb.WriteString(ms.styles.System.Render("Press 'e' to edit the model config file."))
	} else {
		for i, m := range ms.models {
			var line string
			prefix := "  "
			if i == ms.selectedIdx {
				prefix = "> "
				line = fmt.Sprintf("%s%s", prefix, ms.styles.InProgress.Render(m.Name))
			} else {
				line = fmt.Sprintf("%s%s", prefix, ms.styles.Pending.Render(m.Name))
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(ms.styles.System.Render("─── Commands ───"))
	sb.WriteString("\n")
	sb.WriteString(ms.styles.System.Render("e: edit file  r: reload  enter: select  esc: close"))

	return sb.String()
}

// GetActiveModel returns the currently active model (may be nil)
func (ms *ModelSelector) GetActiveModel() *ModelConfig {
	return ms.activeModel
}

// SetActiveModel sets the active model directly
func (ms *ModelSelector) SetActiveModel(m *ModelConfig) {
	ms.activeModel = m
}

// ConsumeModelSelected returns true if a model was just selected and resets the flag
func (ms *ModelSelector) ConsumeModelSelected() bool {
	if ms.modelJustSelected {
		ms.modelJustSelected = false
		return true
	}
	return false
}

// ConsumeOpenModelFile returns true if user requested to open model file and resets the flag
func (ms *ModelSelector) ConsumeOpenModelFile() bool {
	if ms.openModelFile {
		ms.openModelFile = false
		return true
	}
	return false
}

// ConsumeReloadModels returns true if user requested to reload models and resets the flag
func (ms *ModelSelector) ConsumeReloadModels() bool {
	if ms.reloadModels {
		ms.reloadModels = false
		return true
	}
	return false
}

// GetModels returns all saved models
func (ms *ModelSelector) GetModels() []ModelConfig {
	return ms.models
}

// SetModels sets the models list
func (ms *ModelSelector) SetModels(models []ModelConfig) {
	ms.models = models
}

// LoadFromManager loads models from the session's ModelManager
func (ms *ModelSelector) LoadFromManager(mm *agentpkg.ModelManager) {
	if mm == nil {
		return
	}

	models := mm.GetModels()
	ms.models = make([]ModelConfig, len(models))
	for i, m := range models {
		ms.models[i] = ModelConfig{
			ID:           m.ID,
			Name:         m.Name,
			ProtocolType: m.ProtocolType,
			BaseURL:      m.BaseURL,
			ModelName:    m.ModelName,
		}
		// Set active model
		if m.IsActive {
			ms.activeModel = &ms.models[i]
			ms.selectedIdx = i
		}
	}

	// Also get API keys from the manager
	for i, m := range mm.GetModels() {
		fullModel := mm.GetModel(m.ID)
		if fullModel != nil {
			ms.models[i].APIKey = fullModel.APIKey
		}
	}
}

// Helper function to truncate strings
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// SetWidth sets the width
func (ms *ModelSelector) SetWidth(width int) {
	ms.width = min(width-4, 80)
}

// HandleKey handles key events directly (for integration with Terminal)
func (ms *ModelSelector) HandleKey(key string) bool {
	if ms.state == ModelSelectorClosed {
		if key == "ctrl+l" {
			ms.Open()
			return true
		}
		return false
	}

	switch ms.state {
	case ModelSelectorList:
		return ms.handleListKey(key)
	}
	return false
}

// handleListKey handles keys in list mode, returns true if handled
func (ms *ModelSelector) handleListKey(key string) bool {
	switch key {
	case "up", "k":
		if ms.selectedIdx > 0 {
			ms.selectedIdx--
		}
		return true
	case "down", "j":
		if ms.selectedIdx < len(ms.models)-1 {
			ms.selectedIdx++
		}
		return true
	case "enter":
		if len(ms.models) > 0 && ms.selectedIdx >= 0 {
			ms.activeModel = &ms.models[ms.selectedIdx]
			ms.state = ModelSelectorClosed
			_ = ms.SaveActiveModel() // Persist active selection
		}
		return true
	case "e":
		// Open model file with $EDITOR
		ms.openModelFile = true
		return true
	case "r":
		// Reload models from file
		ms.reloadModels = true
		return true
	case "esc", "q":
		ms.state = ModelSelectorClosed
		return true
	}
	return false
}

// RenderOverlay returns the model selector as a centered overlay on top of base content
// Returns baseContent if closed, otherwise returns baseContent with overlay positioned on top
func (ms *ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.state == ModelSelectorClosed {
		return baseContent
	}

	content := ms.renderList()

	// Create the box with content
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#89b4fa")).
		Padding(1, 2).
		Width(ms.width).
		MaxHeight(ms.height)

	box := boxStyle.Render(content)

	// Get actual rendered box dimensions
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Calculate center position
	x := (screenWidth - boxWidth) / 2
	if x < 0 {
		x = 0
	}
	y := (screenHeight - boxHeight) / 2
	if y < 0 {
		y = 0
	}

	// Create layers
	baseLayer := lipgloss.NewLayer(baseContent)
	overlayLayer := lipgloss.NewLayer(box).X(x).Y(y).Z(1)

	// Compose and render
	c := lipgloss.NewCompositor(baseLayer, overlayLayer)
	return c.Render()
}

// RenderString returns the rendered string for embedding (simple version)
func (ms *ModelSelector) RenderString() string {
	if ms.state == ModelSelectorClosed {
		return ""
	}

	content := ms.renderList()

	// Create bordered box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#89b4fa")).
		Padding(1, 2).
		Width(ms.width).
		MaxHeight(ms.height)

	return boxStyle.Render(content)
}

var _ tea.Model = (*ModelSelector)(nil)

// ============================================================================
// Persistence
// ============================================================================

// ModelsConfigFile returns the path to the models configuration file
func ModelsConfigFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".alayacore")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "models.json"), nil
}

// SaveModels saves the model configurations to disk
func (ms *ModelSelector) SaveModels() error {
	path, err := ModelsConfigFile()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(ms.models, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600) // 0600 for security (contains API keys)
}

// LoadModels loads the model configurations from disk
func (ms *ModelSelector) LoadModels() error {
	path, err := ModelsConfigFile()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file yet, that's OK
		}
		return err
	}
	// Try to load with wrapper (new format)
	var wrapper struct {
		Models      []ModelConfig `json:"models"`
		ActiveIndex int           `json:"active_index,omitempty"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Models) > 0 {
		ms.models = wrapper.Models
		if wrapper.ActiveIndex >= 0 && wrapper.ActiveIndex < len(ms.models) {
			ms.activeModel = &ms.models[wrapper.ActiveIndex]
			ms.selectedIdx = wrapper.ActiveIndex
		}
		return nil
	}
	// Fall back to old format (just array)
	return json.Unmarshal(data, &ms.models)
}

// SetInitialModel sets the initial model from the current configuration
// This is called on startup to populate the model selector with the CLI-provided model
func (ms *ModelSelector) SetInitialModel(protocolType, baseURL, apiKey, modelName string) {
	// If we already have an active model from saved config, use it
	if ms.activeModel != nil {
		return
	}

	// Check if this model already exists in the list
	for i, m := range ms.models {
		if m.ProtocolType == protocolType && m.BaseURL == baseURL && m.ModelName == modelName {
			ms.activeModel = &ms.models[i]
			ms.selectedIdx = i
			return
		}
	}

	// Add the current model to the list if not present
	newModel := ModelConfig{
		Name:         modelName + " (CLI)",
		ProtocolType: protocolType,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ModelName:    modelName,
	}
	ms.models = append(ms.models, newModel)
	ms.activeModel = &ms.models[len(ms.models)-1]
	ms.selectedIdx = len(ms.models) - 1
	_ = ms.SaveModels() // Persist the new model
}

// SaveActiveModel saves the active model index to disk
func (ms *ModelSelector) SaveActiveModel() error {
	path, err := ModelsConfigFile()
	if err != nil {
		return err
	}
	// Read existing data first
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var models []ModelConfig
	if len(data) > 0 {
		if err := json.Unmarshal(data, &models); err != nil {
			return err
		}
	}
	// Write with active index metadata
	wrapper := struct {
		Models      []ModelConfig `json:"models"`
		ActiveIndex int           `json:"active_index,omitempty"`
	}{
		Models:      ms.models,
		ActiveIndex: -1,
	}
	// Find active model index
	if ms.activeModel != nil {
		for i, m := range ms.models {
			if m.Name == ms.activeModel.Name {
				wrapper.ActiveIndex = i
				break
			}
		}
	}
	out, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

// OpenModelConfigFile opens the model config file in the user's editor
func OpenModelConfigFile() error {
	path, err := ModelsConfigFile()
	if err != nil {
		return err
	}

	// Create file if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create with empty array
		if err := os.WriteFile(path, []byte("{\n  \"models\": []\n}"), 0600); err != nil {
			return err
		}
	}

	// Get editor from environment
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	// Open editor
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
