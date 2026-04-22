package skills

// Metadata represents the frontmatter of a SKILL.md file
type Metadata struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`
}

// Skill represents a loaded skill
type Skill struct {
	Name        string
	Description string
	Location    string
	Metadata    Metadata
}
