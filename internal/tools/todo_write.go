package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/fantasy"
	"github.com/wallacegibbon/alayacore/internal/todo"
)

// TodoWriter is an interface for writing todos
type TodoWriter interface {
	SetTodos(todos todo.TodoList)
	GetTodos() todo.TodoList
	GetLastWrittenTodos() todo.TodoList
	SetLastWrittenTodos(todos todo.TodoList)
}

// TodoWriteInput represents the input for the todo_write tool
type TodoWriteInput struct {
	Todos string `json:"todos" description:"JSON array of todo items with content, active_form, and status"`
}

// NewTodoWriteTool creates a tool for writing/updating the todo list
func NewTodoWriteTool(todoWriter TodoWriter) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"todo_write",
		"Write or update the todo list. Input is a JSON array of todo items. IMPORTANT: The 'content' field must remain exactly the same when updating status. Fields: content (task description, must not change when updating status), active_form (present continuous verb form), status (pending, in_progress, completed). Example: {\"content\":\"Install dependencies\",\"active_form\":\"Installing dependencies\",\"status\":\"pending\"}",
		func(ctx context.Context, input TodoWriteInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if input.Todos == "" {
				return fantasy.NewTextErrorResponse("todos is required"), nil
			}

			var todos todo.TodoList
			if err := json.Unmarshal([]byte(input.Todos), &todos); err != nil {
				return fantasy.NewTextErrorResponse("invalid todos JSON: " + err.Error()), nil
			}

			// Validate todo items
			for i, item := range todos {
				if item.Content == "" {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item at index %d: content is required", i)), nil
				}
				if item.ActiveForm == "" {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item at index %d: active_form is required", i)), nil
				}
				if item.Status == "" {
					item.Status = "pending"
					todos[i] = item
				}
				if item.Status != "pending" && item.Status != "in_progress" && item.Status != "completed" {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item at index %d: status must be pending, in_progress, or completed", i)), nil
				}
			}

			// Get current todos
			currentTodos := todoWriter.GetTodos()

			// Check if this is a repeated write of the same content (loop detection)
			lastWritten := todoWriter.GetLastWrittenTodos()
			if len(lastWritten) > 0 && len(todos) == len(lastWritten) {
				// Build a map of last written todos by content
				lastWrittenMap := make(map[string]todo.TodoItem, len(lastWritten))
				for _, item := range lastWritten {
					lastWrittenMap[item.Content] = item
				}

				// Check if the new todos are identical to the last written todos
				identical := true
				for _, item := range todos {
					if lastItem, found := lastWrittenMap[item.Content]; !found || lastItem.Status != item.Status {
						identical = false
						break
					}
				}

				if identical {
					// This looks like a loop - the agent is repeatedly writing the same todos
					return fantasy.NewTextErrorResponse(
						"LOOP DETECTED: You are repeatedly writing the same todo list without making progress. " +
							"This suggests you may be stuck. Please:\n" +
							"1. STOP calling todo_write with the same content\n" +
							"2. Actually EXECUTE the pending tasks instead of just updating the todo list\n" +
							"3. Only call todo_write when you have genuinely completed a task and need to mark it as completed\n" +
							"4. If you cannot complete a task, explain why to the user instead of looping"), nil
				}
			}

			// If there are existing todos, validate that only status changes are made
			if len(currentTodos) > 0 {
				// Build a map of current todos by content
				currentMap := make(map[string]todo.TodoItem, len(currentTodos))
				for _, item := range currentTodos {
					currentMap[item.Content] = item
				}

				// Check each incoming todo
				for i, item := range todos {
					existing, found := currentMap[item.Content]
					if !found {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item at index %d: content \"%s\" not found in existing todo list. New items cannot be added after initial creation.", i, item.Content)), nil
					}
					// Check if active_form changed
					if item.ActiveForm != existing.ActiveForm {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item at index %d: active_form cannot be changed (was \"%s\", got \"%s\")", i, existing.ActiveForm, item.ActiveForm)), nil
					}
					// Status changes are allowed
					todos[i].Status = item.Status
				}

				// Check for missing items (items in current but not in new list)
				newMap := make(map[string]bool, len(todos))
				for _, item := range todos {
					newMap[item.Content] = true
				}
				for _, existing := range currentTodos {
					if !newMap[existing.Content] {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("todo item \"%s\" is missing from the new list. All existing items must be included.", existing.Content)), nil
					}
				}
			}

			// Set todos via session (this will send TagTodo to adaptors)
			todoWriter.SetTodos(todos)
			// Track last written todos for loop detection
			todoWriter.SetLastWrittenTodos(todos)

			// Check if all todos are completed, if so clear the list
			allCompleted := true
			for _, item := range todos {
				if item.Status != "completed" {
					allCompleted = false
					break
				}
			}
			if allCompleted && len(todos) > 0 {
				todoWriter.SetTodos(todo.TodoList{})
				return fantasy.NewTextResponse("All tasks completed! Todo list cleared."), nil
			}

			return fantasy.NewTextResponse("Todo list updated with " + fmt.Sprintf("%d", len(todos)) + " items"), nil
		},
	)
}
