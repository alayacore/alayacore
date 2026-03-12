package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID           string `json:"id,omitempty"` // Runtime ID (not persisted)
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key,omitempty"` // Omitted in responses for security
	ModelName    string `json:"model_name"`
	ContextLimit int    `json:"context_limit"` // Maximum context length (0 means unlimited)
}

// ModelSelectorState represents the current state of the model selector
type ModelSelectorState int

const (
	ModelSelectorClosed ModelSelectorState = iota
	ModelSelectorList                      // Showing list of models
)

// ModelSelector manages model selection and configuration
// NOTE: ModelSelector NEVER writes to the model config file.
// Users must edit the file with a text editor (press 'e').
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
	// Note: We don't load models here anymore - they're loaded from ModelManager
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

	title := ms.styles.Tool.Render("SELECT MODEL")
	sb.WriteString(title)
	sb.WriteString("\n\n")

	if len(ms.models) == 0 {
		sb.WriteString(ms.styles.System.Render("No models configured."))
		sb.WriteString("\n")
		sb.WriteString(ms.styles.System.Render("Press 'e' to edit the model config file."))
	} else {
		for i, m := range ms.models {
			var line string
			prefix := "  "
			if i == ms.selectedIdx {
				prefix = "> "
				line = fmt.Sprintf("%s%s", prefix, ms.styles.Text.Render(m.Name))
			} else {
				line = fmt.Sprintf("%s%s", prefix, ms.styles.System.Render(m.Name))
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
			ContextLimit: m.ContextLimit,
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
			// Note: We do NOT save to file - user must edit file manually
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

// OpenModelConfigFile opens the model config file in the user's editor
// path should be obtained from ModelManager.GetFilePath()
func OpenModelConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("no model config file path configured")
	}

	// Create file with template if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Model configuration file
# Use "---" to separate multiple models

name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
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

var _ tea.Model = (*ModelSelector)(nil)
