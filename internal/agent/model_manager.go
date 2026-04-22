package agent

// ModelManager is responsible for loading model definitions from a
// key-value config file (model.conf) and managing them in memory.
// It never writes to the config file – all persistence is manual via
// a text editor. The session layer uses ModelManager only through its
// query/update methods and receives safe JSON-ready views via ModelInfo.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/config"
)

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID           int    `json:"id"`                                   // Runtime ID (generated, not persisted)
	Name         string `json:"name" config:"name"`                   // Display name
	ProtocolType string `json:"protocol_type" config:"protocol_type"` // "openai" or "anthropic"
	BaseURL      string `json:"base_url" config:"base_url"`           // API server URL
	APIKey       string `json:"api_key,omitempty" config:"api_key"`   // API key (omitted in JSON responses for security)
	ModelName    string `json:"model_name" config:"model_name"`       // Model identifier
	ContextLimit int    `json:"context_limit" config:"context_limit"` // Maximum context length (0 means unlimited)
	MaxTokens    int    `json:"max_tokens" config:"max_tokens"`       // Maximum output tokens (0 means use provider default)
	PromptCache  bool   `json:"prompt_cache" config:"prompt_cache"`   // Enable prompt caching (adds cache_control for Anthropic)
}

// ModelInfo is the safe version for JSON responses (no API key)
type ModelInfo struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	ModelName    string `json:"model_name"`
	ContextLimit int    `json:"context_limit"`
	MaxTokens    int    `json:"max_tokens"`
	PromptCache  bool   `json:"prompt_cache"`
	IsActive     bool   `json:"is_active"`
}

// ModelManager manages model configurations
// NOTE: ModelManager NEVER writes to the model config file.
// Users must edit the file with a text editor (press 'e' in model selector).
type ModelManager struct {
	models   []ModelConfig
	activeID int
	nextID   int
	mu       sync.RWMutex
	filePath string
}

// DefaultModelConfig is the default model configuration written when config file is empty
const DefaultModelConfig = `---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
---
`

// NewModelManager creates a new model manager
// If configPath is empty, uses the default path (~/.alayacore/model.conf)
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
		nextID:   1, // IDs start from 1; 0 is reserved as "no model"
	}
	if path != "" {
		_ = mm.LoadFromFile(path) //nolint:errcheck // best-effort load on init
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

// LoadFromFile loads models from a config file in key-value format
// If the file doesn't exist or is empty, it creates the file with default config.
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
			// File doesn't exist - create it with default config
			if createErr := mm.createDefaultConfig(path); createErr != nil {
				return createErr
			}
			data = []byte(DefaultModelConfig)
		} else {
			return err
		}
	}

	// If file is empty, write default config
	if len(strings.TrimSpace(string(data))) == 0 {
		if err := mm.createDefaultConfig(path); err != nil {
			return err
		}
		data = []byte(DefaultModelConfig)
	}

	models := parseModelConfig(string(data))

	// Reset ID counter and generate IDs for models (start from 1; 0 is reserved as "no model")
	mm.nextID = 1
	for i := range models {
		models[i].ID = mm.nextID
		mm.nextID++
	}

	mm.models = models

	if mm.filePath == "" {
		mm.filePath = path
	}

	return nil
}

// createDefaultConfig creates a default model config file
func (mm *ModelManager) createDefaultConfig(path string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(DefaultModelConfig), 0600)
}

// parseModelConfig parses the key-value model config format
func parseModelConfig(content string) []ModelConfig {
	var models []ModelConfig

	// Split by "\n---\n" to get individual model blocks
	blocks := config.ParseKeyValueBlocks(content)

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		var model ModelConfig
		config.ParseKeyValue(block, &model)
		if model.Name != "" || model.ModelName != "" {
			models = append(models, model)
		}
	}

	return models
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
func (mm *ModelManager) AddModel(m ModelConfig) int {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	m.ID = mm.nextID
	mm.nextID++
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
			MaxTokens:    m.MaxTokens,
			PromptCache:  m.PromptCache,
			IsActive:     m.ID == mm.activeID,
		}
	}
	return result
}

// GetModel returns a model by ID (includes API key for internal use)
func (mm *ModelManager) GetModel(id int) *ModelConfig {
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
func (mm *ModelManager) SetActive(id int) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Verify the model exists
	for _, m := range mm.models {
		if m.ID == id {
			mm.activeID = id
			return nil
		}
	}
	return fmt.Errorf("model not found: %d", id)
}

// SetActiveToFirst sets the active model to the first one in the list.
// Returns false if there are no models.
func (mm *ModelManager) SetActiveToFirst() bool {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if len(mm.models) == 0 {
		return false
	}
	if len(mm.models) > 0 {
		mm.activeID = mm.models[0].ID
	}
	return true
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
func (mm *ModelManager) GetActiveID() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.activeID
}

// DeleteModel removes a model by ID from runtime list (does NOT persist to file)
func (mm *ModelManager) DeleteModel(id int) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	for i, m := range mm.models {
		if m.ID == id {
			mm.models = append(mm.models[:i], mm.models[i+1:]...)
			if mm.activeID == id {
				mm.activeID = 0
				if len(mm.models) > 0 {
					mm.activeID = mm.models[0].ID
				}
			}
			return nil
		}
	}
	return fmt.Errorf("model not found: %d", id)
}

// UpdateModel updates a model by ID in runtime list (does NOT persist to file)
func (mm *ModelManager) UpdateModel(id int, m ModelConfig) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	m.ID = id // Preserve the ID
	for i, existing := range mm.models {
		if existing.ID == id {
			mm.models[i] = m
			return nil
		}
	}
	return fmt.Errorf("model not found: %d", id)
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
func (mm *ModelManager) FindModelByName(name string) int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	for _, m := range mm.models {
		if m.Name == name {
			return m.ID
		}
	}
	return 0
}

// SetActiveByName sets the active model by name (does NOT persist to file)
func (mm *ModelManager) SetActiveByName(name string) error {
	id := mm.FindModelByName(name)
	if id == 0 {
		return fmt.Errorf("model not found: %s", name)
	}
	return mm.SetActive(id)
}
