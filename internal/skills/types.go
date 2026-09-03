// Package skills discovers and loads AI skill definitions from SKILL.md files.
package skills

// Metadata is the frontmatter of a SKILL.md file.
//
// Two of these fields do anything: Name and Description, which the prompt
// advertises. License, Compatibility and Metadata are recorded — nothing
// enforces a license, checks a compatibility claim, or reads a metadata entry —
// and are kept on the Skill so the parsed manifest is inspectable without
// re-reading the file. A field the build does not know at all is skipped rather
// than refused, so a newer manifest still loads.
//
// There is deliberately no tool-permission field here: a skill cannot grant
// itself tools, and `allowed-tools` was removed once it was clear nothing read
// it. Tools are the user's to grant — --builtin-tools and --tool-confirm.
type Metadata struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
}

// Skill is one loaded skill: what the prompt says about it, and where the agent
// reads the instructions from.
type Skill struct {
	Name        string
	Description string
	Location    string
	Metadata    Metadata
}
