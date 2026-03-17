package tools

import (
	"context"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
)

// ActivateSkillInput represents the input for the activate_skill tool
type ActivateSkillInput struct {
	Name string `json:"name" jsonschema:"required,description=The name of the skill to activate"`
}

// NewActivateSkillTool creates a tool for activating skills
func NewActivateSkillTool(skillsManager *skills.Manager) llm.Tool {
	return llm.NewTool(
		"activate_skill",
		"Activate a skill by name to load its full instructions. Use this instead of reading SKILL.md files.",
	).
		WithSchema(llm.GenerateSchema(ActivateSkillInput{})).
		WithExecute(llm.TypedExecute(func(_ context.Context, args ActivateSkillInput) (llm.ToolResultOutput, error) {
			content, err := skillsManager.ActivateSkill(args.Name)
			if err != nil {
				return llm.NewTextErrorResponse(err.Error()), nil
			}
			return llm.NewTextResponse(content), nil
		})).
		Build()
}
