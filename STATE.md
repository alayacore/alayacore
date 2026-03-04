# CoreClaw Project Status

## Overview
CoreClaw is a minimal AI Agent that can handle toolcalling. It provides six tools: `read_file`, `todo_read`, `todo_write`, `write_file`, `activate_skill`, and `posix_shell`.
All skills are based on these tools.

For this project, simplicity is more important than efficiency.

## Implementation Status

### Completed
- ✅ Go module initialized (github.com/wallacegibbon/coreclaw)
- ✅ fantasy dependency added (v0.10.0)
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
- ✅ Readline library integration for proper terminal input handling
  - Automatic TTY detection
  - Command history support (~/.coreclaw_history, max 1000 entries)
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
- ✅ Fixed prompt duplication bug
  - Separated bracketed status line from input prompt
  - Bracketed line prints once at session start
  - Input prompt shows green > with reset for clean input
- ✅ Shell command visibility
  - Commands printed when they start with arrow prefix (→)
  - Command text in green, arrow in dim color
  - Newlines and tabs escaped for clean single-line display
- ✅ Debug API filtering of empty SSE content chunks
  - Filters out empty delta content messages during streaming
  - Prevents log spam while maintaining normal SSE behavior
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
  - Removed terminal.go (consolidated isValidTag into common.go)
  - Simplified main.go to ~80 lines of minimal glue code
- ✅ CLI-based provider configuration
  - All config via CLI flags: --type, --base-url, --api-key, --model
  - No environment variables or default configs
  - Supports anthropic and openai provider types
- ✅ Fixed tool_use messages being lost in conversation history
  - Changed Processor.ProcessPrompt to return assistant message including tool calls
  - Updated session to store full assistant message (text + tool calls) in history
  - Previously only text was stored, losing tool calls between requests
- ✅ Debug API now shows Anthropic content blocks
  - Added parsing for Anthropic streaming format (content as array)
  - Shows tool_use and thinking blocks in debug output
- ✅ Thinking/thinking content display for Anthropic-compatible APIs
  - Added OnReasoningDelta and OnReasoningEnd callbacks
  - Thinking displayed in dim color with newline after
- ✅ Text before tool calls displayed as thinking for OpenAI-compatible APIs
  - Text before first tool call shown in dim (thinking style)
  - Text after tool calls shown in bright
  - Added OnTextStart for clean formatting between responses
- ✅ Fixed thinking/reasoning content for OpenAI-compatible APIs
  - Use openaicompat provider for non-OpenAI URLs (Ollama, LM Studio, DeepSeek, etc.)
  - openaicompat has reasoning content hooks that the native openai provider lacks
  - Debug transport now shows thinking field in addition to content and tool_calls
- ✅ Simplified text display for OpenAI-compatible APIs
  - All text now shown bright (no thinking detection)
- ✅ Skills system based on agentskills.io specification
  - Skill discovery from directories with SKILL.md files
  - YAML frontmatter parsing (name, description, license, compatibility)
  - Progressive disclosure: metadata at startup, full content on activation
  - System prompt injection with XML format for available skills
  - Skill activation via Manager.ActivateSkill()
  - Test coverage for parsing, discovery, and activation
- ✅ IOStream abstraction layer
  - Input/Output interfaces in internal/stream/stream.go
  - TLV protocol (TagAssistantText='B', TagTool='D', TagReasoning='C', TagError='E', TagNotify='N', TagSystem='S', TagPromptStart='P', TagUserText='A')
  - Buffered reads/writes with Flush() method
  - ChanInput helper for channel-based input with configurable buffer
  - WriteTLV/ReadTLV functions for encoding/decoding
- ✅ Adaptors in internal/adaptors/
  - terminal.go - Terminal adaptor with Terminal (lipgloss/bubbletea)
  - websocket.go - WebSocket server with per-client sessions
  - colors.go - ANSI color styling (now in common.go)
  - chat.html - Embedded chat UI
  - Removed NewSession function - create processor/session directly
- ✅ coreclaw-web command
  - cmd/coreclaw-web/main.go entry point
  - Per-client independent agent sessions
  - Embedded chat UI (auto-served at /)
  - WebSocket endpoint at /ws
- ✅ Fixed WebSocket cancel button only working once
  - Read loop now signals cancel via channel instead of calling cancel directly
  - Main loop listens on cancel channel and calls the current cancel function
  - This ensures cancel always works regardless of context recreation
- ✅ Tab key to switch focus between display and input windows
  - Focused window has bright border (#89d4fa), unfocused has dimmed border (#45475a)
  - When display window is focused, j/k scrolls content like vim
- ✅ TLV protocol for user-to-session communication
  - Added TagUserText='A' for user text input from client to session
  - Session reads TLV messages from input stream and unwraps TagUserText
  - Session validates tags and emits TagError for invalid ones
  - Session detects commands (starts with "/") and routes to handler
  - Session checks command errors and emits TagError to user
  - ChanInput helper in stream.go with configurable buffer size
  - Terminal uses 10-buffer for human-paced input
  - WebSocket uses 100-buffer for network-paced input
  - HTML client encodes user input as TagUserText TLV
  - Removed adaptors.NewSession - adaptors create processor/session directly
- ✅ Terminal display color persistence fix
  - Fixed dimmed text (reasoning text) losing color in two scenarios:
    1. **Scrolling**: First word loses color when line moves off-screen
    2. **Session loading**: Entire dimmed text block loses color when loading from session file
  - Root causes:
    - ANSI escape sequences only applied at beginning of text blocks via Style.Render()
    - Session loading format didn't match live streaming format (missing TagStreamGap separators)
    - Word wrapping broke ANSI escape sequences across line breaks
  - Solutions implemented:
    1. **Per-line styling**: Added `renderMultiline` helper that splits text by newlines and applies styling per line
    2. **Session format consistency**: Updated `session.go:DisplayMessages()` to match live streaming format with proper TagStreamGap separators and tool formatting
    3. **Wordwrap ANSI preservation**: Enhanced `wordwrap()` function to preserve both prefix (styling) and suffix (reset) ANSI escape sequences across line breaks
    4. **Panic fix**: Fixed slice bounds panic when line contains only escape sequences by extracting suffix from remaining text after prefix removal
  - Testing:
    - Added `TestWordwrapPreservesANSI` to verify each wrapped line starts with styling and ends with reset sequence
    - Added `TestWordwrapEdgeCases` to verify no panic with edge cases (empty lines, only escape sequences)
    - Updated `TestRenderMultiline` to verify per-line styling
    - Verified scrolling behavior with loaded sessions using real session files
- ✅ **Terminal viewport initial position fix**
  - Fixed incorrect initial display position when loading content from session files
  - **Problem**: When loading session content, viewport stayed at welcome text offset (centered) instead of scrolling to bottom of actual content
  - **Root cause**: Welcome text centering added empty lines; viewport YOffset didn't reset when session content replaced welcome
  - **Solution**: Detect existing content in display buffer during Terminal initialization; show session content immediately instead of welcome
  - **Implementation**:
    1. Added check in `NewTerminal()`: if `terminalOutput.display.GetAll()` is non-empty, set viewport content to word-wrapped session content and `GotoBottom()`
    2. Skip welcome text and centering when session content exists
    3. Added `firstScrollDone` flag to track initial scroll state
    4. Updated `updateDisplayContent()` to respect scroll state
  - **Result**: Session content now displays at correct scroll position; scrolling works smoothly without large jumps
- ✅ **Todo system with display and runtime management**
  - Implemented todo_read and todo_write tools for task planning
  - TodoManager provides thread-safe todo list management
  - Todo items have Content (description) and ActiveForm (present continuous verb form)
  - Terminal displays todos between display and input boxes
  - Display updates automatically when todos change
  - Dynamic height adjustment - viewport shrinks when todos appear
  - Status-based coloring: white (pending), green/italic (in-progress), green (completed)
  - Runtime-only - todos are preserved on /cancel, not persisted to session files
  - Updated system prompt to enforce Content field preservation when updating status
  - Content remains exactly the same when changing from pending→in_progress→completed
  - Only status field changes; Content and ActiveForm stay constant
  - **Width matching**: Todo box width now matches input box width (fixed width calculation)

- ✅ **Fixed missing top row when todo appears**
  - **Problem**: When todo box appears, one row disappears from top of display box; when todo box disappears, row returns.
  - **Root cause**: `updateDisplayHeight()` changed viewport height without adjusting YOffset appropriately.
  - **Solution**: Added YOffset adjustment based on scroll state:
    - Auto-scroll mode (`userScrolledAway = false`): keep bottom line constant
    - Manual scroll mode (`userScrolledAway = true`): keep top line constant
  - **Implementation**: Updated `updateDisplayHeight()` with proper line counting and clamping.
  - **Testing**: Added `TestMissingTopRowWhenTodoAppears`, `TestAutoScrollKeepsBottomWhenTodoAppears`, `TestTodoToggleScrollConsistency`.

- ✅ **Fixed wordwrap orphan prevention**
  - **Problem**: Last word sometimes placed on new line even when the previous line had space remaining, creating short "orphan" lines (e.g., "first.", "properly." on separate lines)
  - **Root cause**: Greedy wordwrap algorithm filled each line to maximum width without considering balance between lines, causing short final words to be isolated
  - **Solution**: Added orphan detection in `wordwrap()` function:
    - When breaking at a space would create a short segment (< 30% of width)
    - Check if the remaining text can be packed onto the current line without exceeding 120% of width
    - If so, find an earlier space to break at, or pack entire remaining segment onto current line
  - **Implementation**: Enhanced `wordwrap()` with orphan detection logic that looks for previous spaces and packs short segments together
  - **Testing**: Added `TestWordwrapOrphanPrevention` with multiple test cases to verify no short orphan lines appear

- ✅ **Upgraded to bubbletea/lipgloss/bubbles v2.x**
  - Updated go.mod with v2 versions from charm.land vanity domain:
    - charm.land/bubbletea/v2@v2.0.1
    - charm.land/lipgloss/v2@v2.0.0
    - charm.land/bubbles/v2@v2.0.0
  - Updated all imports across internal/adaptors/ (terminal.go, wordwrap.go, test files)
  - Fixed breaking API changes:
    - View() return type: `string` → `tea.View` (wrap with `tea.NewView()`)
    - KeyMsg API: changed from struct to interface with `msg.String()` for comparison
    - Key constants: removed `tea.KeyCtrlC`, use string `"ctrl+c"` instead
    - Viewport API: `Width/Height` fields → `Width()/Height()` methods
    - Viewport constructor: `viewport.New(w, h)` → `viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))`
    - textinput styles: `PromptStyle`/`TextStyle` fields removed, use `SetStyles(Styles)` method
    - Program options: `tea.WithAltScreen()` removed, set `view.AltScreen = true` in View()
    - lipgloss: `SetColorProfile()` removed (no longer needed)
  - Fixed test KeyMsg construction: `tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl})` instead of struct literal
  - Updated ANSI reset sequence test to accept both `\x1b[0m` and `\x1b[m` (equivalent in v2)
  - All tests pass, project builds successfully

### Architecture
- **Provider Types**: `anthropic` (native Anthropic API), `openai` (OpenAI-compatible)
- **Tools**: read_file, todo_read, todo_write, write_file, activate_skill, posix_shell
- **Framework**: charm.land/fantasy
- **UI Styling**: Raw ANSI escape codes (lightweight, no padding)
- **Stream Protocol**: TLV (Tag-Length-Value) for structured output
  - Session-to-user: TagAssistantText, TagTool, TagReasoning, TagError, TagSystem (JSON), TagNotify, TagStreamGap, TagPromptStart
  - User-to-session: TagUserText
  - Session validates and unwraps user TLV messages
  - TagSystem contains JSON-encoded SystemInfo struct with token usage and queue: `{"context":1234,"total":5678,"queue":2}`

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
  todo/        - Todo list management for task planning
  tools/       - Tool implementations (posix_shell, read_file, write_file, activate_skill, todo_read, todo_write)
cmd/coreclaw-web/       - coreclaw-web entry point
main.go        - coreclaw entry point
```

### Features
- Execute posix_shell commands through the AI agent
- Multi-step conversations with tool calls
- Token usage tracking
- Error handling for command execution
- CLI-based provider configuration (no env vars)
- CLI flags: --type, --base-url, --api-key, --model, --skill
- Provider types: anthropic, openai
- Color-coded output for better readability
- Command history for interactive sessions
- Direct stdin reading for terminal input
- Proper conversation history management for multi-turn tool calls
- IOStream abstraction with TLV protocol
- Web server with WebSocket support and chat UI

### Usage
```sh
# OpenAI API
./coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Anthropic API
./coreclaw --type anthropic --base-url https://api.Anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4-20250514

# Local AI server (e.g., Ollama)
./coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3

# Run with API debug
./coreclaw --debug-api --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run with custom system prompt
./coreclaw --system "You are a code reviewer" --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run interactively
./coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Run with skills
./coreclaw --skill ./skills --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Show help
./coreclaw --help
```

### coreclaw-web (WebSocket Server)
```sh
# Start WebSocket server
./coreclaw-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Custom address
./coreclaw-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4-20250514 --addr :9090

# Then open http://localhost:8080 in browser
# WebSocket endpoint: ws://localhost:8080/ws
```

## Next Steps
- Add more sophisticated skills built on posix_shell tool
