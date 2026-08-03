package agent

// ModelManager is responsible for loading model definitions from a
// key-value config file (model.conf) and managing them in memory.
// All persistence is handled internally; adapters interact via TLV messages.
//
// All methods are called from the session's run() goroutine only, so no
// synchronization is needed.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
)

// ModelManager manages model configurations.
// It owns both the in-memory model list and the config file on disk.
type ModelManager struct {
	models      []config.ModelConfig
	activeID    int
	nextID      int
	filePath    string
	loadErrors  []string // config parse/validation errors from last load
	hasRejected bool     // true when model blocks were present but ALL were rejected (no usable models remain)
}

// DefaultModelConfig is the default model configuration written when config file is empty
const DefaultModelConfig = `name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
`

// KnownProtocolTypes are the protocol types accepted by the provider factory.
var KnownProtocolTypes = map[string]bool{
	"openai":    true,
	"anthropic": true,
}

// NoModelsErrorMessage returns a formatted error message for when no usable models
// are available. If models were found but all rejected, the message reflects that.
func NoModelsErrorMessage(configPath string, hasRejected bool) string {
	var b strings.Builder
	if hasRejected {
		b.WriteString("Error: All models were rejected due to configuration errors.\n")
	} else {
		b.WriteString("Error: No models configured.\n")
	}
	fmt.Fprintf(&b, "Please edit the model config file: %s\n", configPath)
	b.WriteString("\nExample:\n")
	b.WriteString(DefaultModelConfig)
	b.WriteString("\n")
	return b.String()
}

func NewModelManager(configPath string) *ModelManager {
	mm := &ModelManager{
		filePath: configPath,
		nextID:   1, // IDs start from 1; 0 is reserved as "no model"
	}
	if configPath != "" {
		_ = mm.LoadFromFile(configPath) // best-effort load on init
	}
	return mm
}

// GetLoadErrors returns validation messages from the last LoadFromFile call.
// These include both parse errors (e.g. non-numeric value for an int field)
// and model errors (e.g. unknown protocol_type, missing required fields).
func (mm *ModelManager) GetLoadErrors() []string {
	return mm.loadErrors
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

	models, msgs := parseModelConfig(string(data))

	// Reset ID counter and generate IDs for models (start from 1; 0 is reserved as "no model")
	mm.nextID = 1
	for i := range models {
		models[i].ID = mm.nextID
		mm.nextID++
	}

	// Track whether any model blocks were present but rejected
	totalBlocks := len(config.ParseKeyValueBlocks(string(data)))
	mm.hasRejected = totalBlocks > 0 && len(models) == 0 && len(msgs) > 0

	mm.models = models
	mm.loadErrors = msgs

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

// parseModelConfig parses the key-value model config format.
// Returns valid models and a list of validation messages (parse errors and model errors).
func parseModelConfig(content string) ([]config.ModelConfig, []string) {
	var msgs []string

	models, errs := config.ParseModelList(content)
	msgs = append(msgs, errs...)

	// First pass: validate all models and collect names.
	type candidate struct {
		model config.ModelConfig
		index int // original block index (1-based for reporting)
	}
	var validCands = make([]candidate, 0, len(models))
	for i, model := range models {
		if errs := validateModel(model); len(errs) > 0 {
			msgs = append(msgs, errs...)
			continue // skip broken model
		}
		validCands = append(validCands, candidate{model: model, index: i + 1})
	}

	// Second pass: check for duplicate names among valid models.
	// Keep the first occurrence; report and skip subsequent duplicates.
	seenNames := make(map[string]bool)
	result := make([]config.ModelConfig, 0, len(validCands))
	for _, c := range validCands {
		if c.model.Name != "" && seenNames[c.model.Name] {
			msgs = append(msgs, fmt.Sprintf("model.conf block %d: duplicate name %q — skipped", c.index, c.model.Name))
			continue
		}
		seenNames[c.model.Name] = true
		result = append(result, c.model)
	}

	return result, msgs
}

// validateModel checks required fields and returns errors for any issues found.
// A model with errors is unusable and should not be added to the model list.
func validateModel(m config.ModelConfig) []string {
	var errs []string

	if m.ProtocolType == "" {
		errs = append(errs, fmt.Sprintf("model %q: missing required field protocol_type — skipped", m.Name))
	} else if !KnownProtocolTypes[strings.ToLower(m.ProtocolType)] {
		errs = append(errs, fmt.Sprintf("model %q: unknown protocol_type %q (expected \"openai\" or \"anthropic\") — skipped", m.Name, m.ProtocolType))
	}

	if m.BaseURL == "" {
		errs = append(errs, fmt.Sprintf("model %q: missing required field base_url — skipped", m.Name))
	} else if _, err := url.Parse(m.BaseURL); err != nil {
		errs = append(errs, fmt.Sprintf("model %q: invalid base_url %q: %v — skipped", m.Name, m.BaseURL, err))
	}

	if m.ModelName == "" {
		errs = append(errs, fmt.Sprintf("model %q: missing required field model_name — skipped", m.Name))
	}

	return errs
}

func (mm *ModelManager) HasModels() bool {
	return len(mm.models) > 0
}

// HasRejected returns true if model blocks were present but ALL were
// rejected during the last load/sync — i.e. the config produced no usable
// models. Used to distinguish "no models configured" from "models present
// but all invalid" in error messages.
func (mm *ModelManager) HasRejected() bool {
	return mm.hasRejected
}

// GetModels returns all models with full details (including API keys).
func (mm *ModelManager) GetModels() []config.ModelConfig {
	result := make([]config.ModelConfig, len(mm.models))
	copy(result, mm.models)
	return result
}

// SyncFromContent replaces all models with parsed JSON content, persists to the
// config file, and returns validation messages.  The JSON format matches the
// ModelListMsg wire format ([]config.ModelConfig).
func (mm *ModelManager) SyncFromContent(content string) []string {
	var models []config.ModelConfig
	if err := json.Unmarshal([]byte(content), &models); err != nil {
		return []string{fmt.Sprintf("model_sync: invalid JSON: %v", err)}
	}

	valid := make([]config.ModelConfig, 0, len(models))
	var msgs []string
	for i, m := range models {
		if errs := validateModel(m); len(errs) > 0 {
			for _, err := range errs {
				msgs = append(msgs, fmt.Sprintf("model %d: %s", i+1, err))
			}
			continue
		}
		valid = append(valid, m)
	}

	// If all models were rejected, don't touch current state.
	if len(valid) == 0 && len(models) > 0 {
		// Still reflect the outcome in the flag: after this sync there are
		// no usable models (the previous models are kept, but the rejection
		// must be visible). Mirrors LoadFromFile's all-rejected semantics —
		// otherwise HasRejected stays stale and the error message would
		// claim "no models configured" instead of "all models rejected".
		mm.hasRejected = true
		return msgs
	}

	// Assign new IDs
	mm.nextID = 1
	for i := range valid {
		valid[i].ID = mm.nextID
		mm.nextID++
	}

	mm.models = valid
	mm.loadErrors = msgs
	// At least one model is valid (or the sync was empty) — no rejection flag.
	mm.hasRejected = false

	// Persist to config file in key-value format
	if mm.filePath != "" {
		if err := mm.writeConfigFile(); err != nil {
			msgs = append(msgs, fmt.Sprintf("error: failed to persist model config: %v", err))
		}
	}

	return msgs
}

// writeConfigFile persists the current models to the config file in key-value format.
func (mm *ModelManager) writeConfigFile() error {
	data := config.FormatModelList(mm.models)
	return os.WriteFile(mm.filePath, []byte(data), 0600)
}

// GetModel returns a model by ID (includes API key for internal use)
func (mm *ModelManager) GetModel(id int) *config.ModelConfig {
	for i := range mm.models {
		if mm.models[i].ID == id {
			return &mm.models[i]
		}
	}
	return nil
}

// SetActive sets the active model by ID (does NOT persist to file)
func (mm *ModelManager) SetActive(id int) error {
	// Verify the model exists
	for _, m := range mm.models {
		if m.ID == id {
			mm.activeID = id
			return nil
		}
	}
	return fmt.Errorf("model_set: model not found: %d", id)
}

// Returns false if there are no models.
func (mm *ModelManager) SetActiveToFirst() bool {
	if len(mm.models) == 0 {
		return false
	}
	mm.activeID = mm.models[0].ID
	return true
}

// GetActive returns the active model (includes API key)
func (mm *ModelManager) GetActive() *config.ModelConfig {
	for _, m := range mm.models {
		if m.ID == mm.activeID {
			return &m
		}
	}
	return nil
}

func (mm *ModelManager) GetActiveID() int {
	return mm.activeID
}

func (mm *ModelManager) GetFilePath() string {
	return mm.filePath
}

func (mm *ModelManager) FindModelByName(name string) int {
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
		return fmt.Errorf("model_set: model not found: %q", name)
	}
	return mm.SetActive(id)
}
