package tools

import (
	"context"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/skills"
)

// ActivateSkillInput represents the input for the activate_skill tool
type ActivateSkillInput struct {
	Name string `json:"name" description:"The name of the skill to activate"`
}

// NewActivateSkillTool creates a tool for activating skills
func NewActivateSkillTool(skillsManager *skills.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"activate_skill",
		"Activate a skill by name to load its full instructions. Use this instead of reading SKILL.md files.",
		func(_ context.Context, input ActivateSkillInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if input.Name == "" {
				return fantasy.NewTextErrorResponse("skill name is required"), nil
			}

			content, err := skillsManager.ActivateSkill(input.Name)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse(content), nil
		},
	)
}
