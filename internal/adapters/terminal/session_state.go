package terminal

// Session state cache: status and models written by the session
// goroutine and read by the Bubble Tea goroutine for display updates.
//
// All access is protected by the embedded sync.Mutex. The two goroutines
// never hold this lock simultaneously with WindowBuffer.mu — snapshot
// methods and system-tag updates are exclusive paths. See output.go for
// the full lock ordering design.

import (
	"sync"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
)

// sessionState caches the session's status, model, and queue item state
// for race-free access between the session and Bubble Tea goroutines.
//
// mu is a pointer to prevent copying when sessionState is embedded in
// outputWriter by value.
type sessionState struct {
	mu *sync.Mutex

	// Status fields
	contextTokens   int64
	contextLimit    int64
	inProgress      bool
	currentStep     int
	maxSteps        int
	lastCurrentStep int
	lastMaxSteps    int
	lastTaskError   bool
	reasoningLevel  int

	// Video config
	videoFPS int
	videoRes int

	// MCP init status — tracks the initialization phase.
	// Values: "" (no MCP), "connecting", "connected", "failed",
	// "auth_required", "auth_running", "done".
	mcpStatus string

	// Per-server init progress.
	mcpServer  string   // current server being connected/authorized
	mcpServers []string // full list of servers currently being initialized

	// pendingMCPAuths is a queue of MCP auth confirmations awaiting display.
	// The Terminal tick handler pops them to open confirm dialogs one at a time.
	pendingMCPAuths []mcpAuthPending

	// Pending tool confirms — set by handleSystemToolConfirm, consumed by
	// the Terminal tick handler to open the confirm overlay.
	// Stored as a queue so multiple confirms arriving at once aren't lost.
	pendingToolConfirms []toolConfirmPending

	// Model fields
	models          []protocol.ModelInfo
	activeModelID   int
	activeModelName string

	// Theme — active theme broadcast by the session via TagSystemMsg.
	// The terminal reads this in updateStatus() and applies the theme visually
	// when it detects a change from the previously applied theme.
	activeTheme     string
	activeThemeData *theme.Theme
	cachedThemeList []ThemeEntry
}

// toolConfirmPending holds a single pending tool confirmation.
type toolConfirmPending struct {
	ID    string
	Name  string
	Input string
}

// mcpAuthPending holds a single pending MCP auth confirmation.
type mcpAuthPending struct {
	server string
	url    string
}

// updateTask atomically updates task progress fields.
func (s *sessionState) updateTask(inProgress bool, currentStep, maxSteps int, context int64, taskError bool) {
	s.mu.Lock()
	// Save step info when task completes (transition from in-progress to done)
	if s.inProgress && !inProgress && s.maxSteps > 0 {
		s.lastCurrentStep = s.currentStep
		s.lastMaxSteps = s.maxSteps
		s.lastTaskError = taskError
	}
	// Reset last step info when new task starts (transition from not-in-progress to in-progress)
	if !s.inProgress && inProgress {
		s.lastCurrentStep = 0
		s.lastMaxSteps = 0
		s.lastTaskError = false
	}
	s.inProgress = inProgress
	s.currentStep = currentStep
	s.maxSteps = maxSteps
	s.contextTokens = context
	s.mu.Unlock()
}

// updateModel atomically updates active model info.
func (s *sessionState) updateModel(activeID int, activeName string, contextLimit int64) {
	s.mu.Lock()
	s.activeModelID = activeID
	s.activeModelName = activeName
	s.contextLimit = contextLimit
	s.mu.Unlock()
}

// updateModelList atomically replaces the full model list.
func (s *sessionState) updateModelList(models []protocol.ModelInfo) {
	s.mu.Lock()
	s.models = models
	// Also sync active name if models list provides it
	for _, m := range models {
		if m.ID == s.activeModelID {
			s.activeModelName = m.Name
			break
		}
	}
	s.mu.Unlock()
}

// updateTheme atomically updates the active theme.
// When themeData is nil (theme change with just name), the cached
// theme list is used to look up the full data.
func (s *sessionState) updateTheme(name string, themeData *theme.Theme) {
	s.mu.Lock()
	s.activeTheme = name
	if themeData != nil {
		s.activeThemeData = themeData
	} else {
		// Look up from cached list
		for _, ti := range s.cachedThemeList {
			if ti.Name == name {
				s.activeThemeData = ti.Theme
				break
			}
		}
	}
	s.mu.Unlock()
}

// updateThemeList atomically replaces the cached theme list.
func (s *sessionState) updateThemeList(themes []ThemeEntry) {
	s.mu.Lock()
	s.cachedThemeList = themes
	s.mu.Unlock()
}

// updateReasoning atomically updates the reasoning level.
func (s *sessionState) updateReasoning(level int) {
	s.mu.Lock()
	s.reasoningLevel = level
	s.mu.Unlock()
}

// updateVideoConfig atomically updates the video FPS and resolution.
func (s *sessionState) updateVideoConfig(fps, res int) {
	s.mu.Lock()
	s.videoFPS = fps
	s.videoRes = res
	s.mu.Unlock()
}

// updateMCPProgress atomically updates MCP init progress.
// Called when the session sends an "mcp" system message with
// status "connecting", "connected", "failed", "auth_required",
// or "auth_running".
func (s *sessionState) updateMCPProgress(status, server string) {
	s.mu.Lock()
	// Capture the previous status BEFORE overwriting: the "new init cycle"
	// reset check must see the status we are transitioning FROM. Checking
	// s.mcpStatus after the assignment would always see the incoming status
	// (e.g. "connecting"), making the reset branch dead code.
	prevStatus := s.mcpStatus
	s.mcpStatus = status
	s.mcpServer = server
	switch status {
	case "connecting":
		// New init cycle — reset list if coming from idle/done state.
		if prevStatus == "" || prevStatus == "done" {
			s.mcpServers = nil
		}
		// Add to list if not already present.
		found := false
		for _, n := range s.mcpServers {
			if n == server {
				found = true
				break
			}
		}
		if !found {
			s.mcpServers = append(s.mcpServers, server)
		}
	case "connected", "failed":
		// Remove from list.
		for i, n := range s.mcpServers {
			if n == server {
				s.mcpServers = append(s.mcpServers[:i], s.mcpServers[i+1:]...)
				break
			}
		}
	}
	s.mu.Unlock()
}

// addMCPAuthPending appends a pending MCP auth confirmation to the queue.
// Consumed by takeMCPAuthPending in the Terminal tick handler.
func (s *sessionState) addMCPAuthPending(server, url string) {
	s.mu.Lock()
	s.pendingMCPAuths = append(s.pendingMCPAuths, mcpAuthPending{server: server, url: url})
	s.mu.Unlock()
}

// takeMCPAuthPending pops the next pending MCP auth confirmation.
// Returns (server, url, ok). If queue is empty, ok is false.
func (s *sessionState) takeMCPAuthPending() (server, url string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingMCPAuths) == 0 {
		return "", "", false
	}
	p := s.pendingMCPAuths[0]
	s.pendingMCPAuths = s.pendingMCPAuths[1:]
	return p.server, p.url, true
}

// clearMCPAuths discards all pending MCP auth confirmations.
// Used when MCP init is canceled — stale auth events may have been
// queued before the cancellation took effect.
func (s *sessionState) clearMCPAuths() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingMCPAuths = s.pendingMCPAuths[:0]
}

// takeMCPDone returns true if initialization is complete (mcpStatus is "done")
// and resets mcpStatus to "". One-shot — the Terminal uses this to close
// the init overlay exactly once.
func (s *sessionState) takeMCPDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpStatus != "done" {
		return false
	}
	s.mcpStatus = ""
	return true
}

// addToolConfirmPending appends a pending tool confirmation request.
func (s *sessionState) addToolConfirmPending(id, toolName, toolInput string) {
	s.mu.Lock()
	s.pendingToolConfirms = append(s.pendingToolConfirms, toolConfirmPending{
		ID: id, Name: toolName, Input: toolInput,
	})
	s.mu.Unlock()
}

// takeToolConfirmPending pops the next pending tool confirmation.
// Returns (id, toolName, toolInput, ok). If no pending confirms, ok is false.
func (s *sessionState) takeToolConfirmPending() (id, toolName, toolInput string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingToolConfirms) == 0 {
		return "", "", "", false
	}
	p := s.pendingToolConfirms[0]
	s.pendingToolConfirms = s.pendingToolConfirms[1:]
	return p.ID, p.Name, p.Input, true
}

// snapshotStatus returns a consistent point-in-time view of session status.
func (s *sessionState) snapshotStatus() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{
		ContextTokens:   s.contextTokens,
		ContextLimit:    s.contextLimit,
		InProgress:      s.inProgress,
		CurrentStep:     s.currentStep,
		MaxSteps:        s.maxSteps,
		LastCurrentStep: s.lastCurrentStep,
		LastMaxSteps:    s.lastMaxSteps,
		TaskError:       s.lastTaskError,
		ReasoningLevel:  s.reasoningLevel,
		ActiveTheme:     s.activeTheme,
		ActiveThemeData: s.activeThemeData,
		VideoFPS:        s.videoFPS,
		VideoRes:        s.videoRes,
		MCPStatus:       s.mcpStatus,
		MCPServer:       s.mcpServer,
		MCPServers:      append([]string(nil), s.mcpServers...),
		CachedThemes:    append([]ThemeEntry(nil), s.cachedThemeList...),
	}
}

// snapshotModels returns a consistent point-in-time view of model state.
func (s *sessionState) snapshotModels() ModelSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ModelSnapshot{
		ActiveID:   s.activeModelID,
		ActiveName: s.activeModelName,
	}
	if s.models != nil {
		snap.Models = s.models
	}
	return snap
}
