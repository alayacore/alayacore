// Package factory creates LLM providers from configuration.
package factory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// ProviderConfig configures a provider
type ProviderConfig struct {
	Type       string // "anthropic", "openai"
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	MaxTokens  int // Maximum output tokens (0 = provider default)

	// ReasoningConfigs is the user-supplied per-level provider wire-format
	// JSON merged into the request body. Keyed by reasoning level (0=off,
	// 1=normal, 2=max); nil/empty entries mean "no fields added for this
	// level".
	ReasoningConfigs map[int]json.RawMessage

	// ReasoningField is the response-side delta key carrying reasoning text
	// (model.conf `reasoning_field`). Empty means the provider default,
	// providers.DefaultReasoningField.
	ReasoningField string

	// SerialToolCalls runs a step's tool calls one at a time and asks the Chat
	// Completions endpoint for the same. It arrives verbatim from model.conf
	// `serial_tool_calls`, whose negative spelling is what lets its absent form
	// be the behavior alayacore has always had.
	SerialToolCalls bool
}

// NewProvider creates a provider based on configuration
func NewProvider(config ProviderConfig) (llm.Provider, error) {
	cfg := providers.BaseConfig{
		APIKey:           config.APIKey,
		BaseURL:          config.BaseURL,
		Model:            config.Model,
		HTTPClient:       config.HTTPClient,
		MaxTokens:        config.MaxTokens,
		ReasoningConfigs: config.ReasoningConfigs,
		ReasoningField:   config.ReasoningField,
		SerialToolCalls:  config.SerialToolCalls,
	}

	switch strings.ToLower(config.Type) {
	case "anthropic":
		return providers.NewAnthropic(cfg)
	case "openai":
		return providers.NewOpenAI(cfg)
	default:
		return nil, fmt.Errorf("provider: unknown provider type: %s", config.Type)
	}
}
