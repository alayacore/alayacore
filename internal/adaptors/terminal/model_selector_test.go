package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name     string
		search   string
		target   string
		expected bool
	}{
		{
			name:     "exact match",
			search:   "gpt4",
			target:   "gpt4",
			expected: true,
		},
		{
			name:     "fuzzy match with gaps",
			search:   "zhipuglm5",
			target:   "zhipu / glm-5",
			expected: true,
		},
		{
			name:     "fuzzy match with case difference",
			search:   "zhipuglm5",
			target:   "Zhipu / GLM-5",
			expected: true,
		},
		{
			name:     "fuzzy match partial",
			search:   "glm5",
			target:   "zhipu / glm-5",
			expected: true,
		},
		{
			name:     "fuzzy match partial start",
			search:   "zhipu",
			target:   "zhipu / glm-5",
			expected: true,
		},
		{
			name:     "no match - wrong order",
			search:   "glmzhipu",
			target:   "zhipu / glm-5",
			expected: false,
		},
		{
			name:     "no match - missing char",
			search:   "gpt5",
			target:   "gpt4",
			expected: false,
		},
		{
			name:     "empty search matches everything",
			search:   "",
			target:   "anything",
			expected: true,
		},
		{
			name:     "search longer than target",
			search:   "verylongsearch",
			target:   "short",
			expected: false,
		},
		{
			name:     "fuzzy match with spaces and special chars",
			search:   "opengpt4",
			target:   "openai / gpt-4",
			expected: true,
		},
		{
			name:     "case insensitive partial match",
			search:   "openai",
			target:   "OpenAI GPT-4",
			expected: true,
		},
		{
			name:     "partial with numbers",
			search:   "gpt4o",
			target:   "gpt-4o-2024",
			expected: true,
		},
		{
			name:     "no match - completely different",
			search:   "abc",
			target:   "xyz",
			expected: false,
		},
		{
			name:     "match with repeated chars in search",
			search:   "gptt4",
			target:   "gpt-4",
			expected: false, // extra 't' should not match
		},
		{
			name:     "match with repeated chars in target",
			search:   "gpt4",
			target:   "gppt-4",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert to lowercase for both (as the function expects)
			result := fuzzyMatch(strings.ToLower(tt.search), strings.ToLower(tt.target))
			if result != tt.expected {
				t.Errorf("fuzzyMatch(%q, %q) = %v, expected %v", tt.search, tt.target, result, tt.expected)
			}
		})
	}
}

func TestModelSelectorCtrlCClearsSearch(t *testing.T) {
	styles := DefaultStyles()
	ms := NewModelSelector(styles)
	
	// Set up some test models
	models := []ModelConfig{
		{Name: "OpenAI GPT-4", ProtocolType: "openai", ModelName: "gpt-4"},
		{Name: "Zhipu / GLM-5", ProtocolType: "anthropic", ModelName: "glm-5"},
	}
	ms.SetModels(models)
	ms.Open()
	
	// Type in search input
	ms.searchInput.SetValue("gpt4")
	ms.updateFilteredModels()
	
	if ms.searchInput.Value() != "gpt4" {
		t.Fatalf("Expected search input to be 'gpt4', got %q", ms.searchInput.Value())
	}
	
	// Press Ctrl+C
	msg := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	cmd := ms.HandleKeyMsg(msg)
	
	// Check that search input is cleared
	if ms.searchInput.Value() != "" {
		t.Errorf("Ctrl+C should clear search input, got %q", ms.searchInput.Value())
	}
	
	// Check that all models are shown again after clearing
	if len(ms.filteredModels) != len(models) {
		t.Errorf("Expected %d filtered models after clear, got %d", len(models), len(ms.filteredModels))
	}
	
	// Cmd should be nil (no action)
	if cmd != nil {
		t.Errorf("Ctrl+C should not return a command, got %v", cmd)
	}
}
