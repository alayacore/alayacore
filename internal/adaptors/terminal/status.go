package terminal

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// StatusModel shows the status bar (token usage, etc).
type StatusModel struct {
	status     string
	inProgress bool // Whether session has a task in progress
	styles     *Styles
	width      int
}

// NewStatusModel creates a new status model
func NewStatusModel(styles *Styles) StatusModel {
	return StatusModel{
		status: "",
		styles: styles,
		width:  DefaultWidth,
	}
}

// Init initializes the status model
func (m StatusModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the status model
func (m StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if windowMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = windowMsg.Width
	}
	return m, nil
}

// View renders the status bar
func (m StatusModel) View() tea.View {
	return tea.NewView(m.styles.Status.Render(m.status))
}

// SetStatus updates the status text
func (m *StatusModel) SetStatus(status string) {
	m.status = status
}

// SetInProgress updates the in-progress state
func (m *StatusModel) SetInProgress(inProgress bool) {
	m.inProgress = inProgress
}

// GetStatus returns the current status
func (m StatusModel) GetStatus() string {
	return m.status
}

// SetWidth sets the width for rendering
func (m *StatusModel) SetWidth(width int) {
	m.width = width
}

// RenderString returns the rendered status string
func (m StatusModel) RenderString() string {
	// Build status with running indicator (small centered dots)
	var indicator string
	if m.inProgress {
		// Small filled dot for active
		indicator = m.styles.Status.Foreground(lipgloss.Color(ColorSuccess)).Render("•")
	} else {
		// Small hollow dot for idle
		indicator = m.styles.Status.Foreground(lipgloss.Color(ColorDim)).Render("·")
	}

	if m.status != "" {
		// Don't re-render status - it may already contain ANSI codes
		// Apply padding (2 spaces left and right) to the entire status bar
		return m.styles.Status.Padding(0, 2).Render(indicator + " " + m.status)
	}
	return m.styles.Status.Padding(0, 2).Render(indicator)
}

var _ tea.Model = (*StatusModel)(nil)
