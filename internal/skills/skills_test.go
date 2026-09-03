package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillMarkdown(t *testing.T) {
	// Test with frontmatter
	content := `---
name: pdf-processing
description: Extract text and tables from PDF files
license: Apache-2.0
---

# PDF Processing

This is the body content.`

	metadata, body, _, err := ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("ParseSkillMarkdown failed: %v", err)
	}

	if metadata.Name != "pdf-processing" {
		t.Errorf("Expected name 'pdf-processing', got '%s'", metadata.Name)
	}

	if metadata.Description != "Extract text and tables from PDF files" {
		t.Errorf("Expected description, got '%s'", metadata.Description)
	}

	if metadata.License != "Apache-2.0" {
		t.Errorf("Expected license 'Apache-2.0', got '%s'", metadata.License)
	}

	if body == "" {
		t.Error("Expected body content, got empty")
	}
}

func TestParseSkillMarkdownNoFrontmatter(t *testing.T) {
	content := `# Just a title

Some content here.`

	metadata, body, problems, err := ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("ParseSkillMarkdown failed: %v", err)
	}

	if metadata.Name != "" {
		t.Errorf("Expected empty name, got '%s'", metadata.Name)
	}

	// The reason is reported even though it is not an error: a file with no
	// manifest cannot be advertised, and silence is how this went unnoticed.
	if len(problems) == 0 {
		t.Error("Expected a problem naming the missing frontmatter, got none")
	}

	if body == "" {
		t.Error("Expected body content, got empty")
	}
}

func TestParseSkillMarkdownMissingName(t *testing.T) {
	content := `---
description: A skill without a name
---

# No Name Skill`

	_, _, problems, err := ParseSkillMarkdown(content)
	if err == nil {
		t.Fatal("Expected error for missing name, got nil")
	}
	// A required field is a reason to refuse the skill, not a line to note.
	if len(problems) != 0 {
		t.Errorf("Expected no problems beside the error, got %v", problems)
	}
	if !contains(err.Error(), "name is required") {
		t.Errorf("Expected 'name is required' error, got: %v", err)
	}
}

func TestParseSkillMarkdownMissingDescription(t *testing.T) {
	content := `---
name: test-skill
---

# No Description Skill`

	_, _, problems, err := ParseSkillMarkdown(content)
	if err == nil {
		t.Fatal("Expected error for missing description, got nil")
	}
	if len(problems) != 0 {
		t.Errorf("Expected no problems beside the error, got %v", problems)
	}
	if !contains(err.Error(), "description is required") {
		t.Errorf("Expected 'description is required' error, got: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"pdf-processing", false},
		{"my-skill", false},
		{"skill123", false},
		{"123-skill", false},
		{"-invalid", true},
		{"invalid-", true},
		{"invalid--name", true},
		{"Invalid", true},
		{"skill_name", true},
	}

	for _, tt := range tests {
		err := validateName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestSkillDiscovery(t *testing.T) {
	// Create temp skill directory
	tmpDir := t.TempDir()

	// Create a skill directory
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.Mkdir(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	// Create SKILL.md
	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: test-skill
description: A test skill
---

# Test Skill

This is a test skill.`

	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// Test manager
	m := NewManager([]string{tmpDir})

	metadata := m.GetMetadata()
	if len(metadata) != 1 {
		t.Errorf("Expected 1 skill, got %d", len(metadata))
	}

	if metadata[0].Name != "test-skill" {
		t.Errorf("Expected skill name 'test-skill', got '%s'", metadata[0].Name)
	}

	// Test system prompt generation
	fragment := m.GenerateSystemPromptFragment()
	if fragment == "" {
		t.Error("Expected non-empty fragment")
	}

	// Verify content has expected tags
	if !contains(fragment, "<name>test-skill</name>") {
		t.Error("Expected fragment to contain skill name")
	}
}

func TestSkillLocation(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.Mkdir(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: test-skill
description: A test skill
---

# Test Skill Body`

	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	m := NewManager([]string{tmpDir})

	metadata := m.GetMetadata()
	if len(metadata) != 1 {
		t.Fatalf("Expected 1 skill, got %d", len(metadata))
	}

	// Verify the location points to the SKILL.md file
	if metadata[0].Location != skillFile {
		t.Errorf("Expected location %s, got %s", skillFile, metadata[0].Location)
	}
}

func TestEmptySkillsDir(t *testing.T) {
	m := NewManager([]string{})

	metadata := m.GetMetadata()
	if len(metadata) != 0 {
		t.Errorf("Expected 0 skills, got %d", len(metadata))
	}

	fragment := m.GenerateSystemPromptFragment()
	if fragment != "" {
		t.Error("Expected empty fragment for no skills")
	}
}

func TestMultipleSkillPaths(t *testing.T) {
	// Create first temp skill directory
	tmpDir1 := t.TempDir()

	// Create a skill in first directory
	skillDir1 := filepath.Join(tmpDir1, "skill-one")
	if err := os.Mkdir(skillDir1, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile1 := filepath.Join(skillDir1, "SKILL.md")
	skillContent1 := `---
name: skill-one
description: First skill from directory 1
---

# Skill One`
	if err := os.WriteFile(skillFile1, []byte(skillContent1), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// Create second temp skill directory
	tmpDir2 := t.TempDir()

	// Create a skill in second directory
	skillDir2 := filepath.Join(tmpDir2, "skill-two")
	if err := os.Mkdir(skillDir2, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile2 := filepath.Join(skillDir2, "SKILL.md")
	skillContent2 := `---
name: skill-two
description: Second skill from directory 2
---

# Skill Two`
	if err := os.WriteFile(skillFile2, []byte(skillContent2), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// Test manager with multiple paths
	m := NewManager([]string{tmpDir1, tmpDir2})

	metadata := m.GetMetadata()
	if len(metadata) != 2 {
		t.Errorf("Expected 2 skills from multiple directories, got %d", len(metadata))
	}

	// Verify both skills are loaded
	skillNames := make(map[string]bool)
	for _, skill := range metadata {
		skillNames[skill.Name] = true
	}

	if !skillNames["skill-one"] {
		t.Error("Expected to find skill-one")
	}
	if !skillNames["skill-two"] {
		t.Error("Expected to find skill-two")
	}

	// Verify system prompt contains both skills
	fragment := m.GenerateSystemPromptFragment()
	if !contains(fragment, "<name>skill-one</name>") {
		t.Error("Expected fragment to contain skill-one name")
	}
	if !contains(fragment, "<name>skill-two</name>") {
		t.Error("Expected fragment to contain skill-two name")
	}
}

func TestMultipleSkillPathsFirstMissing(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "nonexistent")

	// Create a real skill directory
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skill-two")
	if err := os.Mkdir(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: skill-two
description: Skill from second directory
---

# Skill Two`
	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// First path doesn't exist — should still discover skills from second path
	m := NewManager([]string{missingDir, tmpDir})

	metadata := m.GetMetadata()
	if len(metadata) != 1 {
		t.Fatalf("Expected 1 skill (from second dir), got %d", len(metadata))
	}
	if metadata[0].Name != "skill-two" {
		t.Errorf("Expected skill name 'skill-two', got '%s'", metadata[0].Name)
	}
}

// The first container to claim a name keeps it, and the loser is named. Both
// used to be published: the prompt advertised the same name twice with two
// different locations, and the only thing the model could do with that was guess.
func TestDuplicateSkillNames(t *testing.T) {
	tmpDir1 := t.TempDir()
	skillDir1 := filepath.Join(tmpDir1, "duplicate-skill")
	if err := os.Mkdir(skillDir1, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	skillFile1 := filepath.Join(skillDir1, "SKILL.md")
	if err := os.WriteFile(skillFile1, []byte("---\nname: duplicate-skill\ndescription: First occurrence\n---\n\n# First Duplicate"), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	tmpDir2 := t.TempDir()
	skillDir2 := filepath.Join(tmpDir2, "duplicate-skill")
	if err := os.Mkdir(skillDir2, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	skillFile2 := filepath.Join(skillDir2, "SKILL.md")
	if err := os.WriteFile(skillFile2, []byte("---\nname: duplicate-skill\ndescription: Second occurrence\n---\n\n# Second Duplicate"), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	m := NewManager([]string{tmpDir1, tmpDir2})

	metadata := m.GetMetadata()
	if len(metadata) != 1 {
		t.Fatalf("Expected 1 skill (the first container wins), got %d", len(metadata))
	}
	if metadata[0].Description != "First occurrence" {
		t.Errorf("the surviving skill is %q, want the one from the container listed first", metadata[0].Description)
	}
	if metadata[0].Location != skillFile1 {
		t.Errorf("location = %s, want %s", metadata[0].Location, skillFile1)
	}

	// The ignored skill must be reported with both paths, so the user can see
	// which container lost and why.
	found := false
	for _, e := range m.GetLoadErrors() {
		if strings.Contains(e, "duplicate-skill") && strings.Contains(e, "ignored") &&
			strings.Contains(e, skillFile2) && strings.Contains(e, skillFile1) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected the ignored duplicate to name both manifests, got: %v", m.GetLoadErrors())
	}
	if n := strings.Count(m.GenerateSystemPromptFragment(), "<name>duplicate-skill</name>"); n != 1 {
		t.Errorf("the prompt advertises the name %d times, want exactly once", n)
	}
}

// Passing the same container twice is the same collision, not a second skill.
func TestSameContainerGivenTwiceLoadsOnce(t *testing.T) {
	container := t.TempDir()
	dir := filepath.Join(container, "solo")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: solo\ndescription: d\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{container, container})
	if got := loadedNames(m); len(got) != 1 {
		t.Errorf("loaded %v, want one skill from a container named twice", got)
	}
	if len(m.GetLoadErrors()) != 1 {
		t.Errorf("load errors = %v, want the ignored second copy named", m.GetLoadErrors())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
