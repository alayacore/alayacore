package agent

// ModelManager is responsible for loading model definitions from a
// YAML-like config file (model.conf) and managing them in memory.
// It never writes to the config file – all persistence is manual via
// a text editor. The session layer uses ModelManager only through its
// query/update methods and receives safe JSON-ready views via ModelInfo.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	ContextLimit int    `json:"context_limit"`     // Maximum context length (0 means unlimited)
}

// ModelInfo is the safe version for JSON responses (no API key)
type ModelInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	ModelName    string `json:"model_name"`
	ContextLimit int    `json:"context_limit"`
	IsActive     bool   `json:"is_active"`
}

// ModelListResponse is the response for model_get_all command
type ModelListResponse struct {
	Models   []ModelInfo `json:"models"`
	ActiveID string      `json:"active_id,omitempty"`
}

// ModelManager manages model configurations
// NOTE: ModelManager NEVER writes to the model config file.
// Users must edit the file with a text editor (press 'e' in model selector).
type ModelManager struct {
	models   []ModelConfig
	activeID string
	mu       sync.RWMutex
	filePath string
}

// NewModelManager creates a new model manager
// If configPath is empty, uses the default path (~/.alayacore/models.json)
func NewModelManager(configPath string) *ModelManager {
	var path string
	var err error

	if configPath != "" {
		// Use user-specified path
		path = configPath
	} else {
		// Use default path
		path, err = defaultModelsConfigFile()
		if err != nil {
			path = ""
		}
	}

	mm := &ModelManager{
		filePath: path,
	}
	if path != "" {
		_ = mm.LoadFromFile(path)
	}
	return mm
}

// defaultModelsConfigFile returns the default path to the models configuration file
func defaultModelsConfigFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".alayacore")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "model.conf"), nil
}

// LoadFromFile loads models from a config file in YAML-like format
// NOTE: This does NOT create the file if it doesn't exist.
// The user must create and edit the file manually.
//
// File format:
//
//	name: "Model Name"
//	protocol_type: "openai"
//	base_url: "https://api.example.com/v1"
//	api_key: "your-api-key"
//	model_name: "gpt-4o"
//	---
//	name: "Another Model"
//	...
func (mm *ModelManager) LoadFromFile(path string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - user needs to create it
			return nil
		}
		return err
	}

	models := parseModelConfig(string(data))

	// Generate IDs for models that don't have one
	for i := range models {
		if models[i].ID == "" {
			models[i].ID = uuid.New().String()[:8]
		}
	}

	mm.models = models
	// Set active to the last model
	if len(mm.models) > 0 {
		mm.activeID = mm.models[len(mm.models)-1].ID
	}

	if mm.filePath == "" {
		mm.filePath = path
	}

	return nil
}

// parseModelConfig parses the YAML-like model config format
func parseModelConfig(content string) []ModelConfig {
	var models []ModelConfig

	// Split by "\n---\n" to get individual model blocks
	blocks := strings.Split(content, "\n---\n")

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		model := parseModelBlock(block)
		if model.Name != "" || model.ModelName != "" {
			models = append(models, model)
		}
	}

	return models
}

// parseModelBlock parses a single model block
func parseModelBlock(block string) ModelConfig {
	model := ModelConfig{}
	lines := strings.Split(block, "\n")

	// Regex to match: key: "value" or key: 'value' or key: value
	re := regexp.MustCompile(`^(\w+):\s*["']?(.+?)["']?\s*$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := matches[1]
			value := matches[2]

			// Remove surrounding quotes if present
			if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
				(strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`)) {
				value = value[1 : len(value)-1]
			}

			switch key {
			case "name":
				model.Name = value
			case "protocol_type":
				model.ProtocolType = value
			case "base_url":
				model.BaseURL = value
			case "api_key":
				model.APIKey = value
			case "model_name":
				model.ModelName = value
			case "context_limit":
				if limit, err := strconv.Atoi(value); err == nil {
					model.ContextLimit = limit
				}
			}
		}
	}

	return model
}

// Reload reloads models from the config file
func (mm *ModelManager) Reload() error {
	if mm.filePath == "" {
		return fmt.Errorf("no config file path set")
	}
	return mm.LoadFromFile(mm.filePath)
}

// HasModels returns true if there are any models available
func (mm *ModelManager) HasModels() bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return len(mm.models) > 0
}

// AddModel adds a new model to the runtime list (does NOT persist to file)
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
			ContextLimit: m.ContextLimit,
			IsActive:     m.ID == mm.activeID,
		}
	}
	return result
}

// GetModel returns a model by ID (includes API key for internal use)
func (mm *ModelManager) GetModel(id string) *ModelConfig {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	for i := range mm.models {
		if mm.models[i].ID == id {
			return &mm.models[i]
		}
	}
	return nil
}

// SetActive sets the active model by ID (does NOT persist to file)
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

// DeleteModel removes a model by ID from runtime list (does NOT persist to file)
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

// UpdateModel updates a model by ID in runtime list (does NOT persist to file)
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

// GetFilePath returns the current file path
func (mm *ModelManager) GetFilePath() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.filePath
}

// ModelCount returns the number of models in the runtime list
func (mm *ModelManager) ModelCount() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return len(mm.models)
}

// FindModelByName finds a model by its name and returns its ID
func (mm *ModelManager) FindModelByName(name string) string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	for _, m := range mm.models {
		if m.Name == name {
			return m.ID
		}
	}
	return ""
}

// SetActiveByName sets the active model by name (does NOT persist to file)
func (mm *ModelManager) SetActiveByName(name string) error {
	id := mm.FindModelByName(name)
	if id == "" {
		return fmt.Errorf("model not found: %s", name)
	}
	return mm.SetActive(id)
}
