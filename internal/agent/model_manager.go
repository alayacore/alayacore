package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID           string `json:"id"`                // Runtime ID (generated, not persisted)
	Name         string `json:"name"`              // Display name
	ProtocolType string `json:"protocol_type"`     // "openai" or "anthropic"
	BaseURL      string `json:"base_url"`          // API server URL
	APIKey       string `json:"api_key,omitempty"` // API key (omitted in JSON responses for security)
	ModelName    string `json:"model_name"`        // Model identifier
}

// ModelInfo is the safe version for JSON responses (no API key)
type ModelInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	ModelName    string `json:"model_name"`
	IsActive     bool   `json:"is_active"`
}

// ModelListResponse is the response for model_get_all command
type ModelListResponse struct {
	Models   []ModelInfo `json:"models"`
	ActiveID string      `json:"active_id,omitempty"`
}

// ModelManager manages model configurations
type ModelManager struct {
	models   []ModelConfig
	activeID string
	mu       sync.RWMutex
	filePath string
}

// NewModelManager creates a new model manager
func NewModelManager() *ModelManager {
	path, err := modelsConfigFile()
	if err != nil {
		path = ""
	}
	mm := &ModelManager{
		filePath: path,
	}
	if path != "" {
		_ = mm.LoadFromFile(path)
	}
	return mm
}

// modelsConfigFile returns the path to the models configuration file
func modelsConfigFile() (string, error) {
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

// LoadFromFile loads models from a JSON file
func (mm *ModelManager) LoadFromFile(path string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file yet, that's OK
		}
		return err
	}

	// Try to load with wrapper (new format with active_index)
	var wrapper struct {
		Models      []ModelConfig `json:"models"`
		ActiveIndex int           `json:"active_index,omitempty"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}

	// Generate IDs for models that don't have one
	for i := range wrapper.Models {
		if wrapper.Models[i].ID == "" {
			wrapper.Models[i].ID = uuid.New().String()[:8]
		}
	}

	mm.models = wrapper.Models
	if wrapper.ActiveIndex >= 0 && wrapper.ActiveIndex < len(mm.models) {
		mm.activeID = mm.models[wrapper.ActiveIndex].ID
	}

	if mm.filePath == "" {
		mm.filePath = path
	}

	return nil
}

// SaveToFile saves models to a JSON file
func (mm *ModelManager) SaveToFile(path string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if path == "" {
		path = mm.filePath
	}
	if path == "" {
		return fmt.Errorf("no file path specified")
	}

	// Find active index
	activeIndex := -1
	for i, m := range mm.models {
		if m.ID == mm.activeID {
			activeIndex = i
			break
		}
	}

	// Clear API keys before saving (for security)
	modelsToSave := make([]ModelConfig, len(mm.models))
	for i, m := range mm.models {
		modelsToSave[i] = m
		// Keep API key in saved file - user needs it
	}

	wrapper := struct {
		Models      []ModelConfig `json:"models"`
		ActiveIndex int           `json:"active_index,omitempty"`
	}{
		Models:      modelsToSave,
		ActiveIndex: activeIndex,
	}

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600) // 0600 for security (contains API keys)
}

// Save saves to the default file path
func (mm *ModelManager) Save() error {
	return mm.SaveToFile("")
}

// AddModel adds a new model and returns its ID
func (mm *ModelManager) AddModel(m ModelConfig) string {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}
	mm.models = append(mm.models, m)
	return m.ID
}

// GetModels returns all models (without API keys)
func (mm *ModelManager) GetModels() []ModelInfo {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	result := make([]ModelInfo, len(mm.models))
	for i, m := range mm.models {
		result[i] = ModelInfo{
			ID:           m.ID,
			Name:         m.Name,
			ProtocolType: m.ProtocolType,
			BaseURL:      m.BaseURL,
			ModelName:    m.ModelName,
			IsActive:     m.ID == mm.activeID,
		}
	}
	return result
}

// GetModel returns a model by ID (includes API key for internal use)
func (mm *ModelManager) GetModel(id string) *ModelConfig {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	for _, m := range mm.models {
		if m.ID == id {
			return &m
		}
	}
	return nil
}

// SetActive sets the active model by ID
func (mm *ModelManager) SetActive(id string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Verify the model exists
	for _, m := range mm.models {
		if m.ID == id {
			mm.activeID = id
			return nil
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

// GetActive returns the active model (includes API key)
func (mm *ModelManager) GetActive() *ModelConfig {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	for _, m := range mm.models {
		if m.ID == mm.activeID {
			return &m
		}
	}
	return nil
}

// GetActiveID returns the active model ID
func (mm *ModelManager) GetActiveID() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.activeID
}

// DeleteModel removes a model by ID
func (mm *ModelManager) DeleteModel(id string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	for i, m := range mm.models {
		if m.ID == id {
			mm.models = append(mm.models[:i], mm.models[i+1:]...)
			if mm.activeID == id {
				mm.activeID = ""
				if len(mm.models) > 0 {
					mm.activeID = mm.models[0].ID
				}
			}
			return nil
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

// UpdateModel updates a model by ID
func (mm *ModelManager) UpdateModel(id string, m ModelConfig) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	m.ID = id // Preserve the ID
	for i, existing := range mm.models {
		if existing.ID == id {
			mm.models[i] = m
			return nil
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

// SetInitialModel sets the initial model from CLI args if no active model exists
func (mm *ModelManager) SetInitialModel(protocolType, baseURL, apiKey, modelName string) string {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// If we already have an active model, return its ID
	if mm.activeID != "" {
		return mm.activeID
	}

	// Check if this model already exists
	for _, m := range mm.models {
		if m.ProtocolType == protocolType && m.BaseURL == baseURL && m.ModelName == modelName {
			mm.activeID = m.ID
			return m.ID
		}
	}

	// Add the CLI model
	newModel := ModelConfig{
		ID:           uuid.New().String()[:8],
		Name:         modelName + " (CLI)",
		ProtocolType: protocolType,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ModelName:    modelName,
	}
	mm.models = append(mm.models, newModel)
	mm.activeID = newModel.ID
	_ = mm.Save() // Persist

	return newModel.ID
}

// GetFilePath returns the current file path
func (mm *ModelManager) GetFilePath() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.filePath
}
