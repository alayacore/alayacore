package terminal

// Session state cache: status and models written by the session
// goroutine and read by the UI goroutine for display updates.
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
// for race-free access between the session and UI goroutines.
//
// mu is a pointer to prevent copying when sessionState is embedded in
// outputWriter by value.
type sessionState struct {
	mu *sync.Mutex

	// Status fields
	contextTokens  int64
	contextLimit   int64
	inProgress     bool
	currentStep    int
	maxSteps       int
	reasoningLevel int

	// Last completed task's step summary, shown in the status bar until the
	// next task starts. Captured on the completion edge before the
	// completion broadcast overwrites currentStep with 0; 0 = no completed
	// run (e.g. startup or an instant failure before step 1).
	lastCurrentStep int
	lastMaxSteps    int // 0 = unlimited (--max-steps not set)

	// Video config
	videoFPS int
	videoRes int

	// statusVersion increments on every status-affecting update. The
	// Terminal caches the last-seen version and skips the status-bar
	// rebuild when nothing has changed since the last snapshot — the
	// status bar (steps, tokens, MCP, theme, video, model) only changes
	// a few times per task, but the tick handler rebuilds it 4×/sec
	// regardless.
	statusVersion uint64

	// modelVersion increments only on model-list / active-model updates
	// (not on task / status / theme changes). The Terminal caches the
	// last-seen version and skips the model-selector rebuild when the
	// list and active ID are unchanged.
	modelVersion uint64

	// MCP init status — tracks the initialization phase for progress
	// display. Values: "" (no MCP), "connecting", "connected", "failed",
	// "auth_required", "auth_running", "done".
	// NOTE: this is display-only progress. The authoritative "initialization
	// complete" signal is sessionReady, driven by the SM "session" frame.
	mcpStatus string

	// sessionReady is set by the SM "session" frame (state "ready") — the
	// authoritative signal that the session finished initialization
	// (replay + MCP settle). Consumed one-shot by takeSessionReady to
	// close the init overlay exactly once.
	sessionReady bool

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
// On the completion edge (in-progress → done) the last known step values
// are snapshotted before the completion broadcast overwrites currentStep
// with 0; on the start edge (done → in-progress) they are reset so the
// next run shows live progress rather than the previous run's summary.
// The completion edge is also returned to the caller so it can settle
// tool windows left pending by abnormal paths (see
// outputWriter.handleSystemTask / WindowBuffer.SettleUnfinishedTools).
func (s *sessionState) updateTask(inProgress bool, currentStep, maxSteps int, context int64) (completed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inProgress && !inProgress {
		s.lastCurrentStep = s.currentStep
		s.lastMaxSteps = s.maxSteps
		completed = true
	}
	if !s.inProgress && inProgress {
		s.lastCurrentStep = 0
		s.lastMaxSteps = 0
	}
	s.inProgress = inProgress
	s.currentStep = currentStep
	s.maxSteps = maxSteps
	s.contextTokens = context
	s.statusVersion++
	return completed
}

// updateModel atomically updates active model info.
func (s *sessionState) updateModel(activeID int, activeName string, contextLimit int64) {
	s.mu.Lock()
	s.activeModelID = activeID
	s.activeModelName = activeName
	s.contextLimit = contextLimit
	s.statusVersion++
	s.modelVersion++
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
	s.statusVersion++
	s.modelVersion++
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
	s.statusVersion++
	s.mu.Unlock()
}

// updateThemeList atomically replaces the cached theme list.
func (s *sessionState) updateThemeList(themes []ThemeEntry) {
	s.mu.Lock()
	s.cachedThemeList = themes
	s.statusVersion++
	s.mu.Unlock()
}

// updateReasoning atomically updates the reasoning level.
func (s *sessionState) updateReasoning(level int) {
	s.mu.Lock()
	s.reasoningLevel = level
	s.statusVersion++
	s.mu.Unlock()
}

// updateVideoConfig atomically updates the video FPS and resolution.
func (s *sessionState) updateVideoConfig(fps, res int) {
	s.mu.Lock()
	s.videoFPS = fps
	s.videoRes = res
	s.statusVersion++
	s.mu.Unlock()
}

// updateMCPProgress atomically updates MCP init progress.
// Called when the session sends an "mcp" system message with status
// "connecting", "connected", "failed", "auth_required", "auth_running",
// or "done" (the display terminal state — overlay closure is driven by
// the SM "session" ready frame, see takeSessionReady).
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
	s.statusVersion++
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

// markSessionReady records the SM "session" ready frame — the
// authoritative signal that initialization completed (replay + MCP
// settle). Unlike the MCP progress "done" status (which is display-only
// and also sent for canceled/aborted init, never sent without MCP), this
// flag is set for every session exactly once.
func (s *sessionState) markSessionReady() {
	s.mu.Lock()
	s.sessionReady = true
	s.mu.Unlock()
}

// takeSessionReady returns true if initialization completed (session
// ready frame received) and resets the flag. One-shot — the Terminal
// uses this to close the init overlay exactly once.
func (s *sessionState) takeSessionReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionReady {
		return false
	}
	s.sessionReady = false
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
	snap := StatusSnapshot{
		ContextTokens:   s.contextTokens,
		ContextLimit:    s.contextLimit,
		InProgress:      s.inProgress,
		CurrentStep:     s.currentStep,
		MaxSteps:        s.maxSteps,
		LastCurrentStep: s.lastCurrentStep,
		LastMaxSteps:    s.lastMaxSteps,
		ReasoningLevel:  s.reasoningLevel,
		ActiveModel:     s.activeModelName,
		ActiveTheme:     s.activeTheme,
		ActiveThemeData: s.activeThemeData,
		VideoFPS:        s.videoFPS,
		VideoRes:        s.videoRes,
		MCPStatus:       s.mcpStatus,
		MCPServer:       s.mcpServer,
		Version:         s.statusVersion,
	}
	// Copy slices only when populated — `append([]T(nil), nil...)` allocates
	// an empty non-nil slice, which is wasted work every tick when no MCP
	// servers / themes are configured.
	if s.mcpServers != nil {
		snap.MCPServers = append([]string(nil), s.mcpServers...)
	}
	if s.cachedThemeList != nil {
		snap.CachedThemes = append([]ThemeEntry(nil), s.cachedThemeList...)
	}
	return snap
}

// snapshotModels returns a consistent point-in-time view of model state.
func (s *sessionState) snapshotModels() ModelSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ModelSnapshot{
		ActiveID:   s.activeModelID,
		ActiveName: s.activeModelName,
		Version:    s.modelVersion,
	}
	// Copy the slice only when populated — `append([]T(nil), nil...)`
	// allocates an empty non-nil slice every tick when no models are
	// configured.
	if s.models != nil {
		snap.Models = append([]protocol.ModelInfo(nil), s.models...)
	}
	return snap
}
