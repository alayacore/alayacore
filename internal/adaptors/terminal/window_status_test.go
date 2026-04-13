package terminal

import (
	"testing"
)

func TestUpdateToolStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendToolCall("tool123", "shell", "shell: git status")

	// Verify window was created
	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected 1 window, got %d", wb.GetWindowCount())
	}

	// Initially no status
	if wb.GetWindow(0).Status != ToolStatusNone {
		t.Errorf("Expected ToolStatusNone, got %v", wb.GetWindow(0).Status)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", ToolStatusPending)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusPending {
		t.Errorf("Expected ToolStatusPending, got %v", wb.GetWindow(0).Status)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", ToolStatusSuccess)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.GetWindow(0).Status)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", ToolStatusError)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.GetWindow(0).Status)
	}

	// Try to update non-existent window (should not crash)
	wb.UpdateToolStatus("nonexistent", ToolStatusSuccess)
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendToolCall("tool123", "shell", "shell: git status")

	// Test rendering without status (default for loaded sessions)
	w := wb.GetWindow(0)
	content := wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed hollow dot (·) as default for tool windows without status
	if !contains(content, "·") {
		t.Errorf("Expected content to contain hollow dot (·), got: %s", content)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", ToolStatusPending)

	// Test rendering with pending status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", ToolStatusSuccess)

	// Test rendering with success status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", ToolStatusError)

	// Test rendering with error status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
