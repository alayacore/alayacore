package config

import (
	"fmt"
	"strings"
)

// ModelConfig represents a model configuration.
// JSON tags are used for TLV serialization to adapters.
type ModelConfig struct {
	ID           int    `json:"id" config:"-"`                                  // Runtime ID (generated, not persisted)
	Name         string `json:"name" config:"name"`                             // Display name
	ProtocolType string `json:"protocol_type" config:"protocol_type"`           // "openai" or "anthropic"
	BaseURL      string `json:"base_url" config:"base_url"`                     // API server URL
	APIKey       string `json:"api_key" config:"api_key"`                       // API key
	ModelName    string `json:"model_name" config:"model_name"`                 // Model identifier
	ContextLimit int    `json:"context_limit" config:"context_limit,omitempty"` // Maximum context length (0 means unlimited)
	MaxTokens    int    `json:"max_tokens" config:"max_tokens,omitempty"`       // Maximum output tokens (0 means use provider default)
}

// FormatModelList formats a slice of ModelConfig to key-value block format.
// Blocks are separated by "---". The output has no trailing blank line.
func FormatModelList(models []ModelConfig) string {
	if len(models) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(models))
	for _, m := range models {
		blocks = append(blocks, strings.TrimSuffix(FormatKeyValue(m), "\n"))
	}
	return strings.Join(blocks, "\n---\n") + "\n"
}

// ParseModelList parses key-value block format into a slice of ModelConfig.
// Returns models with a non-empty Name or ModelName, and any parse errors.
// Does NOT validate model fields — callers should validate after this.
func ParseModelList(content string) ([]ModelConfig, []string) {
	blocks := ParseKeyValueBlocks(content)
	models := make([]ModelConfig, 0, len(blocks))
	var errs []string

	for blockIdx, block := range blocks {
		blockModels, blockErrs := parseModelBlock(block, blockIdx)
		models = append(models, blockModels...)
		errs = append(errs, blockErrs...)
	}

	return models, errs
}

// parseModelBlock parses a single model block. Returns the models the
// block yields (at most one; nil for blank or nameless blocks) and any
// parse errors with the block index baked into the messages.
func parseModelBlock(block string, blockIdx int) ([]ModelConfig, []string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return nil, nil
	}

	var m ModelConfig
	parseErrs := ParseKeyValue(block, &m)
	errs := make([]string, 0, len(parseErrs))
	for _, e := range parseErrs {
		errs = append(errs, fmt.Sprintf("model.conf block %d: %s", blockIdx+1, e.String()))
	}

	if m.Name == "" && m.ModelName == "" {
		return nil, errs
	}
	return []ModelConfig{m}, errs
}
