# AlayaCore Project Status

## Overview
AlayaCore is a minimal AI Agent that can handle toolcalling. It provides five tools: `read_file` (supports line range), `edit_file` (search/replace), `write_file`, `activate_skill`, and `posix_shell`.
All skills are based on these tools.

For this project, simplicity is more important than efficiency.

## Implementation Status

### Completed
- ✅ Go module initialized (github.com/alayacore/alayacore)
- ✅ fantasy dependency added (v0.11.0)
- ✅ Direct stdin reading for terminal input
- ✅ Basic agent structure with OpenAI provider
- ✅ Shell tool implementation with `fantasy.NewAgentTool`
- ✅ Command-line interface with prompt input
- ✅ Tool calling support with posix_shell command execution
- ✅ Tool result display
- ✅ Usage statistics (input/output/total tokens)
- ✅ Multi-provider support (OpenAI, Anthropic, DeepSeek, ZAI)
- ✅ Color styling with ANSI escape codes
  - AI responses: bold white (#cdd6f4)
  - User prompts: blue (#89b4fa)
  - Debug/info messages: dim gray (#6c7086)
  - Applied to both single-prompt and interactive modes
  - Uses raw ANSI codes for lightweight output without padding
- ✅ CLI flags for version and help information
- ✅ Custom system prompts (--system) to override default behavior
- ✅ README.md with comprehensive documentation
- ✅ Terminal input handling (bubbles textinput)
  - Automatic TTY detection
  - Command history support (~/.alayacore_history, max 1000 entries)
  - Proper backspace/delete for all character encodings
  - Ctrl-C interruption support
- ✅ Real-time streaming output
  - All text (including thinking) displays immediately
  - Interleaved with posix_shell command outputs
- ✅ API debug mode (--debug-api)
  - Logs raw API requests and responses to stderr
  - Shows HTTP method, URL, headers (with sensitive data masked), and body
  - Colors request messages in green and response messages in purple
  - Useful for troubleshooting API communication issues
- ✅ Prompt formatting with cyan brackets, green usernames, and consistent background
  - Format: «USER@LOCALHOSTNAME — MODEL@API_URL»
  - Background color applied to bracketed section only
  - Newline before green > prompt character with single space
- ✅ Shell command visibility
  - Commands printed when they start with arrow prefix (→)
  - Command text in green, arrow in dim color
  - Newlines and tabs escaped for clean single-line display
- ✅ Ctrl-C handling in interactive mode
  - Cancels unfinished requests when Ctrl-C is pressed
  - Does nothing (continues waiting) if no request is in progress (at the prompt)
  - Uses context cancellation to stop ongoing API calls
  - Displays "Request cancelled." message when a request is interrupted
  - Properly handles Ctrl-C to prevent process termination
- ✅ Ctrl-G handling in Terminal mode
  - Cancels current request when Ctrl-G is pressed
  - Works similar to the cancel button in WebSocket client
  - Context is automatically recreated after cancellation for subsequent requests
  - Displays "Request cancelled." message when interrupted
- ✅ Refactored codebase for better maintainability
  - Extracted CLI flag parsing to `internal/config` package
  - Refactored terminal adaptor structure
  - Simplified main.go to ~80 lines of minimal glue code
- ✅ CLI-based provider configuration
  - All config via CLI flags: --type, --base-url, --api-key, --model
  - No environment variables or default configs
  - Supports anthropic and openai provider types
- ✅ Skills system based on agentskills.io specification
  - Skill discovery from directories with SKILL.md files
  - YAML frontmatter parsing (name, description, license, compatibility)
  - Progressive disclosure: metadata at startup, full content on activation
  - System prompt injection with XML format for available skills
  - Skill activation via Manager.ActivateSkill()
  - Test coverage for parsing, discovery, and activation
- ✅ IOStream abstraction layer
  - Input/Output interfaces in internal/stream/stream.go
  - TLV protocol (TagAssistantText='A', TagTool='T', TagReasoning='R', TagError='E', TagNotify='N', TagSystem='S', TagUserText='U', TagModel='M')
  - Buffered reads/writes with Flush() method
  - ChanInput helper for channel-based input with configurable buffer
  - WriteTLV/ReadTLV functions for encoding/decoding
- ✅ Adaptors in internal/adaptors/
  - terminal.go - Terminal adaptor with Terminal (lipgloss/bubbletea)
  - websocket.go - WebSocket server with per-client sessions
  - styles.go - lipgloss styling for terminal UI
  - chat.html - Embedded chat UI
  - Removed NewSession function - create processor/session directly
- ✅ alayacore-web command
  - cmd/alayacore-web/main.go entry point
  - Per-client independent agent sessions
  - Embedded chat UI (auto-served at /)
  - WebSocket endpoint at /ws
- ✅ Tab key to switch focus between display and input windows
  - Focused window has bright border (#89d4fa), unfocused has dimmed border (#45475a)
  - When display focused: j/k move window cursor; J/K scroll screen
- ✅ TLV protocol for user-to-session communication
  - Added TagUserText='A' for user text input from client to session
  - Session reads TLV messages from input stream and unwraps TagUserText
- ✅ Simplified auto-summarize mechanism
  - Removed `skipAutoSummarize` flag and `prependTasks` function
  - Auto-summarize now runs synchronously before processing user prompt
  - Cleaner linear control flow without task queue manipulation
  - `shouldAutoSummarize()` helper for threshold check
  - `autoSummarize()` delegates to shared `summarize()` function
  - `:cancel` command calls `handleCommandSync()` directly for immediate execution (not queued)
  - Other commands are queued like user prompts via `submitTask()`
  - Session validates tags and emits TagError for invalid ones
  - Session detects commands (starts with ":") and routes to handler
  - Session checks command errors and emits TagError to user
  - ChanInput helper in stream.go with configurable buffer size
  - Terminal uses 10-buffer for human-paced input
  - WebSocket uses 100-buffer for network-paced input
  - HTML client encodes user input as TagUserText TLV
  - Removed adaptors.NewSession - adaptors create processor/session directly
- ✅ Terminal display color persistence
  - Per-line styling for dimmed text (reasoning) to preserve color during scrolling
  - Wordwrap preserves ANSI escape sequences across line breaks
- ✅ Terminal viewport initial position
  - Session content displays at correct scroll position when loading from file
- ✅ **Upgraded to bubbletea/lipgloss/bubbles v2.x**
  - Updated go.mod with v2 versions from charm.land vanity domain
  - Fixed breaking API changes (View(), KeyMsg, Viewport, textinput styles)
  - All tests pass, project builds successfully

- ✅ **Added confirmation dialog for :cancel command**
  - Confirmation dialog similar to `:quit` for `:cancel` command
  - Ctrl+G shows confirmation dialog before sending command

- ✅ **Window container feature with synchronized widths**
  - Transformed display area into a container of windows with dimmed borders
  - Synchronized widths between windows and input box
  - Delta messages include stream ID prefix (`[:id:]`) for routing to correct windows
  - Non-delta messages create new windows; deltas append to existing windows

- ✅ **Window Cursor feature for navigating between windows**
  - Window Cursor highlights one window at a time with a bright border
  - `j`/`k` keys to navigate (like vi); `J`/`K` for screen scrolling
  - `g`/`G` jumps to first/last window
  - Cursor defaults to last window and updates when new windows are created

- ✅ **Terminal focus/blur handling**
  - Display and input appear dimmed when user switches away from the program
  - Focus is restored when switching back

- ✅ **Terminal display performance optimizations**
  - KeyMsg returns immediately; display updates only on tick (every 250ms during streaming)
  - Incremental window rendering - only re-render the window that changed
  - Cursor-only border swap reuses cached content

- ✅ **Context limit flag and status bar fraction display**
  - Added `--context-limit` CLI flag to specify provider's context window size in tokens
  - Supports K/M suffixes: `200K` → 200000, `1M` → 1000000
  - Status bar shows: `Context: 45231 / 128000 (35.3%) | Total: 67890` when limit is set

- ✅ **Auto-summarize at 80% context usage**
  - Automatically triggers `:summarize` command when context reaches 80% of the limit
  - Shows notification with current usage before summarizing
  - Prevents context overflow errors from API providers
  - Only triggers when `--context-limit` is configured

- ✅ **Model Selector UI for switching/managing model configurations**
  - Press `Ctrl+L` to open model selector overlay
  - Floating overlay using lipgloss layers and compositor (centered on screen)
  - List view shows saved models with details (protocol type, base URL)
  - Key bindings: `e` edit file, `r` reload, `enter` select, `esc` close
  - External editor ($EDITOR or vi) for editing model config file
  - Uses `tea.ExecProcess` for proper terminal state handling when editor exits
  - Model window remains open after editor exits, models auto-reload
  - Config file: `~/.alayacore/models.conf` (default) or custom path via `--model-config`
  - **IMPORTANT: Program NEVER writes to model config file**
    - Users must edit the file manually with text editor (press 'e')
    - This ensures user has full control over model configurations
  - Config file uses YAML-like format with `---` separator between models
  - Supported fields: `name`, `protocol_type`, `base_url`, `api_key`, `model_name`, `context_limit`
  - `context_limit` is optional and specifies maximum context length (0 means unlimited)
  - Runtime model list created from: (1) model config file, (2) CLI arguments (if provided)
  - CLI model is appended to the end of the runtime list (if CLI args provided)
  - When models exist, the last one becomes active
  - Program exits with helpful error if no models are available
  - **All CLI arguments are optional** - can run with just `alayacore` if models.conf exists
  - Located in `internal/adaptors/terminal/model_selector.go`

- ✅ **Model Management Commands**
  - `:model_get_all` - Get all available models (returns via TagSystem with models field)
  - `:model_set <ID>` - Switch to a model by its ID
  - `:model_load [file]` - Load models from config file (default: path from --model-config or ~/.alayacore/models.conf)
  - ModelManager in `internal/agent/model_manager.go` manages models with runtime IDs
  - Model info included in SystemInfo struct (models, active_model_id, active_model_config)
  - Terminal sends commands to session instead of calling session methods directly

- ✅ **Terminal adaptor refactor for clarity and maintainability**
  - Added doc.go with package-level architecture docs
  - Added constants.go for timing and layout constants
  - Renamed terminalOutput → outputWriter
  - Removed dead code: DisplayMsg, InputMsg, StatusMsg

- ✅ **Model selector focus management**
  - Input and display windows lose focus when model selector is shown

- ✅ **Prevent model switching during task execution**
  - `:model_set` command returns error "cannot switch model while task is running" when a task is in progress
  - Ensures model configuration consistency during active operations
  - Users must wait for task completion before switching models
  - Check implemented with mutex-protected `inProgress` flag in session
  - Focus is restored to previously focused window when model selector closes
  - Provides better visual feedback and prevents accidental input

- ✅ **Removed unused session directory code**
  - Deleted `LoadLatestSession()`, `GetSessionsDir()`, and `GenerateSessionFilename()` functions
  - Removed related tests: `TestGetSessionsDir`, `TestGenerateSessionFilename`, `TestLoadLatestSession_EmptyDir`, `TestLoadLatestSession_WithFiles`
  - Cleaned up unused imports (`sort`, `time`)
  - Session persistence now handled via explicit file paths only (no directory scanning)

### Architecture
- **Provider Types**: `anthropic` (native Anthropic API), `openai` (OpenAI-compatible)
- **Tools**: read_file, edit_file, write_file, activate_skill, posix_shell
- **Framework**: charm.land/fantasy
- **UI Styling**: Raw ANSI escape codes (lightweight, no padding)
- **Stream Protocol**: TLV (Tag-Length-Value) for structured output
  - Session-to-user: TagAssistantText, TagTool, TagReasoning, TagError, TagSystem (JSON), TagNotify, TagUserText
  - User-to-session: TagUserText
  - Session validates and unwraps user TLV messages
  - TagSystem contains JSON-encoded SystemInfo struct with token usage, queue, and model info:
    - `{"context":1234,"total":5678,"queue":2,"models":[...],"active_model_id":"abc123"}`
    - When model changes, includes `active_model_config` with full config (including API key)

### Code Structure
```
internal/
  agent/       - Agent processor for streaming responses
  adaptors/    - Terminal and WebSocket adaptors
  config/      - CLI flags and settings parsing
  debug/       - Debug HTTP transport for API debugging
  provider/    - Provider configuration (API keys, endpoints)
  skills/      - Skills system (discovery, parsing, activation)
  stream/      - IOStream interfaces and TLV protocol
  tools/       - Tool implementations (posix_shell, read_file, edit_file, write_file, activate_skill)
cmd/alayacore-web/       - alayacore-web entry point
main.go        - alayacore entry point
```

### Features
- Execute posix_shell commands through the AI agent
- Multi-step conversations with tool calls
- Token usage tracking
- Error handling for command execution
- CLI-based provider configuration (no env vars)
- CLI flags: --type, --base-url, --api-key, --model, --skill, --session, --context-limit
- Provider types: anthropic, openai
- Color-coded output for better readability
- Command history for interactive sessions
- Direct stdin reading for terminal input
- Proper conversation history management for multi-turn tool calls
- IOStream abstraction with TLV protocol
- Web server with WebSocket support and chat UI
- Session commands: :save, :cancel, :summarize, :quit, :q
- Session file persistence for conversation history

### Usage
```sh
# OpenAI API
./alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Anthropic API
./alayacore --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4

# Local AI server (e.g., Ollama)
./alayacore --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3

# Run with API debug
./alayacore --debug-api --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run with custom system prompt
./alayacore --system "You are a code reviewer" --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run interactively
./alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run with skills
./alayacore --skill ./skills --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run with session persistence
./alayacore --session ~/mysession.md --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Show help
./alayacore --help
```

### Session Commands
- `:save [filename]` - Save session to file (uses configured session file if no filename provided)
- `:cancel` - Cancel current request
- `:summarize` - Summarize the entire conversation to a single message to reduce token usage
- `:quit`, `:q` - Exit with confirmation

### alayacore-web (WebSocket Server)
```sh
# Start WebSocket server
./alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Custom address
./alayacore-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4-20250514 --addr :9090

# Then open http://localhost:8080 in browser
# WebSocket endpoint: ws://localhost:8080/ws
```

## Next Steps
- Add more sophisticated skills built on posix_shell tool
