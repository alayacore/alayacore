package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/protocol"
)

// modelConfig represents a model configuration.
// config tags drive model.conf parsing; json tags cover the :model_sync
// JSON exchange. The model_list system message uses protocol.ModelInfo
// (see toModelInfos) so adapters never need this type.
type modelConfig struct {
	ID           int             `json:"id" config:"-"`                                        // Runtime ID (generated, not persisted)
	Name         string          `json:"name" config:"name"`                                   // Display name
	ProtocolType string          `json:"protocol_type" config:"protocol_type"`                 // "openai" or "anthropic"
	BaseURL      string          `json:"base_url" config:"base_url"`                           // API server URL
	APIKey       string          `json:"api_key" config:"api_key"`                             // API key
	ModelName    string          `json:"model_name" config:"model_name"`                       // Model identifier
	ContextLimit int             `json:"context_limit" config:"context_limit,omitempty"`       // Maximum context length (0 means unlimited)
	MaxTokens    int             `json:"max_tokens" config:"max_tokens,omitempty"`             // Maximum output tokens (0 means use provider default)
	Reasoning0   json.RawMessage `json:"reasoning_0,omitempty" config:"reasoning_0,omitempty"` // Raw provider-level JSON merged into request body for reasoning level 0 (empty = no fields added)
	Reasoning1   json.RawMessage `json:"reasoning_1,omitempty" config:"reasoning_1,omitempty"` // Raw provider-level JSON merged into request body for reasoning level 1 (empty = no fields added)
	Reasoning2   json.RawMessage `json:"reasoning_2,omitempty" config:"reasoning_2,omitempty"` // Raw provider-level JSON merged into request body for reasoning level 2 (empty = no fields added)
}

// ReasoningConfigs returns a map of reasoning level → raw provider JSON
// built from the per-level Reasoning0/1/2 fields. Nil entries are omitted
// so callers can use a nil/empty check to decide whether to merge.
func (m *modelConfig) ReasoningConfigs() map[int]json.RawMessage {
	configs := map[int]json.RawMessage{}
	if len(m.Reasoning0) > 0 {
		configs[config.ReasoningLevelOff] = m.Reasoning0
	}
	if len(m.Reasoning1) > 0 {
		configs[config.ReasoningLevelNormal] = m.Reasoning1
	}
	if len(m.Reasoning2) > 0 {
		configs[config.ReasoningLevelMax] = m.Reasoning2
	}
	if len(configs) == 0 {
		return nil
	}
	return configs
}

// formatModelList formats a slice of modelConfig to key-value block format.
// Blocks are separated by "---". The output has no trailing blank line.
func formatModelList(models []modelConfig) string {
	if len(models) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(models))
	for _, m := range models {
		blocks = append(blocks, strings.TrimSuffix(config.FormatKeyValue(m), "\n"))
	}
	return strings.Join(blocks, "\n---\n") + "\n"
}

// toModelInfos converts domain models to the wire format used in system
// messages, so adapters can decode model_list without depending on the
// agent package.
func toModelInfos(models []modelConfig) []protocol.ModelInfo {
	infos := make([]protocol.ModelInfo, len(models))
	for i, m := range models {
		infos[i] = protocol.ModelInfo{
			ID:           m.ID,
			Name:         m.Name,
			ProtocolType: m.ProtocolType,
			BaseURL:      m.BaseURL,
			APIKey:       m.APIKey,
			ModelName:    m.ModelName,
			ContextLimit: m.ContextLimit,
			MaxTokens:    m.MaxTokens,
			Reasoning0:   m.Reasoning0,
			Reasoning1:   m.Reasoning1,
			Reasoning2:   m.Reasoning2,
		}
	}
	return infos
}

// parseModelList parses key-value block format into a slice of modelConfig.
// file is the source file name used in error messages (e.g. "model.conf").
// Returns models with a non-empty Name or ModelName, and any parse errors.
// Does NOT validate model fields — callers should validate after this.
func parseModelList(content string, file string) ([]modelConfig, []string) {
	blocks := config.ParseKeyValueBlocks(content)
	models := make([]modelConfig, 0, len(blocks))
	var errs []string

	for blockIdx, block := range blocks {
		blockModels, blockErrs := parseModelBlock(block, blockIdx, file)
		models = append(models, blockModels...)
		errs = append(errs, blockErrs...)
	}

	return models, errs
}

// parseModelBlock parses a single model block. Returns the models the
// block yields (at most one; nil for blank or nameless blocks) and any
// parse errors with the block index baked into the messages.
func parseModelBlock(block string, blockIdx int, file string) ([]modelConfig, []string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return nil, nil
	}

	var m modelConfig
	parseErrs := config.ParseKeyValue(block, &m)
	errs := make([]string, 0, len(parseErrs))
	for _, e := range parseErrs {
		errs = append(errs, fmt.Sprintf("%s block %d: %s", file, blockIdx+1, e.String()))
	}

	if m.Name == "" && m.ModelName == "" {
		return nil, errs
	}
	return []modelConfig{m}, errs
}
