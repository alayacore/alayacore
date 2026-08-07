package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"fmt"
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// SessionState — startup lifecycle
// ============================================================================

// SessionState represents the startup lifecycle phase of a Session.
//
// Initialization consists of exactly one synchronous phase (session file
// load + replay, guaranteed complete by construction) and one asynchronous
// phase (MCP init, when MCP servers are configured). The state transitions
// are owned by the run() goroutine — see syncState() in session.go.
//
// IMPORTANT: agent/provider creation is deliberately lazy (happens on the
// first task, and MCP init completion resets an already-created agent so it
// is rebuilt with MCP tools). It is therefore NOT part of SessionState —
// SessionReady does not imply an agent exists.
type SessionState int

const (
	// SessionStarting: the Session has been constructed; session file load
	// and replay (if any) are complete. Fatal load errors abort startup
	// before Start(), so this phase implies a usable, replayed session.
	SessionStarting SessionState = iota

	// SessionInitializing: Start() has been called and MCP initialization
	// is in progress (only reached when MCP servers are configured).
	SessionInitializing

	// SessionReady: MCP initialization has settled (done, canceled, or
	// aborted) — or was never configured. Prompts are accepted.
	SessionReady
)

// String returns a stable, human-readable name for the state.
func (s SessionState) String() string {
	switch s {
	case SessionStarting:
		return "starting"
	case SessionInitializing:
		return "initializing"
	case SessionReady:
		return "ready"
	default:
		return fmt.Sprintf("SessionState(%d)", int(s))
	}
}

// ============================================================================
// TagSystemMsg (SM) payload types
// ============================================================================

// taskMsg carries task progress info (type "task").
// CommandID is non-empty when the task was started by a CI command
// (continue/summarize), correlating the async completion to its request.
type taskMsg struct {
	InProgress  bool   `json:"in_progress"`
	CurrentStep int    `json:"current_step,omitempty"`
	MaxSteps    int    `json:"max_steps,omitempty"`
	Context     int64  `json:"context"`
	TaskError   bool   `json:"task_error,omitempty"`
	CommandID   string `json:"command_id,omitempty"`
}

func (taskMsg) SystemMsgType() string { return "task" }

// modelMsg carries active model info (type "model").
type modelMsg struct {
	ActiveModelID   int    `json:"active_id"`
	ActiveModelName string `json:"active_name"`
	ContextLimit    int64  `json:"context_limit"`
}

func (modelMsg) SystemMsgType() string { return "model" }

// modelListMsg carries the full model list (type "model_list").
// Only sent when models change.
type modelListMsg struct {
	Models []protocol.ModelInfo `json:"models"`
}

func (modelListMsg) SystemMsgType() string { return "model_list" }

// themeInfo carries a theme's name and full content for adapters.
type themeInfo struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme"`
}

// themeListMsg carries all available themes (type "theme_list").
// Sent once on startup (TUI only — skipped under NoTheme) so the
// terminal can cache theme content locally.
type themeListMsg struct {
	Themes []themeInfo `json:"themes"`
}

func (themeListMsg) SystemMsgType() string { return "theme_list" }

// themeMsg carries the active theme name (type "theme").
// On startup the full Theme is included; on theme changes only the name is sent.
type themeMsg struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme,omitempty"`
}

func (themeMsg) SystemMsgType() string { return "theme" }

// reasoningMsg carries the reasoning level (type "reasoning").
type reasoningMsg struct {
	Level int `json:"level"`
}

func (reasoningMsg) SystemMsgType() string { return "reasoning" }

// videoConfigMsg carries the video FPS and resolution (type "video_config").
type videoConfigMsg struct {
	FPS int `json:"fps"`
	Res int `json:"res"`
}

func (videoConfigMsg) SystemMsgType() string { return "video_config" }

// mcpMsg communicates MCP initialization progress (type "mcp").
// The adapter uses these messages to show/hide init overlays.
//
// Status values (from InitEvent.Type):
//   - "connecting":    starting to connect a server (non-OAuth) or begins processing an OAuth server
//   - "connected":     server connected and initialized (both non-OAuth and OAuth)
//   - "failed":        connection or OAuth failed
//   - "auth_required":  session needs user to authorize this server
//   - "auth_running":  OAuth flow is running for this server
//   - "done":          all MCP initialization complete
type mcpMsg struct {
	Status string `json:"status"`
	Server string `json:"server,omitempty"`
	URL    string `json:"url,omitempty"`   // set for "auth_required"
	Error  string `json:"error,omitempty"` // set for "failed"
}

func (mcpMsg) SystemMsgType() string { return "mcp" }

// messageVersionMsg carries the TLV message format version and the
// alayacore application version (type "version").
// Sent as the first TagSystemMsg frame so adapters can validate format
// compatibility and identify the core version before processing
// subsequent messages.
type messageVersionMsg struct {
	MessageVersion int    `json:"message_version"`
	CoreVersion    string `json:"core_version"`
}

func (messageVersionMsg) SystemMsgType() string { return "version" }

// messageVersion is the current version of the message encoding
// used in session files and TagSystemMsg broadcasts.
// Increment when making backward-incompatible changes to the TLV
// message format within the session body.
//
// v11: commands moved to the CI/CO control plane — text commands
// (UT ':' sniffing) removed, command results now travel as CO frames,
// taskMsg gained command_id for async command correlation.
const messageVersion = 11

// sessionMeta is the frontmatter metadata.
type sessionMeta struct {
	CreatedAt      time.Time `config:"created_at"`
	UpdatedAt      time.Time `config:"updated_at"`
	ActiveModel    string    `config:"active_model,omitempty"`
	MessageVersion int       `config:"message_version,omitempty"`
	ReasoningLevel int       `config:"reasoning_level"`
	ContextTokens  int64     `config:"context_tokens,omitempty"`
	VideoFPS       int       `config:"video_fps"`
	VideoRes       int       `config:"video_res"`
}

// taskResultCh carries the final content list from the task goroutine to run().

// sessionData is the persisted form of a Session.
type sessionData struct {
	sessionMeta
	Contents []llm.ContentPart // source of truth on reload
}

// SessionConfig bundles all configuration for creating or restoring a session.
// This avoids passing 16+ positional parameters to newSession / RestoreFromSession.
type SessionConfig struct {
	// IO — required, provided by the adapter.
	Input  io.Reader
	Output io.Writer

	// Files — paths to configuration and session files. Empty means default / none.
	SessionFile       string
	ModelConfigPath   string
	RuntimeConfigPath string
	ThemesFolder      string

	// Agent behavior
	BaseTools         []llm.Tool
	SystemPrompt      string
	ExtraSystemPrompt string
	MaxSteps          int
	ToolConfirmTools  []string // tool names requiring user confirmation (empty = no confirmation)

	// Feature flags
	DebugLogDir   string // "" = disabled (when flag not set), "." = write to CWD, or any path
	AutoSummarize int    // 0 = disabled, 1-100 = enabled with this threshold percentage
	ProxyURL      string
	NoTheme       bool // If true, skip all theme loading, detection, and broadcasting
	NoDelta       bool // If true, suppress delta frames (At, Ar, Af, Uf); use complete frames only

	// External dependencies
	SkillsMgr *skills.Manager

	// MCPInit handles MCP initialization lifecycle (connect, OAuth, discover).
	// When non-nil, the session reads from its Events() channel in the main
	// loop and applies results (tools, system prompt, manager) internally.
	MCPInit *mcp.Initializer

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
