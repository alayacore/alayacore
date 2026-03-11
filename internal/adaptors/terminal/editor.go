package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorFinishedMsg is sent when external editor closes
type editorFinishedMsg struct {
	content string
	err     error
}

// Editor handles external editor operations
type Editor struct {
	tempFilePrefix string
}

// NewEditor creates a new editor handler
func NewEditor() *Editor {
	return &Editor{
		tempFilePrefix: "alayacore-input-*.txt",
	}
}

// Open opens an external editor for multi-line input
func (e *Editor) Open(currentContent string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	tmpFile, err := os.CreateTemp("", e.tempFilePrefix)
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: err}
		}
	}

	tmpFileName := tmpFile.Name()

	if currentContent != "" {
		if _, err := tmpFile.WriteString(currentContent); err != nil {
			tmpFile.Close()
			os.Remove(tmpFileName)
			return func() tea.Msg {
				return editorFinishedMsg{content: "", err: err}
			}
		}
	}
	tmpFile.Close()

	cmd := exec.Command(editorCmd, tmpFileName)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpFileName)

		if err != nil {
			return editorFinishedMsg{content: "", err: err}
		}

		content, readErr := os.ReadFile(tmpFileName)
		if readErr != nil {
			return editorFinishedMsg{content: "", err: readErr}
		}

		return editorFinishedMsg{content: string(content), err: nil}
	})
}

// FileEditorFinishedMsg is sent when external editor closes for a specific file
type FileEditorFinishedMsg struct {
	Path string
	Err  error
}

// OpenFile opens an external editor for a specific file path
func (e *Editor) OpenFile(path string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{Path: path, Err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	cmd := exec.Command(editorCmd, path)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return FileEditorFinishedMsg{Path: path, Err: err}
	})
}

// FormatEditorContent formats editor content for preview in the input field
func FormatEditorContent(content string) string {
	lineCount := strings.Count(content, "\n") + 1
	preview := strings.Fields(content)
	var previewText string
	if len(preview) > 0 && len(preview[0]) > 20 {
		previewText = preview[0][:20] + "..."
	} else if len(preview) > 0 {
		previewText = preview[0]
	} else {
		previewText = "(empty)"
	}
	return fmt.Sprintf("[%d lines] %s (press Enter to send)", lineCount, previewText)
}

// getEditorCommand returns the editor command to use
// First checks EDITOR env var, then tries vim, vi, nano in order
func getEditorCommand(editorCmd string) string {
	if editorCmd != "" {
		return editorCmd
	}

	for _, editor := range []string{"vim", "vi", "nano"} {
		path, err := exec.LookPath(editor)
		if err == nil {
			return path
		}
	}

	return ""
}
