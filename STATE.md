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
  - Logs raw API requests and responses to local debug logs (or stderr as fallback)
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
  - TLV protocol with 2-byte tags:
    - Text: TagTextUser="TU", TagTextAssistant="TA", TagTextReasoning="TR"
    - Function: TagFunctionShow="FS", TagFunctionCall="FC", TagFunctionResult="FR"
    - System: TagSystemError="SE", TagSystemNotify="SN", TagSystemData="SD"
  - Buffered reads/writes with Flush() method
  - ChanInput helper for channel-based input with configurable buffer
  - WriteTLV/ReadTLV functions for encoding/decoding (ReadTLV uses io.ReadFull to avoid partial frames)
  - GenericReader/GenericWriter helpers to adapt plain io.Reader/io.Writer
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
- ✅ Version display in welcome screen
  - Version (e.g., v0.1.0) displayed on the last line only, at bottom right
  - Aligned at column 52 (positioned relative to the ASCII art's visual width)
  - Only one version string displayed (on the line containing "▀▀▀")
  - Right-aligned on the last 2 content lines for visual balance
  - Pulled from `config.Version` constant
  - Positioned on the last non-empty line of welcome text
- ✅ TLV protocol for user-to-session communication
  - Added TagTextUser="TU" for user text input from client to session
  - Session reads TLV messages from input stream and unwraps TagTextUser
- ✅ Adaptor refactoring: TLV-only communication
  - Adaptors communicate with session through TLV messages only
  - Removed direct ModelManager access from terminal adaptor
  - Model info (models list, active ID, config path) comes from TagSystemData
  - Model switching uses TLV flow: :model_set → TagSystemData with ActiveModelConfig → adaptor creates provider
  - Only exception: SwitchModel() called directly by adaptor for provider creation (requires proxy/debug settings)
- ✅ Simplified auto-summarize mechanism
  - Removed `skipAutoSummarize` flag and `prependTasks` function
  - Auto-summarize now runs synchronously before processing user prompt
  - Cleaner linear control flow without task queue manipulation
  - `shouldAutoSummarize()` helper for threshold check
  - `autoSummarize()` delegates to shared `summarize()` function
  - `:cancel` command calls `handleCommandSync()` directly for immediate execution (not queued)
  - Other commands are queued like user prompts via `submitTask()`
  - Session validates tags and emits TagSystemError for invalid ones
  - Session detects commands (starts with ":") and routes to handler
  - Session checks command errors and emits TagSystemError to user
  - ChanInput helper in stream.go with configurable buffer size
  - Terminal uses 10-buffer for human-paced input
  - WebSocket uses 100-buffer for network-paced input
  - HTML client encodes user input as TagTextUser TLV
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
  - **Search/Filter Functionality:**
    - Search input box above model list with "/ " prompt and "Search models..." placeholder
    - Real-time case-insensitive filtering across model name, protocol type, model name, and base URL
    - TAB key switches focus between search input and model list
    - Search input always has border (blue #89d4fa when focused, gray #45475a when blurred)
    - Search input and model list have same width as main input box
    - Fixed height model list (15 items) with scrolling support
    - Performance optimized with pre-computed lowercase fields and smart filtering
    - No outer border on model window; only model list has border

- ✅ **Runtime Configuration for persisting active model**
  - `runtime.conf` file stores runtime data that changes during execution
  - Default location: same directory as `models.conf` (e.g., `~/.alayacore/runtime.conf`)
  - Custom path via `--runtime-config` CLI flag
  - Currently stores: `active_model: "Model Name"` (the active model's name)
  - On startup: loads `runtime.conf` after `models.conf`, finds model by name, sets it active
  - When model is switched: saves new active model name to `runtime.conf`
  - **File is created automatically** if it doesn't exist (unlike readonly models.conf)
  - RuntimeManager in `internal/agent/runtime_manager.go` handles load/save
  - File format is YAML-like for consistency with models.conf
  - **Fixed**: Tick handler now always runs (not just during streaming) to process model switches
  - Clarified RuntimeManager locking and file-save behavior
  - Removed unused internal fields for simpler state

- ✅ **Model Management Commands**
  - `:model_get_all` - Get all available models (returns via TagSystemData with models field)
  - `:model_set <ID>` - Switch to a model by its ID (works even during task execution)
  - `:model_load [file]` - Load models from config file (default: path from --model-config or ~/.alayacore/models.conf)
  - ModelManager in `internal/agent/model_manager.go` manages models with runtime IDs
  - Model info included in SystemInfo struct (models, active_model_id, active_model_config)
  - Terminal sends commands to session instead of calling session methods directly

- ✅ **Terminal adaptor refactor for clarity and maintainability**
  - Added doc.go with package-level architecture docs
  - Added constants.go for timing and layout constants
  - Renamed terminalOutput → outputWriter
  - Removed dead code: DisplayMsg, InputMsg, StatusMsg
  - Extracted TerminalAdaptor entrypoint into adaptor_entry.go to keep Tea model focused
  - Clarified WebSocket/terminal adaptor responsibilities and config reload flow

- ✅ **Model selector focus management**
  - Input and display windows lose focus when model selector is shown
  - Focus is restored to previously focused window when model selector closes
  - Provides better visual feedback and prevents accidental input

- ✅ **Model selector keyboard handling improvements**
  - Fixed command handling: `q`, `e`, `r` only work when list is focused, not when search input is focused
  - TAB switches focus between search input and model list
  - ENTER works in both modes (selects first model when search focused, highlighted model when list focused)
  - ESC always closes selector regardless of focus state
  - Fixed textinput delay by returning tea.Cmd instead of executing commands synchronously
  - Fixed empty model list on open by using sentinel value for lastSearchValue
  - Updated help text to show contextual commands based on focus state
  - Character keys (including `q`, `e`, `r`) can be typed in search box for filtering

- ✅ **Removed unused session directory code**
  - Deleted `LoadLatestSession()`, `GetSessionsDir()`, and `GenerateSessionFilename()` functions
  - Removed related tests: `TestGetSessionsDir`, `TestGenerateSessionFilename`, `TestLoadLatestSession_EmptyDir`, `TestLoadLatestSession_WithFiles`
  - Cleaned up unused imports (`sort`, `time`)
  - Session persistence now handled via explicit file paths only (no directory scanning)
- ✅ **Migrated to config file-only model configuration**
  - Removed CLI flags: --api-key, --base-url, --model, --type
  - Model configuration now only supported via ~/.alayacore/models.conf file
  - Removed SetInitialModel() method from ModelManager
  - Removed GetProviderConfig() method from config.Settings
  - Removed internal/provider/config.go file
  - Simplified main.go and internal/app/app.go initialization
  - Updated README.md and AGENTS.md to reflect config file-only workflow
  - Updated CLI help text and documentation

- ✅ **Fixed nil model panic on startup**
  - When no CLI model is provided, session is created with nil model
  - initAgent() now sends ActiveModelConfig via TagSystemData if there's an active model from runtime.conf
  - Terminal adaptor receives the config and calls SwitchModel() to set up the provider
  - Previously, sendSystemInfo() was called without ActiveModelConfig, causing GetActiveModel() to return nil
  - This resulted in SwitchModel() never being called, leaving the Agent with a nil model
  - Panic occurred when user sent a prompt: Agent.Stream() with nil model
- ✅ **Fixed deadlock in SwitchModel**
  - SwitchModel() was holding mutex lock while calling initAgent()
  - initAgent() calls sendSystemInfoWithModel() which tries to acquire the same mutex
  - Fixed by releasing mutex before calling initAgent()
  - Pattern: lock → update fields → unlock → call methods that may need the lock

- ✅ **Simplified session task runner + state safety**
  - Replaced spawn-per-submit task runner and 100ms idle timeout with a single long-lived task runner goroutine
  - Protected shared session state more consistently (cancel func under mutex; per-session prompt IDs via atomic counter)
  - Centralized TagSystemData emission into one helper (optional ActiveModelConfig)
  - Added session shutdown signaling to prevent goroutine leaks when input closes
  - Locked usage/context updates to avoid inconsistent TagSystemData snapshots
  - Split monolithic `session.go` into focused files (tasks, prompt streaming, commands, output/system info, persistence, markdown)
 - ✅ **Always-on terminal tick loop**
   - Terminal `Init` now starts the periodic tick loop immediately
   - Model switches from the selector (`Ctrl+L` → `enter`) are applied right away, even before the first prompt is sent
   - Keeps status/model info in sync without requiring an initial user submit
 - ✅ **Welcome screen version alignment**
   - Version is now right-aligned to the ASCII art’s right edge (no hardcoded column)
   - Web chat UI now embeds welcome text by calling `common.WelcomeText()`
- ✅ **Version constant in config**
   - `internal/config.Version` is now a simple constant defined in code (no external files or ldflags)
- ✅ **Complete message history preservation**
   - Fixed `handleUserPrompt` to save ALL messages from agent execution
   - Now includes assistant messages, tool calls, and tool results in session history
   - Replaced `processPrompt` with `processPromptWithResult` for full result access
   - Removed unnecessary abstractions: `extractAllMessages` and `extractAssistantMessage`
   - Simplified `summarize()` to directly use `processPromptWithResult`
   - Users can now naturally continue tasks with any prompt (e.g., "continue", "go on")

- ✅ **Synchronous session loading**
  - Session loads synchronously before the terminal UI starts
  - Eliminates race conditions by ensuring session is fully loaded before initialization
  - Terminal waits for session to complete before displaying anything
  - Simplified code by removing async loading logic (isLoading, NewLoadingTerminal, sessionLoadedMsg, handleSessionLoaded)
  - Better reliability at the cost of slightly slower startup for large session files
  - Complete history maintained for better context preservation and session persistence
  - Gets actual terminal size using golang.org/x/term before loading session
  - Passes initial width and height to terminal to ensure correct initial rendering
  - Window resize events are handled normally after the tea program starts

### Architecture
- **Provider Types**: `anthropic` (native Anthropic API), `openai` (OpenAI-compatible)
- **Tools**: read_file, edit_file, write_file, activate_skill, posix_shell
- **Framework**: charm.land/fantasy
- **UI Styling**: Raw ANSI escape codes (lightweight, no padding)
- **Stream Protocol**: TLV (Tag-Length-Value) for structured output with 2-byte tags:
  - Text: TagTextUser, TagTextAssistant, TagTextReasoning
  - Function: TagFunctionShow, TagFunctionCall, TagFunctionResult
  - System: TagSystemError, TagSystemNotify, TagSystemData (JSON)
  - Session-to-user: TagTextAssistant, TagFunctionShow, TagTextReasoning, TagSystemError, TagSystemData, TagSystemNotify
  - User-to-session: TagTextUser
  - Session validates and unwraps user TLV messages
  - TagSystemData contains JSON-encoded SystemInfo struct with token usage, queue, and model info:
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

- ✅ **Fixed session parsing with embedded NUL characters**
  - Problem: When `tool_result` output contained embedded NUL characters (e.g., from previous session data), the parser incorrectly split messages at those NUL characters
  - This caused incomplete `tool_call` messages to be created, leading to API errors: "Field required: input.messages.X.tool_calls.X.function"
  - Solution: Added `splitByMessageSeparators()` function that only recognizes NUL followed by known message type markers (`msg:user`, `msg:assistant`, `tool_call`, `tool_result`, etc.) as valid separators
  - Embedded NUL characters in content are now preserved correctly
  - Added tests: `TestParseMessagesWithEmbeddedNUL`, `TestSplitByMessageSeparators`, `TestLoadSessionWithEmbeddedNUL`

- ✅ **Fixed scroll position loss when switching focus (TAB or app switch)**
  - Problem: When user scrolls the display with J/K keys and then switches focus (via TAB to input, or switching apps), the scroll position is reset to bottom
  - Root cause: 'J'/'K' scrolling didn't set `userMovedCursorAway = true`, so `shouldFollow()` returned true in `updateContent()`, causing `GotoBottom()` to be called
  - Solution: Added `MarkUserScrolled()` method to DisplayModel and call it when 'J'/'K' is pressed
  - This ensures `shouldFollow()` returns false, preserving the scroll position across focus switches
  - Located in `internal/adaptors/terminal/display.go` (new method) and `internal/adaptors/terminal/terminal.go` (key handling)

## Next Steps
- Add more sophisticated skills built on posix_shell tool
