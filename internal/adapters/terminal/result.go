package terminal

// Result is a fact a component produces for its owner, as a value. The owner
// folds it synchronously in the same Update — which is what the old arrangement
// faked by wrapping the fact in a Cmd and calling cmd() inside Update, then
// asserting the concrete message type. Cmd stays for I/O only; a component
// reports what happened as data.
//
// The message types below already carried these facts. Marking them as Results
// is what lets a component hand one back directly instead of hiding it behind an
// opaque func().

import (
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/commands"
)

// Result is a fact reported by a component. Sealed: only this package defines
// implementations.
type Result interface{ isResult() }

func (ConfirmResultMsg) isResult()        {}
func (AttachmentSelectedMsg) isResult()   {}
func (HelpCmdMsg) isResult()              {}
func (ModelSelectedMsg) isResult()        {}
func (ReloadModelsMsg) isResult()         {}
func (ThemeSelectedMsg) isResult()        {}
func (openEditorForDisplayMsg) isResult() {}
func (openEditorForPromptMsg) isResult()  {}
func (focusInputWithValueMsg) isResult()  {}

// foldResults applies each result in order, batching the Cmds they imply. It is
// how a dispatcher consumes a component's results: the state changes happen
// here and now, the I/O is collected to run after.
func (m Terminal) foldResults(results []Result) (Terminal, Cmd) {
	var cmds []Cmd
	for _, r := range results {
		var cmd Cmd
		m, cmd = m.applyResult(r)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, Batch(cmds...)
}

// applyResult folds one Result into the model and returns any I/O it implies.
// It is the single place these facts are interpreted: Update's message cases and
// the component dispatch both call it, so a result cannot mean one thing when it
// arrives as a message and another when it is folded.
//
// A Result is applied synchronously, so a UI transition it drives — closing an
// overlay, moving focus, selecting a theme — happens in the same frame it was
// produced. The returned Cmd is only for I/O (a CI frame, opening the editor).
func (m Terminal) applyResult(r Result) (Terminal, Cmd) {
	switch msg := r.(type) {
	case ConfirmResultMsg:
		return m.handleConfirmResult(msg.Result)

	case ThemeSelectedMsg:
		return m, m.emitCommand(":" + commands.CommandNameThemeSet + " " + msg.Name)

	case ModelSelectedMsg:
		return m, m.emitCommand(fmt.Sprintf(":%s %d", commands.CommandNameModelSet, msg.ID))

	case ReloadModelsMsg:
		return m, m.emitCommand(":" + commands.CommandNameModelLoad)

	case openEditorForDisplayMsg:
		return m, m.editor.OpenForDisplay(msg.content)

	case openEditorForPromptMsg:
		return m, m.editor.Open(msg.content)

	case focusInputWithValueMsg:
		m = m.focusInput()
		m.input = m.input.WithValue(msg.value).CursorEnd()
		m.display = m.display.updateContent()
		return m, nil

	case AttachmentSelectedMsg:
		if strings.HasPrefix(msg.Path, "http://") || strings.HasPrefix(msg.Path, "https://") {
			m = m.addURLAttachment(msg.Path)
		} else {
			m = m.addAttachment(msg.Path)
		}
		return m, nil

	case HelpCmdMsg:
		m = m.focusInput()
		m.input = m.input.WithValue(msg.Command + " ").CursorEnd()
		m.display = m.display.updateContent()
		return m, nil
	}
	// Unreachable: Result is sealed to the types above, and every one has a case.
	// Under test it is fatal, so a result type added to the set without a case is
	// caught here, exactly as an unhandled message is caught in Update.
	if failOnUnknownDispatch {
		panic(fmt.Sprintf("terminal: unhandled result %T", r))
	}
	return m, nil
}
