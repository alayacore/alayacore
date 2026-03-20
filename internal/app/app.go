package app

import (
	"fmt"
	"net/http"
	"os"

	"github.com/alayacore/alayacore/internal/config"
	debugpkg "github.com/alayacore/alayacore/internal/debug"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
)

// This package provides shared initialization for both terminal and web adaptors.
// It builds the system prompt, initializes tools, and creates the app config.

const DefaultSystemPrompt = `IDENTITY:
- Your name is AlayaCore
- You are a helpful AI assistant with access to tools for reading/writing files, executing shell commands, and activating skills

RULES:
- Never assume - verify with tools

SKILLS:
- Check <available_skills> below; activate relevant ones using the activate_skill tool
- Skill instructions may use relative paths - run them from the skill's directory (derived from <location>)

FILE EDITING:
- Always read a file before editing it to get exact text including whitespace
- Use edit_file for surgical changes; use write_file only for new files or complete rewrites
- Include 3-5 lines of context in old_string to make matches unique
- Match whitespace exactly - tabs, spaces, and newlines must be identical`

// Config holds the common app configuration
type Config struct {
	Cfg               *config.Settings
	Provider          llm.Provider
	SkillsMgr         *skills.Manager
	AgentTools        []llm.Tool
	SystemPrompt      string // Default system prompt (always present)
	ExtraSystemPrompt string // User-provided extra system prompt via --system flag
	MaxSteps          int    // Maximum agent loop steps
}

// Setup initializes the common app components
func Setup(cfg *config.Settings) (*Config, error) {
	// Build the default system prompt
	systemPrompt := DefaultSystemPrompt

	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
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

	// Add current working directory to system prompt (at the end for better API cache reuse)
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd
	}

	readFileTool := tools.NewReadFileTool()
	writeFileTool := tools.NewWriteFileTool()
	activateSkillTool := tools.NewActivateSkillTool(skillsManager)
	posixShellTool := tools.NewPosixShellTool()
	editFileTool := tools.NewEditFileTool()

	return &Config{
		Cfg:               cfg,
		Provider:          nil, // Provider will be created when model is set
		SkillsMgr:         skillsManager,
		AgentTools:        []llm.Tool{readFileTool, editFileTool, writeFileTool, activateSkillTool, posixShellTool},
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt, // User-provided extra system prompt (supplemental, not replacement)
		MaxSteps:          cfg.MaxSteps,
	}, nil
}

// CreateProvider creates a provider based on type
func CreateProvider(providerType, apiKey, baseURL, model string, debugAPI bool, proxyURL string) (llm.Provider, error) {
	// Create HTTP client with optional proxy and debug
	var client *http.Client
	var err error
	if proxyURL != "" {
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
		}
	} else if debugAPI {
		client = debugpkg.NewHTTPClient()
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:       providerType,
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: client,
	})
}
