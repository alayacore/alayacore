package app

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
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
- Skill instructions may use relative paths - run them from the skill's directory (derived from <location>)`

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

	// Add current working directory to system prompt (at the end for better API cache reuse)
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd
	}

	readFileTool := tools.NewReadFileTool()
	writeFileTool := tools.NewWriteFileTool()
	activateSkillTool := tools.NewActivateSkillTool(skillsManager)
	shellTool := tools.NewShellTool()
	editFileTool := tools.NewEditFileTool()

	return &Config{
		Cfg:               cfg,
		Provider:          nil, // Provider will be created when model is set
		SkillsMgr:         skillsManager,
		AgentTools:        []llm.Tool{readFileTool, editFileTool, writeFileTool, activateSkillTool, shellTool},
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt, // User-provided extra system prompt (supplemental, not replacement)
		MaxSteps:          cfg.MaxSteps,
	}, nil
}
