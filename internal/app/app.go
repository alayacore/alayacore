package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/alayacore/alayacore/internal/config"
	debugpkg "github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
)

const DefaultSystemPrompt = `You are AlayaCore, an AI assistant with POSIX shell and some other tool access.

IDENTITY:
- Your name is AlayaCore
- You are a helpful AI assistant created to help users with various tasks
- You have access to tools for reading/writing files, executing shell commands, and activating skills

RULES:
- Never assume - verify with tools
- Check <available_skills> below; activate relevant ones using the activate_skill tool
- When running skill scripts, cd to the skill's directory first (e.g., cd /path/to/skill && ./scripts/script.sh)
- Do NOT use find to locate scripts - use the path from SKILL.md`

// Config holds the common app configuration
type Config struct {
	Cfg          *config.Settings
	Model        fantasy.LanguageModel
	SkillsMgr    *skills.Manager
	AgentTools   []fantasy.AgentTool
	SystemPrompt string
}

// Setup initializes the common app components
func Setup(cfg *config.Settings) (*Config, error) {
	// Compute effective system prompt
	systemPrompt := DefaultSystemPrompt
	if cfg.SystemPrompt != "" {
		systemPrompt = cfg.SystemPrompt
	}

	// Try to get provider config from CLI args (optional)
	providerConfig, err := cfg.GetProviderConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get provider config: %w", err)
	}

	// Create model if CLI args were provided
	var model fantasy.LanguageModel
	if providerConfig != nil {
		provider, err := CreateProvider(providerConfig.Provider, providerConfig.APIKey, providerConfig.BaseURL, cfg.DebugAPI, cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider: %w", err)
		}

		model, err = provider.LanguageModel(context.Background(), providerConfig.ModelName)
		if err != nil {
			return nil, fmt.Errorf("failed to create language model: %w", err)
		}
	}

	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	// Add current working directory to system prompt
	cwd, _ := os.Getwd()
	if cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd + "\nDo NOT prefix shell commands with 'cd' - they already run in this directory."
	}

	// Generate skills fragment for system prompt
	skillsFragment := skillsManager.GenerateSystemPromptFragment()
	if skillsFragment != "" {
		systemPrompt = systemPrompt + "\n\n" + skillsFragment
	}

	// Load AGENTS.md from current directory if it exists
	agentsContent, err := os.ReadFile("AGENTS.md")
	if err == nil {
		systemPrompt = systemPrompt + "\n\n" + string(agentsContent)
	}

	readFileTool := tools.NewReadFileTool()
	writeFileTool := tools.NewWriteFileTool()
	activateSkillTool := tools.NewActivateSkillTool(skillsManager)
	posixShellTool := tools.NewPosixShellTool()
	editFileTool := tools.NewEditFileTool()

	return &Config{
		Cfg:          cfg,
		Model:        model, // Can be nil if no CLI args provided
		SkillsMgr:    skillsManager,
		AgentTools:   []fantasy.AgentTool{readFileTool, editFileTool, writeFileTool, activateSkillTool, posixShellTool},
		SystemPrompt: systemPrompt,
	}, nil
}

// CreateAgent creates a new fantasy agent with the configured tools and system prompt
func (c *Config) CreateAgent() fantasy.Agent {
	return fantasy.NewAgent(c.Model, fantasy.WithTools(c.AgentTools...), fantasy.WithSystemPrompt(c.SystemPrompt))
}

// AgentFactory returns a function that creates new agents (for WebSocket)
func (c *Config) AgentFactory() func() fantasy.Agent {
	return func() fantasy.Agent {
		return fantasy.NewAgent(c.Model, fantasy.WithTools(c.AgentTools...), fantasy.WithSystemPrompt(c.SystemPrompt))
	}
}

// CreateProvider creates a provider based on type
func CreateProvider(provider, apiKey, baseURL string, debugAPI bool, proxyURL string) (interface {
	LanguageModel(context.Context, string) (fantasy.LanguageModel, error)
}, error) {
	switch provider {
	case "anthropic":
		return CreateAnthropicProvider(apiKey, baseURL, debugAPI, proxyURL)
	default:
		return CreateOpenAIProvider(apiKey, baseURL, debugAPI, proxyURL)
	}
}

// CreateAnthropicProvider creates an Anthropic provider
func CreateAnthropicProvider(apiKey, baseURL string, debugAPI bool, proxyURL string) (interface {
	LanguageModel(context.Context, string) (fantasy.LanguageModel, error)
}, error) {
	var opts []anthropic.Option
	opts = append(opts, anthropic.WithAPIKey(apiKey))
	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}
	if proxyURL != "" {
		var client *http.Client
		var err error
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
		}
		opts = append(opts, anthropic.WithHTTPClient(client))
	} else if debugAPI {
		opts = append(opts, anthropic.WithHTTPClient(debugpkg.NewHTTPClient()))
	}
	return anthropic.New(opts...)
}

// CreateOpenAIProvider creates an OpenAI-compatible provider
func CreateOpenAIProvider(apiKey, baseURL string, debugAPI bool, proxyURL string) (interface {
	LanguageModel(context.Context, string) (fantasy.LanguageModel, error)
}, error) {
	// Use openaicompat for non-OpenAI URLs (Ollama, LM Studio, DeepSeek, etc.)
	// This enables reasoning/thinking content support
	if !strings.Contains(baseURL, "api.openai.com") {
		var opts []openaicompat.Option
		opts = append(opts, openaicompat.WithAPIKey(apiKey), openaicompat.WithBaseURL(baseURL))
		if proxyURL != "" {
			var client *http.Client
			var err error
			if debugAPI {
				client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
			} else {
				client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
			}
			if err != nil {
				return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
			}
			opts = append(opts, openaicompat.WithHTTPClient(client))
		} else if debugAPI {
			opts = append(opts, openaicompat.WithHTTPClient(debugpkg.NewHTTPClient()))
		}
		return openaicompat.New(opts...)
	}

	// Use native OpenAI provider for api.openai.com
	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey), openai.WithBaseURL(baseURL))
	if proxyURL != "" {
		var client *http.Client
		var err error
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
		}
		opts = append(opts, openai.WithHTTPClient(client))
	} else if debugAPI {
		opts = append(opts, openai.WithHTTPClient(debugpkg.NewHTTPClient()))
	}
	return openai.New(opts...)
}
