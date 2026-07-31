package agent

// Session output helpers: writing TLV messages to the adapter output,
// tracking token usage, and broadcasting system info.
//
// Broadcasting overview:
//
//   Guaranteed broadcasts (critical state transitions):
//     handleTaskEvent(StepStartEvent)  → sendSystemInfo(SystemInfoTask)  — step counter
//     handleTaskEvent(StepFinishEvent) → sendSystemInfo(SystemInfoTask)  — token count update
//     handleTaskEvent(SetContextTokensEvent) → sendSystemInfo(SystemInfoTask) — summary correction
//     handleTaskDone()                 → sendSystemInfo(SystemInfoTask)  — task completion
//     handleModelSet/ModelLoad         → sendSystemInfo(SystemInfoModel) — model switch
//     SetReasoningLevel()              → sendSystemInfo(SystemInfoReasoning)
//     SetVideoConfig()                 → sendSystemInfo(SystemInfoVideoConfig)
//     handleThemeSet()                 → sendSystemInfo(SystemInfoTheme)
//
// All state reads in sendSystemInfo are from fields owned by run()
// or from atomic fields — no mutex needed.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/alayacore/alayacore/internal/version"
)

// SystemInfoKind selects which state to broadcast to the adapter.
// Used instead of string literals to avoid silent typos.
type SystemInfoKind int

const (
	SystemInfoAll         SystemInfoKind = iota // all state
	SystemInfoTask                              // task progress (step, in_progress)
	SystemInfoModel                             // active model
	SystemInfoTheme                             // active theme
	SystemInfoReasoning                         // reasoning level
	SystemInfoVideoConfig                       // video FPS/resolution
)

// ============================================================================
// TLV Write Helpers
//
// All writes check for a previously broken output stream.  On the first
// write error the session context is canceled, which stops the agent
// loop and prevents wasted API calls on a dead adapter.
// ============================================================================

// markOutputBroken sets the broken flag and cancels the session context.
// Idempotent — only the first call has any effect.
func (s *Session) markOutputBroken() {
	if s.outputBroken.CompareAndSwap(false, true) {
		s.sessionCancel()
	}
}

// writeTLV writes a TLV frame. On error, marks output as broken.
func (s *Session) writeTLV(tag string, value string) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	if err := tlv.WriteTLV(s.Output, tag, value); err != nil {
		s.markOutputBroken()
	}
}

// writeSystemMsg writes a TagSystemMsg frame. On error, marks output as broken.
func (s *Session) writeSystemMsg(msg protocol.SystemMsg) {
	if s.outputBroken.Load() || s.Output == nil {
		return
	}
	if err := protocol.WriteSystemMsg(s.Output, msg); err != nil {
		s.markOutputBroken()
	}
}

func (s *Session) writeError(msg string) {
	s.writeSystemMsg(protocol.ErrorMsg{Text: msg})
}

func (s *Session) writeNotify(msg string) {
	s.writeSystemMsg(protocol.NotifyMsg{Text: msg})
}

func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

// writeCmdResult writes a CO (Command Output) frame for a command call.
// On success, result is serialized into Output (nil → JSON null).
// On error, Output carries a uniform CmdError object; CmdErr codes are
// preserved, plain errors default to "ERROR". An empty id is legal and
// means the error could not be correlated to a request.
func (s *Session) writeCmdResult(id string, result any, err error) {
	msg := protocol.CmdResultMsg{ID: id}

	switch {
	case err != nil:
		msg.IsError = true
		code := "ERROR"
		var ce *CmdErr
		if errors.As(err, &ce) {
			code = ce.Code
		}
		data, marshalErr := json.Marshal(protocol.CmdError{Code: code, Message: err.Error()})
		if marshalErr != nil {
			data = json.RawMessage(`{"code":"ERROR","message":"failed to serialize command error"}`)
		}
		msg.Output = data
	case result != nil:
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			// A serialization failure is itself a command error.
			msg.IsError = true
			msg.Output = json.RawMessage(`{"code":"ERROR","message":"failed to serialize command result"}`)
		} else {
			msg.Output = data
		}
	default:
		msg.Output = json.RawMessage("null")
	}

	data, marshalErr := json.Marshal(msg)
	if marshalErr != nil {
		// Last resort: drop the frame entirely (writeTLV would corrupt).
		return
	}
	s.writeTLV(tlv.TagCommandOut, string(data))
}

// ============================================================================
// System Info Broadcasting
// ============================================================================

// sendSystemInfo sends one or more TagSystemMsg frames to the adapter.
// Must only be called from the run() goroutine.
func (s *Session) sendSystemInfo(kind SystemInfoKind) {
	switch kind {
	case SystemInfoAll:
		s.sendMessageVersionMsg()
		s.sendTaskMsg()
		s.sendModelListMsg()
		s.sendModelMsg()
		if !s.NoTheme {
			s.sendThemeListMsg()
			s.sendThemeMsg()
		}
		s.sendReasoningMsg()
		s.sendVideoConfigMsg()
	case SystemInfoTask:
		s.sendTaskMsg()
	case SystemInfoModel:
		s.sendModelMsg()
	case SystemInfoTheme:
		if !s.NoTheme {
			s.sendThemeMsg()
		}
	case SystemInfoReasoning:
		s.sendReasoningMsg()
	case SystemInfoVideoConfig:
		s.sendVideoConfigMsg()
	}
}

func (s *Session) sendMessageVersionMsg() {
	s.writeSystemMsg(MessageVersionMsg{MessageVersion: MessageVersion, CoreVersion: version.Version})
}

func (s *Session) sendTaskMsg() {
	s.writeSystemMsg(TaskMsg{
		InProgress:  s.activeTask != nil,
		CurrentStep: s.activeTaskStep(),
		MaxSteps:    s.MaxSteps,
		Context:     s.ContextTokens,
		TaskError:   false,
	})
}

func (s *Session) sendModelMsg() {
	ms := s.modelService
	s.writeSystemMsg(ModelMsg{
		ActiveModelID:   ms.ActiveModelID(),
		ActiveModelName: ms.ActiveModelName(),
		ContextLimit:    ms.contextLimit,
	})
}

// sendModelListMsg sends the full model list.
func (s *Session) sendModelListMsg() {
	ms := s.modelService
	if !ms.HasModels() {
		return
	}
	s.writeSystemMsg(ModelListMsg{
		Models: ms.GetModels(),
	})
}

func (s *Session) sendThemeMsg() {
	rm := s.modelService.runtimeMgr
	if rm == nil {
		return
	}
	name := rm.GetActiveTheme()
	s.writeSystemMsg(ThemeMsg{Name: name})
}

// loadThemeFromFile loads a theme from a file path and returns its info.
// Returns parse errors from theme loading (unknown fields, type mismatches).
func loadThemeFromFile(path string) (ThemeInfo, []string, bool) {
	name := strings.TrimSuffix(filepath.Base(path), ".conf")
	t, errs, err := theme.LoadTheme(path)
	if err != nil {
		return ThemeInfo{}, nil, false
	}
	return ThemeInfo{Name: name, Theme: t}, errs, true
}

// sendThemeListMsg sends the full list of available themes with content.
// Called once on startup so adapters can cache theme data.
// Parse errors from theme files are sent as TLV error messages so all
// adapters see them.
func (s *Session) sendThemeListMsg() {
	if s.ThemesFolder == "" {
		return
	}
	confs, err := filepath.Glob(filepath.Join(s.ThemesFolder, "*.conf"))
	if err != nil {
		return
	}
	infos := make([]ThemeInfo, 0, len(confs))
	for _, path := range confs {
		if info, errs, ok := loadThemeFromFile(path); ok {
			infos = append(infos, info)
			for _, e := range errs {
				s.writeError(e)
			}
		}
	}
	if len(infos) > 0 {
		s.writeSystemMsg(ThemeListMsg{Themes: infos})
	}
}

func (s *Session) sendReasoningMsg() {
	s.writeSystemMsg(ReasoningMsg{Level: s.modelService.reasoningLevel})
}

func (s *Session) sendVideoConfigMsg() {
	s.writeSystemMsg(VideoConfigMsg{FPS: s.modelService.videoFPS, Res: s.modelService.videoRes})
}
