package skills

import (
	"io"
	"os"
	"path/filepath"
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

	metadata, body, err := ParseSkillMarkdown(content)
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

	metadata, body, err := ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("ParseSkillMarkdown failed: %v", err)
	}

	if metadata.Name != "" {
		t.Errorf("Expected empty name, got '%s'", metadata.Name)
	}

	if body == "" {
		t.Error("Expected body content, got empty")
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
	m, err := NewManager([]string{tmpDir})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

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

func TestSkillActivation(t *testing.T) {
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

	m, err := NewManager([]string{tmpDir})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Test activation
	content, err := m.ActivateSkill("test-skill")
	if err != nil {
		t.Fatalf("ActivateSkill failed: %v", err)
	}

	if !contains(content, "Test Skill Body") {
		t.Error("Expected activated content to contain skill body")
	}

	// Test non-existent skill
	_, err = m.ActivateSkill("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent skill")
	}
}

func TestEmptySkillsDir(t *testing.T) {
	m, err := NewManager([]string{})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

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
	m, err := NewManager([]string{tmpDir1, tmpDir2})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

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

	// Test activation of skills from different paths
	content1, err := m.ActivateSkill("skill-one")
	if err != nil {
		t.Fatalf("ActivateSkill failed for skill-one: %v", err)
	}
	if !contains(content1, "Skill One") {
		t.Error("Expected activated content to contain Skill One")
	}

	content2, err := m.ActivateSkill("skill-two")
	if err != nil {
		t.Fatalf("ActivateSkill failed for skill-two: %v", err)
	}
	if !contains(content2, "Skill Two") {
		t.Error("Expected activated content to contain Skill Two")
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

func TestDuplicateSkillNames(t *testing.T) {
	// Suppress warnings during this test
	original := warnWriter
	warnWriter = io.Discard
	defer func() { warnWriter = original }()

	// Create first temp skill directory
	tmpDir1 := t.TempDir()

	skillDir1 := filepath.Join(tmpDir1, "duplicate-skill")
	if err := os.Mkdir(skillDir1, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile1 := filepath.Join(skillDir1, "SKILL.md")
	skillContent1 := `---
name: duplicate-skill
description: First occurrence
---

# First Duplicate`
	if err := os.WriteFile(skillFile1, []byte(skillContent1), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// Create second temp skill directory with same skill name
	tmpDir2 := t.TempDir()

	skillDir2 := filepath.Join(tmpDir2, "duplicate-skill")
	if err := os.Mkdir(skillDir2, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile2 := filepath.Join(skillDir2, "SKILL.md")
	skillContent2 := `---
name: duplicate-skill
description: Second occurrence
---

# Second Duplicate`
	if err := os.WriteFile(skillFile2, []byte(skillContent2), 0644); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	// Test manager - both skills should be loaded (with warning)
	m, err := NewManager([]string{tmpDir1, tmpDir2})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	metadata := m.GetMetadata()
	// Both skills should be in the list
	if len(metadata) != 2 {
		t.Errorf("Expected 2 skills (with duplicate names), got %d", len(metadata))
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
