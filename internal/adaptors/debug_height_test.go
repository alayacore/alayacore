package adaptors

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wallacegibbon/coreclaw/internal/stream"
	"github.com/wallacegibbon/coreclaw/internal/todo"
)

func TestMissingTopRowWhenTodoAppears(t *testing.T) {
	// Create terminal with fixed window size
	term := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	// Simulate window size
	term.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	term.updateDisplayContent()

	// Add more lines than display height (30 lines)
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	content := strings.Join(lines, "\n")
	term.terminalOutput.display.Append(content)
	term.updateDisplayContent()

	// Simulate user scrolling up (not at bottom)
	term.userScrolledAway = true
	term.display.SetYOffset(0) // Start at top
	displayView := term.display.View()
	displayLines := strings.Split(strings.TrimRight(displayView, "\n"), "\n")
	t.Logf("Display height before: %d", term.display.Height)
	t.Logf("Display lines before: %d", len(displayLines))
	// Show first few and last few lines
	for i := 0; i < 5; i++ {
		t.Logf("  %d: %q", i, displayLines[i])
	}
	for i := len(displayLines) - 5; i < len(displayLines); i++ {
		if i >= 0 {
			t.Logf("  %d: %q", i, displayLines[i])
		}
	}

	// Record first visible line
	firstVisibleBefore := displayLines[0]
	lastVisibleBefore := displayLines[len(displayLines)-1]

	// Add a todo
	mgr := todo.NewManager()
	mgr.SetTodos([]todo.TodoItem{
		{Content: "Test todo", Status: "pending", ActiveForm: "Testing"},
	})
	term.SetTodoMgr(mgr)

	// After todo added, display height should shrink
	displayView2 := term.display.View()
	displayLines2 := strings.Split(strings.TrimRight(displayView2, "\n"), "\n")
	t.Logf("Display height after: %d", term.display.Height)
	t.Logf("Display lines after: %d", len(displayLines2))
	for i := 0; i < 5; i++ {
		t.Logf("  %d: %q", i, displayLines2[i])
	}
	for i := len(displayLines2) - 5; i < len(displayLines2); i++ {
		if i >= 0 {
			t.Logf("  %d: %q", i, displayLines2[i])
		}
	}

	firstVisibleAfter := displayLines2[0]
	lastVisibleAfter := displayLines2[len(displayLines2)-1]

	// Check if first visible line changed (missing top row) - should NOT change
	if firstVisibleBefore != firstVisibleAfter {
		t.Errorf("First visible line changed (missing top row): was %q, now %q", firstVisibleBefore, firstVisibleAfter)
	}
	// Last visible line will change because height shrinks from bottom
	if lastVisibleBefore == lastVisibleAfter {
		t.Errorf("Last visible line should change when height shrinks, but stayed %q", lastVisibleBefore)
	}
}

func TestAutoScrollKeepsBottomWhenTodoAppears(t *testing.T) {
	// Create terminal with fixed window size
	term := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	// Simulate window size
	term.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	term.updateDisplayContent()

	// Add more lines than display height (30 lines)
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	content := strings.Join(lines, "\n")
	term.terminalOutput.display.Append(content)
	term.updateDisplayContent()

	// Ensure we're at bottom (auto-scroll) - userScrolledAway defaults to false
	term.display.GotoBottom()
	displayView := term.display.View()
	displayLines := strings.Split(strings.TrimRight(displayView, "\n"), "\n")
	firstVisibleBefore := displayLines[0]
	lastVisibleBefore := displayLines[len(displayLines)-1]

	// Add a todo
	mgr := todo.NewManager()
	mgr.SetTodos([]todo.TodoItem{
		{Content: "Test todo", Status: "pending", ActiveForm: "Testing"},
	})
	term.SetTodoMgr(mgr)

	// After todo added, display height shrinks, bottom line should stay same
	displayView2 := term.display.View()
	displayLines2 := strings.Split(strings.TrimRight(displayView2, "\n"), "\n")
	firstVisibleAfter := displayLines2[0]
	lastVisibleAfter := displayLines2[len(displayLines2)-1]

	// First visible line should change (missing top row) because bottom is constant
	if firstVisibleBefore == firstVisibleAfter {
		t.Errorf("First visible line should change when bottom constant, but stayed %q", firstVisibleBefore)
	}
	// Last visible line should stay the same (bottom constant)
	if lastVisibleBefore != lastVisibleAfter {
		t.Errorf("Last visible line changed: was %q, now %q", lastVisibleBefore, lastVisibleAfter)
	}

	// Remove todo (height increases)
	mgr.SetTodos([]todo.TodoItem{})
	term.SetTodoMgr(mgr)

	displayView3 := term.display.View()
	displayLines3 := strings.Split(strings.TrimRight(displayView3, "\n"), "\n")
	_ = displayLines3[0] // first visible line (not used)
	lastVisibleAfterRemove := displayLines3[len(displayLines3)-1]

	// After removing todo, bottom should still be constant (last line same)
	if lastVisibleAfter != lastVisibleAfterRemove {
		t.Errorf("Last visible line changed after todo removed: was %q, now %q", lastVisibleAfter, lastVisibleAfterRemove)
	}
	// First visible line may change (show more lines at top)
	// No specific assertion needed
}

func TestTodoToggleScrollConsistency(t *testing.T) {
	// Test that toggling todo multiple times preserves scroll position appropriately
	term := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	term.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	term.updateDisplayContent()

	// Add enough lines to scroll
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	content := strings.Join(lines, "\n")
	term.terminalOutput.display.Append(content)
	term.updateDisplayContent()

	// Test auto-scroll mode (userScrolledAway = false)
	term.display.GotoBottom()
	bottomLineAuto := func() string {
		view := term.display.View()
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		return lines[len(lines)-1]
	}
	firstBottom := bottomLineAuto()

	// Add todo (height shrinks)
	mgr := todo.NewManager()
	mgr.SetTodos([]todo.TodoItem{{Content: "Test", Status: "pending", ActiveForm: "Testing"}})
	term.SetTodoMgr(mgr)
	secondBottom := bottomLineAuto()
	if firstBottom != secondBottom {
		t.Errorf("Auto-scroll: bottom line changed after todo added: was %q, now %q", firstBottom, secondBottom)
	}

	// Remove todo (height increases)
	mgr.SetTodos([]todo.TodoItem{})
	term.SetTodoMgr(mgr)
	thirdBottom := bottomLineAuto()
	if secondBottom != thirdBottom {
		t.Errorf("Auto-scroll: bottom line changed after todo removed: was %q, now %q", secondBottom, thirdBottom)
	}

	// Test manual scroll mode (userScrolledAway = true)
	term.userScrolledAway = true
	term.display.SetYOffset(10) // arbitrary offset
	topLineManual := func() string {
		view := term.display.View()
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		return lines[0]
	}
	firstTop := topLineManual()

	// Add todo again
	mgr.SetTodos([]todo.TodoItem{{Content: "Test2", Status: "pending", ActiveForm: "Testing2"}})
	term.SetTodoMgr(mgr)
	secondTop := topLineManual()
	if firstTop != secondTop {
		t.Errorf("Manual scroll: top line changed after todo added: was %q, now %q", firstTop, secondTop)
	}

	// Remove todo
	mgr.SetTodos([]todo.TodoItem{})
	term.SetTodoMgr(mgr)
	thirdTop := topLineManual()
	if secondTop != thirdTop {
		t.Errorf("Manual scroll: top line changed after todo removed: was %q, now %q", secondTop, thirdTop)
	}
}
