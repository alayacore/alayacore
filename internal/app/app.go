package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/mcp/auth"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

// This package provides shared initialization for all adapters.
// It builds the system prompt, initializes tools, and creates the app config.

const systemPromptBase = `You are a helpful AI assistant with access to a set of tools that you can use to accomplish tasks.

Never assume - verify with tools.

Use search tools to locate code and patterns before using file read tools for detailed inspection.`

const systemPromptSkills = `Check <available_skills> below; read the <location> file to load relevant skill instructions. Skill instructions may use relative paths - run them from the skill's directory (derived from <location>).`

// Config holds the common app configuration
type Config struct {
	Cfg               *config.Settings
	SkillsMgr         *skills.Manager
	AgentTools        []llm.Tool
	SystemPrompt      string   // Default system prompt (always present)
	ExtraSystemPrompt string   // User-provided extra system prompt via --system flag
	MaxSteps          int      // Maximum agent loop steps
	ToolConfirmTools  []string // Tool names requiring user confirmation

	// MCPInit provides asynchronous MCP initialization.
	// If non-nil, MCP servers are configured and initialization is
	// running in the background. The session manages init results
	// internally — the adapter receives progress via system messages.
	MCPInit *mcp.Initializer

	// StartupErrors contains errors from skills loading and MCP config
	// parsing. These are emitted as TLV system messages during session
	// startup so the user sees them even in TUI mode.
	StartupErrors []string
}

// Setup initializes the common app components.
//
// Fast path: skills, built-in tools, MCP config parsing (no connections).
// MCP initialization (connect, discover) runs asynchronously via cfg.AsyncMCP.
// The session manages init results internally — the adapter only needs
// AsyncMCP for TUI lifecycle checks (e.g. init overlay).
func Setup(cfg *config.Settings) (*Config, error) {
	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	// Apply command timeout before tools are created, so that
	// NewExecuteCommandTool() picks up the correct value for its
	// LLM-facing description.
	shell.DefaultCommandTimeout = time.Duration(cfg.CommandTimeout) * time.Second

	agentTools, err := tools.DefaultTools(cfg.BuiltinTools)
	if err != nil {
		return nil, fmt.Errorf("invalid --builtin-tools: %w", err)
	}

	// Collect startup errors from all sources.
	var startupErrors []string
	startupErrors = append(startupErrors, skillsManager.GetLoadErrors()...)

	// ========================================================================
	// MCP (Model Context Protocol) — async initialization
	// ========================================================================
	mcpInit, mcpErrors := initMCPAsync(cfg)
	startupErrors = append(startupErrors, mcpErrors...)

	// ========================================================================
	// System Prompt Construction (base — without MCP sections)
	// ========================================================================

	// Build the default system prompt
	systemPrompt := systemPromptBase

	// Only include SKILLS section when skills are actually available
	skillsFragment := skillsManager.GenerateSystemPromptFragment()
	if skillsFragment != "" {
		systemPrompt = systemPrompt + "\n\n" + systemPromptSkills + "\n\n" + skillsFragment
	}

	// Append CWD at the end so the LLM constructs correct absolute paths
	// from the first tool call. Placed last for API cache reuse — stable
	// portion stays cached, only the suffix changes per project.
	// See docs/architecture.md for rationale.
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd
	}

	// Note: MCP sections (instructions, resources, prompts) are NOT added here.
	// They'll be appended dynamically when async MCP init completes —
	// the session applies them internally via applyMCPUpdate.

	return &Config{
		Cfg:               cfg,
		SkillsMgr:         skillsManager,
		AgentTools:        agentTools, // no MCP tools yet
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt,
		MaxSteps:          cfg.MaxSteps,
		ToolConfirmTools:  cfg.ToolConfirm,
		MCPInit:           mcpInit,
		StartupErrors:     startupErrors,
	}, nil
}

// initMCPAsync starts asynchronous MCP initialization.
// Returns an Init (nil if no MCP servers configured) and any config
// parsing errors. The Init is NOT started yet — the session starts
// it when the main event loop begins.
func initMCPAsync(cfg *config.Settings) (*mcp.Initializer, []string) {
	// Load MCP configurations from mcp.conf
	mcpConfigs, startupErrors := mcp.LoadConfigs(cfg)
	if len(mcpConfigs) == 0 {
		return nil, startupErrors
	}

	// Set debug mode from global config.
	for i := range mcpConfigs {
		mcpConfigs[i].DebugDir = cfg.DebugLogDir
	}

	// Set up token persistence for all MCP servers.
	if tokenStore := createTokenStore(cfg); tokenStore != nil {
		for i := range mcpConfigs {
			mcpConfigs[i].TokenStore = tokenStore
		}
	}

	mcpInit := mcp.NewInitializer(mcpConfigs)
	return mcpInit, startupErrors
}

// createTokenStore creates a FileTokenStore for persisting MCP OAuth tokens.
// Returns nil if the config directory cannot be determined.
func createTokenStore(cfg *config.Settings) *auth.FileTokenStore {
	// Derive token directory from the config path directory (parent of mcp.conf).
	tokenDir := filepath.Join(filepath.Dir(cfg.MCPConfigPath), "mcp-cache")
	return auth.NewFileTokenStore(tokenDir)
}
