// Package skills discovers and loads AI skill definitions from SKILL.md files.
package skills

// Metadata is the frontmatter of a SKILL.md file.
//
// Two of these fields do anything: Name and Description, which the prompt
// advertises. License and Compatibility are recorded — nothing enforces a
// license or checks a compatibility claim — and are kept on the Skill so the
// parsed manifest is inspectable without re-reading the file.
//
// The spec's free-form `metadata` field is deliberately absent: the reader skips
// the whole entry rather than record part of it (see ParseSkillMarkdown). There
// is likewise no tool-permission field: a skill cannot grant itself tools, and
// `allowed-tools` was removed once it was clear nothing read it. Tools are the
// user's to grant — --builtin-tools and --tool-confirm.
type Metadata struct {
	Name          string
	Description   string
	License       string
	Compatibility string
}

// Skill is one loaded skill: what the prompt says about it, and where the agent
// reads the instructions from.
type Skill struct {
	Name        string
	Description string
	Location    string
	Metadata    Metadata
}
