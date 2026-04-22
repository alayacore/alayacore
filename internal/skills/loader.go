package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// warnWriter is where warnings are written. Can be set to io.Discard in tests.
var warnWriter io.Writer = os.Stderr

// Manager handles skill discovery and loading
type Manager struct {
	skills    []Skill
	skillDirs []string
}

// NewManager creates a new skill manager
func NewManager(skillPaths []string) (*Manager, error) {
	m := &Manager{
		skills:    []Skill{},
		skillDirs: skillPaths,
	}

	// If no skill paths provided, return empty manager
	if len(skillPaths) == 0 {
		return m, nil
	}

	// Discover and load skill metadata from all paths
	if err := m.discoverSkills(); err != nil {
		return nil, fmt.Errorf("failed to discover skills: %w", err)
	}

	return m, nil
}

// discoverSkills scans all skill directories for skills
func (m *Manager) discoverSkills() error {
	for _, skillDir := range m.skillDirs {
		entries, err := os.ReadDir(skillDir)
		if err != nil {
			// If directory doesn't exist, that's OK - no skills
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(skillDir, entry.Name())
			skillFile := filepath.Join(skillPath, "SKILL.md")

			if _, err := os.Stat(skillFile); os.IsNotExist(err) {
				continue
			}

			// Load only metadata at startup
			skill, err := m.loadSkillMetadata(skillFile, entry.Name())
			if err != nil {
				// Skip invalid skills but log warning
				fmt.Fprintf(warnWriter, "Warning: failed to load skill %s from %s: %v\n", entry.Name(), skillDir, err)
				continue
			}

			// Check for duplicate skill names
			for _, existing := range m.skills {
				if existing.Name == skill.Name {
					fmt.Fprintf(warnWriter, "Warning: duplicate skill name '%s' found in %s\n", skill.Name, skillDir)
				}
			}

			m.skills = append(m.skills, skill)
		}
	}

	return nil
}

// loadSkillMetadata loads only the frontmatter from a SKILL.md file
func (m *Manager) loadSkillMetadata(skillFile, dirName string) (Skill, error) {
	content, err := os.ReadFile(skillFile)
	if err != nil {
		return Skill{}, err
	}

	metadata, _, err := ParseSkillMarkdown(string(content))
	if err != nil {
		return Skill{}, err
	}

	// Validate name matches directory
	if metadata.Name != "" && metadata.Name != dirName {
		return Skill{}, fmt.Errorf("skill name '%s' does not match directory '%s'", metadata.Name, dirName)
	}

	// Use directory name if name not specified
	if metadata.Name == "" {
		metadata.Name = dirName
	}

	return Skill{
		Name:        metadata.Name,
		Description: metadata.Description,
		Location:    skillFile,
		Metadata:    metadata,
	}, nil
}

// GetMetadata returns all skill metadata for system prompt injection
func (m *Manager) GetMetadata() []Skill {
	return m.skills
}

// GetSkillPaths returns the file paths to all skill SKILL.md files.
// Used by session.compactHistory to exempt skill file reads from truncation.
func (m *Manager) GetSkillPaths() []string {
	paths := make([]string, len(m.skills))
	for i, skill := range m.skills {
		paths[i] = skill.Location
	}
	return paths
}

// GenerateSystemPromptFragment generates the XML fragment for system prompt
func (m *Manager) GenerateSystemPromptFragment() string {
	if len(m.skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n<available_skills>\n")

	for _, skill := range m.skills {
		sb.WriteString("  <skill>\n")
		fmt.Fprintf(&sb, "    <name>%s</name>\n", skill.Name)
		fmt.Fprintf(&sb, "    <description>%s</description>\n", skill.Description)
		fmt.Fprintf(&sb, "    <location>%s</location>\n", skill.Location)
		sb.WriteString("  </skill>\n")
	}

	sb.WriteString("</available_skills>\n")

	return sb.String()
}
