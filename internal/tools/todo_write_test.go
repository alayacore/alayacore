package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/wallacegibbon/alayacore/internal/todo"
)

// mockTodoWriter implements TodoWriter for testing
type mockTodoWriter struct {
	todos todo.TodoList
}

func (m *mockTodoWriter) SetTodos(todos todo.TodoList) {
	m.todos = todos
}

func (m *mockTodoWriter) GetTodos() todo.TodoList {
	return m.todos
}

func TestTodoWrite_EmptyInput(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	info := tool.Info()
	if info.Name != "todo_write" {
		t.Errorf("expected tool name 'todo_write', got '%s'", info.Name)
	}

	// Test empty todos input
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: `{"todos": ""}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response for empty todos")
	}
}

func TestTodoWrite_InvalidJSON(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: `{"todos": "not valid json"}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response for invalid JSON")
	}
}

func TestTodoWrite_InitialCreation(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"pending\"},{\"content\":\"Task 2\",\"active_form\":\"Doing task 2\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.IsError {
		t.Errorf("unexpected error response: %s", resp.Content)
	}

	if len(writer.todos) != 2 {
		t.Errorf("expected 2 todos, got %d", len(writer.todos))
	}
}

func TestTodoWrite_StatusUpdateAllowed(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
			{Content: "Task 2", ActiveForm: "Doing task 2", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Update only status
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"in_progress\"},{\"content\":\"Task 2\",\"active_form\":\"Doing task 2\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.IsError {
		t.Errorf("unexpected error response: %s", resp.Content)
	}

	if writer.todos[0].Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got '%s'", writer.todos[0].Status)
	}
}

func TestTodoWrite_NewItemRejected(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Try to add a new item
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"pending\"},{\"content\":\"New Task\",\"active_form\":\"Doing new task\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response when adding new item")
	}
	if resp.Content == "" {
		t.Error("error message should not be empty")
	}
}

func TestTodoWrite_ActiveFormChangeRejected(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Try to change active_form
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Changed active form\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response when changing active_form")
	}
}

func TestTodoWrite_MissingItemRejected(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
			{Content: "Task 2", ActiveForm: "Doing task 2", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Missing Task 2
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"completed\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response when missing existing item")
	}
}

func TestTodoWrite_AllCompletedClearsList(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "in_progress"},
			{Content: "Task 2", ActiveForm: "Doing task 2", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Mark all as completed
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"completed\"},{\"content\":\"Task 2\",\"active_form\":\"Doing task 2\",\"status\":\"completed\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.IsError {
		t.Errorf("unexpected error response: %s", resp.Content)
	}

	if len(writer.todos) != 0 {
		t.Errorf("expected 0 todos after all completed, got %d", len(writer.todos))
	}
	if resp.Content != "All tasks completed! Todo list cleared." {
		t.Errorf("unexpected response text: %s", resp.Content)
	}
}

func TestTodoWrite_MissingContent(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	input := `{"todos": "[{\"active_form\":\"Doing task\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response for missing content")
	}
}

func TestTodoWrite_MissingActiveForm(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	input := `{"todos": "[{\"content\":\"Task 1\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response for missing active_form")
	}
}

func TestTodoWrite_InvalidStatus(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task\",\"status\":\"invalid\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response for invalid status")
	}
}

func TestTodoWrite_DefaultStatus(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	// No status provided - should default to "pending"
	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.IsError {
		t.Errorf("unexpected error response: %s", resp.Content)
	}

	if writer.todos[0].Status != "pending" {
		t.Errorf("expected default status 'pending', got '%s'", writer.todos[0].Status)
	}
}

func TestTodoWrite_ContentMatchIsExact(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Try with slightly different content (extra space)
	input := `{"todos": "[{\"content\":\"Task 1 \",\"active_form\":\"Doing task 1\",\"status\":\"in_progress\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.IsError {
		t.Error("expected error response when content doesn't match exactly")
	}
}

// Test multiple status updates in sequence
func TestTodoWrite_MultipleStatusUpdates(t *testing.T) {
	writer := &mockTodoWriter{
		todos: todo.TodoList{
			{Content: "Task 1", ActiveForm: "Doing task 1", Status: "pending"},
			{Content: "Task 2", ActiveForm: "Doing task 2", Status: "pending"},
		},
	}
	tool := NewTodoWriteTool(writer)

	// Update first task to in_progress
	input1 := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"in_progress\"},{\"content\":\"Task 2\",\"active_form\":\"Doing task 2\",\"status\":\"pending\"}]"}`
	resp1, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp1.IsError {
		t.Errorf("first update failed: %s", resp1.Content)
	}

	// Update first to completed, second to in_progress
	input2 := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"completed\"},{\"content\":\"Task 2\",\"active_form\":\"Doing task 2\",\"status\":\"in_progress\"}]"}`
	resp2, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.IsError {
		t.Errorf("second update failed: %s", resp2.Content)
	}

	// Verify final state
	if writer.todos[0].Status != "completed" {
		t.Errorf("expected Task 1 completed, got '%s'", writer.todos[0].Status)
	}
	if writer.todos[1].Status != "in_progress" {
		t.Errorf("expected Task 2 in_progress, got '%s'", writer.todos[1].Status)
	}
}

// Test JSON unmarshaling of TodoList
func TestTodoList_JSONUnmarshal(t *testing.T) {
	jsonStr := `[{"content":"Task 1","active_form":"Doing task 1","status":"pending"},{"content":"Task 2","active_form":"Doing task 2","status":"in_progress"}]`

	var todos todo.TodoList
	if err := json.Unmarshal([]byte(jsonStr), &todos); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(todos) != 2 {
		t.Errorf("expected 2 todos, got %d", len(todos))
	}
	if todos[0].Content != "Task 1" {
		t.Errorf("expected 'Task 1', got '%s'", todos[0].Content)
	}
	if todos[1].Status != "in_progress" {
		t.Errorf("expected 'in_progress', got '%s'", todos[1].Status)
	}
}

// Test response format
func TestTodoWrite_ResponseFormat(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	input := `{"todos": "[{\"content\":\"Task 1\",\"active_form\":\"Doing task 1\",\"status\":\"pending\"}]"}`
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test", Name: "todo_write", Input: input})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Todo list updated with 1 items"
	if resp.Content != expected {
		t.Errorf("expected '%s', got '%s'", expected, resp.Content)
	}
}

// Test tool implements fantasy.AgentTool
func TestTodoWrite_ToolInterface(t *testing.T) {
	writer := &mockTodoWriter{}
	tool := NewTodoWriteTool(writer)

	// Verify the tool implements fantasy.AgentTool
	var _ fantasy.AgentTool = tool

	// Check tool name
	if tool.Info().Name != "todo_write" {
		t.Errorf("expected tool name 'todo_write', got '%s'", tool.Info().Name)
	}

	// Check description is not empty
	if tool.Info().Description == "" {
		t.Error("tool description should not be empty")
	}
}
