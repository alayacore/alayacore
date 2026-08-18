package terminal

// External editor support: opening $EDITOR for multi-line input,
// display viewing, queue item editing, and file editing.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2" // only ExecProcess until S2 (module 5)
)

// ============================================================================
// Editor Action Types
// ============================================================================

// EditorAction classifies what should happen after the editor closes.
type EditorAction int

const (
	EditorActionNone        EditorAction = iota // e.g. display viewing — no side effects
	EditorActionUpdateInput                     // update input field with editor content
)

// EditorFinishedMsg is sent when an external editor closes.
//
// - For EditorActionNone: content is empty, no side effects.
// - For EditorActionUpdateInput: content contains the editor's text.
type EditorFinishedMsg struct {
	Content string
	Err     error
	Action  EditorAction
}

// editorStartMsg is sent to trigger actual editor execution (lazy temp file creation)
type editorStartMsg struct {
	editorCmd  string
	editorArgs []string
	action     EditorAction
}

// ============================================================================
// Editor Message Helpers
// ============================================================================

// errEditorNotFound is returned when no text editor binary is found.
func errEditorNotFound() error {
	return fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", "))
}

// ============================================================================
// Editor
// ============================================================================

// Editor handles external editor operations
type Editor struct {
	tempFilePrefix string
	content        string
}

// NewEditor creates a new editor handler
func NewEditor() *Editor {
	return &Editor{
		tempFilePrefix: "alayacore-input-*.txt",
	}
}

// Open opens an external editor for multi-line input.
func (e *Editor) Open(currentContent string) Cmd {
	return e.openWithAction(currentContent, EditorActionUpdateInput)
}

// OpenForDisplay opens an external editor to view display window content.
// Content is NOT read back — this is a view-only operation.
func (e *Editor) OpenForDisplay(content string) Cmd {
	return e.openWithAction(content, EditorActionNone)
}

// openWithAction is the shared implementation for all editor operations
// that require temp file creation (input, display viewing).
func (e *Editor) openWithAction(content string, action EditorAction) Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() Msg {
			return EditorFinishedMsg{Err: errEditorNotFound(), Action: action}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)

	// Store content for lazy temp file creation
	e.content = content

	return func() Msg {
		return editorStartMsg{
			editorCmd:  cmd,
			editorArgs: args,
			action:     action,
		}
	}
}

// createTempFile creates a temp file with the editor content.
// This is called lazily when the editor is actually executed.
func (e *Editor) createTempFile() (string, error) {
	tmpFile, err := os.CreateTemp("", e.tempFilePrefix)
	if err != nil {
		return "", err
	}
	tmpFileName := tmpFile.Name()

	if e.content != "" {
		if _, err := tmpFile.WriteString(e.content); err != nil {
			tmpFile.Close()
			os.Remove(tmpFileName)
			return "", err
		}
	}
	tmpFile.Close()

	return tmpFileName, nil
}

// ============================================================================
// Editor Helpers
// ============================================================================

// handleEditorStart handles the lazy start of the external editor.
// Temp file is created here, ensuring cleanup happens properly.
func (m Terminal) handleEditorStart(msg editorStartMsg) (Terminal, Cmd) {
	tmpFileName, err := m.editor.createTempFile()
	if err != nil {
		return m, func() Msg {
			return displayErrorMsg{
				message: fmt.Sprintf("Failed to create temp file: %v", err),
			}
		}
	}

	cmdArgs := append([]string{tmpFileName}, msg.editorArgs...)
	//nolint:gosec // G204: Editor command from user config is intentional
	cmd := exec.Command(msg.editorCmd, cmdArgs...)

	// Bridge to the forked bubbletea ExecProcess until module 5 (S2)
	// replaces it with the self-built suspend/handoff. The wrapped Cmd is
	// a plain function returning a Msg, so it works with the self-built
	// program; the editor itself will only run once S2 handles the
	// exec message.
	return m, func() Msg {
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			defer os.Remove(tmpFileName)

			// Build the result message
			result := EditorFinishedMsg{
				Err:    err,
				Action: msg.action,
			}

			if err != nil {
				return result
			}

			// For view-only (display), don't read content back
			if msg.action == EditorActionNone {
				return result
			}

			content, readErr := os.ReadFile(tmpFileName)
			if readErr != nil {
				result.Err = readErr
				return result
			}

			result.Content = string(content)
			return result
		})()
	}
}

func getEditorCommand(editorCmd string) string {
	if editorCmd != "" {
		return editorCmd
	}

	for _, editor := range defaultEditors {
		path, err := exec.LookPath(editor)
		if err == nil {
			return path
		}
	}

	return ""
}

// splitEditorCmd splits an editor command string into the executable and its arguments.
// For example, "code --wait" becomes ("code", ["--wait"]).
func splitEditorCmd(editorCmd string) (string, []string) {
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}
