// Package skills discovers and loads AI skill definitions from SKILL.md files.
package skills

// Metadata represents the frontmatter of a SKILL.md file.
//
// Only the fields the runtime acts on are read: Name and Description drive
// discovery and activation. License, Compatibility and Metadata are kept for
// round-tripping the manifest and are not enforced anywhere — a skill cannot be
// restricted by declaring tools it will not use, so no tool-permission field
// lives here.
type Metadata struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
}

// Skill represents a loaded skill
type Skill struct {
	Name        string
	Description string
	Location    string
	Metadata    Metadata
}
