package terminal

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/alayacore/internal/todo"
)

// TodoModel displays the todo list when present.
type TodoModel struct {
	todos  todo.TodoList
	styles *Styles
	width  int
}

// NewTodoModel creates a new todo model
func NewTodoModel(styles *Styles) TodoModel {
	return TodoModel{
		styles: styles,
		width:  DefaultWidth,
	}
}

// Init initializes the todo model
func (m TodoModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the todo model
func (m TodoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

// View renders the todo list
func (m TodoModel) View() tea.View {
	if len(m.todos) == 0 {
		return tea.NewView("")
	}

	var sb strings.Builder
	sb.WriteString(m.styles.TodoHeader.Render("TODO LIST"))
	sb.WriteString("\n")

	for i, item := range m.todos {
		var statusStyle lipgloss.Style
		var todoText string

		switch item.Status {
		case "pending":
			statusStyle = m.styles.Pending
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		case "in_progress":
			statusStyle = m.styles.InProgress
			todoText = fmt.Sprintf("%d. %s", i+1, item.ActiveForm)
		case "completed":
			statusStyle = m.styles.Completed
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		}

		sb.WriteString(statusStyle.Render(todoText))
		if i < len(m.todos)-1 {
			sb.WriteString("\n")
		}
	}

	return tea.NewView(sb.String())
}

// SetTodos updates the todo list
func (m *TodoModel) SetTodos(todos todo.TodoList) {
	m.todos = todos
}

// Count returns the number of todos
func (m TodoModel) Count() int {
	return len(m.todos)
}

// RenderString returns the rendered todo string for embedding in layout
func (m TodoModel) RenderString() string {
	if len(m.todos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(m.styles.TodoHeader.Render("TODO LIST"))
	sb.WriteString("\n")

	for i, item := range m.todos {
		var statusStyle lipgloss.Style
		var todoText string

		switch item.Status {
		case "pending":
			statusStyle = m.styles.Pending
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		case "in_progress":
			statusStyle = m.styles.InProgress
			todoText = fmt.Sprintf("%d. %s", i+1, item.ActiveForm)
		case "completed":
			statusStyle = m.styles.Completed
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		}

		sb.WriteString(statusStyle.Render(todoText))
		if i < len(m.todos)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// SetWidth sets the width for rendering
func (m *TodoModel) SetWidth(width int) {
	m.width = width
}

var _ tea.Model = (*TodoModel)(nil)
