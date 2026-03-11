package tools

import (
	"context"
	"encoding/json"

	"charm.land/fantasy"
	"github.com/wallacegibbon/alayacore/internal/todo"
)

// TodoReader is an interface for reading todos
type TodoReader interface {
	GetTodos() todo.TodoList
}

// TodoReadInput represents the input for the todo_read tool
type TodoReadInput struct{}

// NewTodoReadTool creates a tool for reading the todo list
func NewTodoReadTool(todoReader TodoReader) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"todo_read",
		"Read the current todo list",
		func(ctx context.Context, input TodoReadInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			todos := todoReader.GetTodos()

			data, err := json.MarshalIndent(todos, "", "  ")
			if err != nil {
				return fantasy.NewTextErrorResponse("failed to marshal todos: " + err.Error()), nil
			}

			return fantasy.NewTextResponse(string(data)), nil
		},
	)
}
