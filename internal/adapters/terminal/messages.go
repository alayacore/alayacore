package terminal

// The message set: every type the event loop may deliver. Msg is sealed to these
// (program.go), which is what makes "a message nobody handles" impossible to
// smuggle in — an unmarked type is a compile error at the point someone tries to
// send it, not a silent drop in Update.
//
// This list is the whole set. A component's reported facts are Results
// (result.go), not messages, and are deliberately absent here: a selection, an
// editor request and a confirm outcome never travel through the loop.
//
// Handled by Program.handleMsg and never reaching model.Update:
//
//	QuitMsg, SuspendMsg, execMsg, BatchMsg, sequenceMsg, forceRepaintMsg
//
// Handled by Terminal.Update:
//
//	KeyMsg (KeyPressMsg), PasteMsg, FocusMsg, BlurMsg, WindowSizeMsg, tickMsg,
//	themePreviewMsg, sessionLoadedMsg, sessionLoadingErrorMsg, editorStartMsg,
//	EditorFinishedMsg, displayErrorMsg, displayNotifyMsg

func (QuitMsg) isMsg()                {}
func (SuspendMsg) isMsg()             {}
func (execMsg) isMsg()                {}
func (BatchMsg) isMsg()               {}
func (sequenceMsg) isMsg()            {}
func (forceRepaintMsg) isMsg()        {}
func (KeyPressMsg) isMsg()            {}
func (PasteMsg) isMsg()               {}
func (FocusMsg) isMsg()               {}
func (BlurMsg) isMsg()                {}
func (WindowSizeMsg) isMsg()          {}
func (tickMsg) isMsg()                {}
func (themePreviewMsg) isMsg()        {}
func (sessionLoadedMsg) isMsg()       {}
func (sessionLoadingErrorMsg) isMsg() {}
func (editorStartMsg) isMsg()         {}
func (EditorFinishedMsg) isMsg()      {}
func (displayErrorMsg) isMsg()        {}
func (displayNotifyMsg) isMsg()       {}

// failOnUnknownDispatch makes the two dispatch-table defaults fatal: Update's
// unknown-message case (tui.go) and applyResult's unknown-result tail
// (result.go). TestMain (messages_test.go) sets it. A type added to the sealed
// Msg or Result set without a handler then fails the suite instead of being
// dropped — which is the point of sealing either one. Production leaves it false,
// where both defaults are unreachable anyway.
var failOnUnknownDispatch bool
