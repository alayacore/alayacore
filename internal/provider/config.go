package provider

import (
	"fmt"
)

// Config holds the provider configuration
type Config struct {
	APIKey    string
	BaseURL   string
	ModelName string
	Provider  string // "anthropic" or "openai"
}

// GetProviderConfig returns the provider configuration based on CLI flags
// All parameters are optional - if not provided, models must be loaded from config file
func GetProviderConfig(apiKey, baseURL, modelName, providerType string) (*Config, error) {
	// If no CLI args provided, return nil (models will be loaded from config file)
	if providerType == "" && baseURL == "" && apiKey == "" {
		return nil, nil
	}

	// If any arg is provided, validate all required args are present
	if providerType == "" {
		return nil, fmt.Errorf("--type is required when using CLI args (anthropic or openai)")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("--base-url is required when using CLI args")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("--api-key is required when using CLI args")
	}

	if providerType != "anthropic" && providerType != "openai" {
		return nil, fmt.Errorf("unknown provider type: %s (supported: anthropic, openai)", providerType)
	}

	return &Config{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ModelName: modelName,
		Provider:  providerType,
	}, nil
}
