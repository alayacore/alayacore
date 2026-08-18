package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
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
			result := FuzzyMatch(strings.ToLower(tt.search), strings.ToLower(tt.target))
			if result != tt.expected {
				t.Errorf("FuzzyMatch(%q, %q) = %v, expected %v", tt.search, tt.target, result, tt.expected)
			}
		})
	}
}

func TestModelSelectorCtrlCClearsSearch(t *testing.T) {
	styles := DefaultStyles()
	ms := NewModelSelector(styles)

	// Set up some test models
	models := []protocol.ModelInfo{
		{Name: "OpenAI GPT-4", ProtocolType: "openai", ModelName: "gpt-4"},
		{Name: "Zhipu / GLM-5", ProtocolType: "anthropic", ModelName: "glm-5"},
	}
	ms, _ = ms.LoadModels(models, 0)
	ms = ms.Open()

	// Focus the search input first (simulates user pressing Tab to focus search)
	ms.FilterInputFocused = true
	ms.FilterInput = ms.FilterInput.Focus()
	ms.updateFilterInputStyles()

	// Type in search input
	ms.FilterInput = ms.FilterInput.WithValue("gpt4")
	ms = ms.updateFilteredModels()

	if ms.FilterInput.Value() != "gpt4" {
		t.Fatalf("Expected search input to be 'gpt4', got %q", ms.FilterInput.Value())
	}

	// Press Ctrl+C
	msg := KeyPressMsg(Key{Code: 'c', Mod: ModCtrl})
	ms, cmd := ms.Update(msg)

	// Check that search input is cleared
	if ms.FilterInput.Value() != "" {
		t.Errorf("Ctrl+C should clear search input, got %q", ms.FilterInput.Value())
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

func TestModelSelectorSetModelsUpdatesFilteredModels(t *testing.T) {
	styles := DefaultStyles()
	ms := NewModelSelector(styles)

	// Set up initial models
	models := []protocol.ModelInfo{
		{Name: "OpenAI GPT-4", ProtocolType: "openai", ModelName: "gpt-4"},
		{Name: "Zhipu / GLM-5", ProtocolType: "anthropic", ModelName: "glm-5"},
	}
	ms, _ = ms.LoadModels(models, 0)
	ms = ms.Open()

	// Verify filteredModels is set
	if len(ms.filteredModels) != 2 {
		t.Fatalf("Expected 2 filtered models, got %d", len(ms.filteredModels))
	}

	// Simulate user typing a search (so lastSearchValue is set)
	ms.FilterInput = ms.FilterInput.WithValue("gpt")
	ms = ms.updateFilteredModels()

	// Verify filtered models are now filtered
	if len(ms.filteredModels) != 1 {
		t.Fatalf("Expected 1 filtered model after search, got %d", len(ms.filteredModels))
	}
	if ms.filteredModels[0].Name != "OpenAI GPT-4" {
		t.Errorf("Expected 'OpenAI GPT-4', got %q", ms.filteredModels[0].Name)
	}

	// Now set new models (simulating reload after editing config file)
	// The search value is still "gpt", so without the fix, filteredModels wouldn't update
	newModels := []protocol.ModelInfo{
		{Name: "OpenAI GPT-4o", ProtocolType: "openai", ModelName: "gpt-4o"},
		{Name: "OpenAI GPT-4", ProtocolType: "openai", ModelName: "gpt-4"},
		{Name: "Claude 3.5", ProtocolType: "anthropic", ModelName: "claude-3.5"},
	}
	ms, _ = ms.LoadModels(newModels, 0)

	// After SetModels, filteredModels should be updated with the new models
	// The search "gpt" should now match both GPT-4o and GPT-4
	if len(ms.filteredModels) != 2 {
		t.Errorf("Expected 2 filtered models matching 'gpt' after SetModels, got %d", len(ms.filteredModels))
	}

	// Clear search and verify all 3 models are shown
	ms.FilterInput = ms.FilterInput.WithValue("")
	ms = ms.updateFilteredModels()
	if len(ms.filteredModels) != 3 {
		t.Errorf("Expected 3 filtered models after clearing search, got %d", len(ms.filteredModels))
	}
}

func TestModelSelectorLoadModelsPreservesSelection(t *testing.T) {
	styles := DefaultStyles()
	ms := NewModelSelector(styles)

	// Set up initial models via LoadModels
	models := []protocol.ModelInfo{
		{ID: 1, Name: "Model A", ProtocolType: "openai", ModelName: "model-a"},
		{ID: 2, Name: "Model B", ProtocolType: "anthropic", ModelName: "model-b"},
	}
	ms, _ = ms.LoadModels(models, 1) // Model A is active
	ms = ms.Open()

	// Simulate user pressing Tab to focus the list, then navigating to Model B (index 1)
	ms.FilteredListCore = ms.HandleTabKey() // now FilterInputFocused=false, list is focused
	ms = ms.handleListKeys(keyDown)

	// Verify we selected Model B
	if ms.SelectedIdx != 1 {
		t.Fatalf("Expected selectedIdx=1 after navigating down, got %d", ms.SelectedIdx)
	}

	// Reload models (simulating Ctrl+R — user-triggered reload)
	// The selection should be preserved when selector is open and models already existed
	newModels := []protocol.ModelInfo{
		{ID: 1, Name: "Model A", ProtocolType: "openai", ModelName: "model-a"},
		{ID: 2, Name: "Model B", ProtocolType: "anthropic", ModelName: "model-b"},
		{ID: 3, Name: "Model C", ProtocolType: "anthropic", ModelName: "model-c"},
	}
	ms, _ = ms.LoadModels(newModels, 1) // Model A is still active

	// Selection should still be at index 1 (Model B)
	if ms.SelectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be preserved at 1, got %d", ms.SelectedIdx)
	}
}
