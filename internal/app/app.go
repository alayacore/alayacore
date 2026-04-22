package app

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
)

// This package provides shared initialization for all adaptors.
// It builds the system prompt, initializes tools, and creates the app config.

const systemPromptIdentity = `Your name is AlayaCore. You are a helpful AI assistant with access to tools for reading/writing files, executing commands, and activating skills.`

const systemPromptRules = `Never assume - verify with tools.`

const systemPromptSearch = `Use search_content to locate code and patterns before using read_file for detailed inspection.`

const systemPromptSkills = `Check <available_skills> below; read the <location> file to load relevant skill instructions. Skill instructions may use relative paths - run them from the skill's directory (derived from <location>). If a skill file's content was truncated (indicated by a truncation marker), re-read it before using the skill.`

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
	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	readFileTool := tools.NewReadFileTool()
	writeFileTool := tools.NewWriteFileTool()
	executeCommandTool := tools.NewExecuteCommandTool()
	editFileTool := tools.NewEditFileTool()

	agentTools := []llm.Tool{readFileTool, editFileTool, writeFileTool, executeCommandTool}

	// Conditionally register search_content tool if rg binary is available
	rgAvailable := tools.RGAvailable()
	if rgAvailable {
		agentTools = append(agentTools, tools.NewSearchContentTool())
	}

	// Build the default system prompt
	var systemPrompt string
	if rgAvailable {
		systemPrompt = systemPromptIdentity + "\n\n" + systemPromptRules + "\n\n" + systemPromptSearch
	} else {
		systemPrompt = systemPromptIdentity + "\n\n" + systemPromptRules
	}

	// Only include SKILLS section when skills are actually available
	skillsFragment := skillsManager.GenerateSystemPromptFragment()
	if skillsFragment != "" {
		systemPrompt = systemPrompt + "\n\n" + systemPromptSkills + "\n\n" + skillsFragment
	}

	// Add current working directory to system prompt (at the end for better API cache reuse)
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd
	}

	return &Config{
		Cfg:               cfg,
		Provider:          nil, // Provider will be created when model is set
		SkillsMgr:         skillsManager,
		AgentTools:        agentTools,
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt, // User-provided extra system prompt (supplemental, not replacement)
		MaxSteps:          cfg.MaxSteps,
	}, nil
}
