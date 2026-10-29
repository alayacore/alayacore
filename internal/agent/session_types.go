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
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// SessionState — startup lifecycle
// ============================================================================

// SessionState represents the startup lifecycle phase of a Session.
//
// Initialization consists of exactly one synchronous phase (session file
// load + replay, guaranteed complete by construction) and one asynchronous
// phase (MCP init, when MCP servers are configured). State transitions are
// owned by the run() goroutine: constructors store SessionStarting,
// run() sets the initial phase, setState()/syncState() (session.go) advance
// it once MCP init settles, and run()'s exit stores SessionClosed.
//
// IMPORTANT: agent/provider creation is deliberately lazy (happens on the
// first task, and MCP init completion resets an already-created agent so it
// is rebuilt with MCP tools). It is therefore NOT part of SessionState —
// SessionReady does not imply an agent exists.
type SessionState int32

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

	// SessionClosed: the session has ended. This is a terminal phase, never
	// a startup one: run() broadcasts it once on its way out, after every
	// other frame, and nothing else is written afterwards. State() never
	// leaves it, so beginTask's "must be ready" check keeps refusing new
	// work.
	SessionClosed
)

// String returns a stable, human-readable name for the state.
func (s SessionState) String() string {
	switch s {
	case SessionStarting:
		return protocol.SessionStateStarting
	case SessionInitializing:
		return protocol.SessionStateInitializing
	case SessionReady:
		return protocol.SessionStateReady
	case SessionClosed:
		return protocol.SessionStateClosed
	default:
		return fmt.Sprintf("SessionState(%d)", int(s))
	}
}

// ============================================================================
// TagSystemMsg (SM) payload types
//
// The payloads an adapter must decode live in internal/protocol, beside the
// MsgType* constants that name them: they cross the adapter/agent boundary, and a
// shape spelled on both sides is how a field rename became a silently dropped
// frame (see protocol/sm.go). The three below stay here because they carry
// theme.Theme, which protocol must not depend on (protocol/sm.go says why), and
// the terminal's copy of them is pinned by a drift test.
// ============================================================================

// themeMsg carries the active theme's name (type "theme").
//
// The name only: the palettes travel in theme_list, which an adapter caches, so
// this frame says which entry is active and nothing else. (A `theme` object was
// documented here and never sent; the field is gone rather than populated twice.)
type themeMsg struct {
	Name string `json:"name"`
}

func (themeMsg) SystemMsgType() string { return string(protocol.MsgTypeTheme) }

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

func (themeListMsg) SystemMsgType() string { return string(protocol.MsgTypeThemeList) }

// messageVersion is the adapter protocol version: the frame format adapters
// speak — the tags, the SM types, and the states those carry. It is broadcast
// as the first TagSystemMsg frame (protocol.MessageVersionMsg) so an out-of-process
// client can check compatibility before reading anything else.
//
// It moves for additive changes too — a new tag, a new state, a new command —
// because an adapter that knows only the older format has no other way to learn
// that the newer one exists.
//
// Session files record the number in their frontmatter and loading demands an
// exact match (see parseSessionMeta). That is a side effect of carrying the
// protocol version in the file, not a promise about the file's contents, and
// nothing here migrates an older file: editing the field to load one anyway is
// the user's call.
//
// What each number means — what moved at each generation, and whether a session
// file needs converting — is docs/architecture.md's "Wire version history".
const messageVersion = 18

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

// SessionConfig bundles all configuration for creating or restoring a session,
// so a constructor takes one value instead of a dozen positional parameters.
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
	ReasoningLevel    int      // startup reasoning level (0=off, 1=normal, 2=max)
	ReasoningLevelSet bool     // true when set explicitly (CLI flag wins over the session file's saved level)

	// Feature flags
	DebugLogDir   string // "" = disabled (when flag not set), "." = write to CWD, or any path
	AutoSummarize int    // 0 = disabled, 1-100 = enabled with this threshold percentage
	ProxyURL      string
	NoTheme       bool // If true, skip all theme loading, detection, and broadcasting
	NoDelta       bool // If true, suppress the delta frames (Ax, Af, Uf); use complete frames only

	// External dependencies.
	//
	// MCPInit handles MCP initialization lifecycle (connect, OAuth, discover).
	// When non-nil, the session reads from its Events() channel in the main
	// loop and applies results (tools, system prompt, manager) internally.
	MCPInit *mcp.Initializer

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
