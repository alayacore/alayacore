#!/usr/bin/env bash
#
# check-terms.sh — assert the tree names one concept one way
#
# Usage:
#   ./misc/check-terms.sh
#   make check-terms
#
# The vocabulary is defined in docs/vocabulary.md. Every rule below is the
# mechanical half of a rule written there: a name that page retires, and a word
# it confines to the file that owns it. Each is exact — a hit is a name that
# must not exist, with nothing for a reviewer to argue about — and the file
# list is the page's own rule, not an allowlist of exceptions to it.
#
# Scope, matching check-docs.sh: this cannot tell you a *new* synonym has
# appeared. "Any two words might mean the same thing" is not decidable; the
# page and review are where a new concept gets its name.

set -u
cd "$(dirname "$0")/.."

fails=0
report() {
	[ -n "$1" ] && { printf '%s\n' "$1"; printf '  ^ %s\n' "$2"; fails=$((fails + 1)); }
}

pkg='internal/adapters/terminal'

# ── 1. Names retired by docs/vocabulary.md ─────────────────────────────────
# collapsed/expanded → folded/unfolded; the two floating "windows" → overlays;
# windowWidth/Height and WindowSizeMsg → the screen's; the three meanings of
# "cursor" → caret, windowCursor, screenCursor; the four meanings of "input" →
# prompt box, keys, session pipe, toolInput; handleSystemMsg → the frame it
# parses.
retired='\bcollaps'
retired="$retired"'|\b(Collapsed|BuildCollapsed|collapsedSummary|collapsedRow|CollapsedLabelWidth)\b'
retired="$retired"'|\b(buildExpandHeader|expandTitle|expandedCursor|Expanded|expanded)\b'
retired="$retired"'|\b(HelpWindow|AttachmentWindow|helpWindow|attachmentWindow)\b'
retired="$retired"'|\b(WindowSizeMsg|SetWindowWidth|handleWindowSize|windowWidth|windowHeight)\b'
retired="$retired"'|\b(InputParser|streamInput|readInput|pauseInput|stopInput|resumeInput|newInput|inputSource)\b'
retired="$retired"'|\b(focusInput|handleInputKeys|focusInputWithValueMsg)\b'
retired="$retired"'|\b(handleSystemMsg)\b'
retired="$retired"'|\b(CursorCell|CursorEnd|CursorPos|WithCursorPos|ensureCursorVisible|cursorCell)\b'
retired="$retired"'|\b(CursorShape|CursorBlock|CursorUnderline|CursorBar|NewCursor|newCursor|cursorsEqual|lastCursor|sameCursor)\b'
retired="$retired"'|\b(ValidateCursor|ClampCursor|EnsureCursorVisible|setCursor|clearCursor|anchorCursorWindow)\b'
retired="$retired"'|\b(ScrollCursorToTop|WithCursorToLastWindow|GetCursorWindowContent|GetCursorWindowHistoryID)\b'
hits=$(grep -rnE "$retired" --include=*.go "$pkg" 2>/dev/null)
report "$hits" 'a name retired by docs/vocabulary.md — use its replacement'

# ── 2. New declarations of the owned words, outside the files that own them ─
# screen.go/program.go own the terminal's cursor, display.go/window.go/
# window_buffer.go the window cursor, input_field.go/prompt_input.go the caret.
# A *declaration* somewhere else is the failure this catches — prose is out of
# reach of a grep, which is why this class is declarations only.
decls=$(grep -rnE '^[[:space:]]*(cursor|caret)[[:space:]]+[*\[]?[A-Za-z]|\)[[:space:]]+(cursor|caret)\(' \
	--include=*.go "$pkg" 2>/dev/null)
hits=$(printf '%s\n' "$decls" |
	grep -vE '^'"$pkg"'/(screen|program|display|window|window_buffer|console_windows|input_field|prompt_input)\.go:' |
	grep -vE '^'"$pkg"'/[a-z0-9_]*_test\.go:' |
	grep -v '^$')
report "$hits" 'a declaration using a word another file owns — say windowCursor, caret or screenCursor'

# ── 3. The retired word for "a run" ─────────────────────────────────────────
# A run is a task; what used to be spelled "turn" is task plus promptKind.
# The verb and the idioms ("turn it off", "turn a request into", "in turn",
# "turns out") are English, not the term, so only the noun signatures are
# banned — an article before it, a possessive, the compounds, and the phrase
# tails no verb would take. `pause_turn`/`end_turn` are Anthropic's field
# names and do not match.
turnnoun='\b(mid|multi|per|three|two)-turn\b|\b(assistant|user) turns?\b|\buserTurn\b|\bturn'"'"'s\b|\b(the|a|an|this|that|next|each|per|one|any|every|same|previous|new|own|first|last|whole|single|both|its) turns?\b|\bturns? (ended|never|boundary|is|was|starts|has|already|splices|spliced|takes|whose)\b'
hits=$(grep -rnE "$turnnoun" --include=*.go --include=*.md . 2>/dev/null)
report "$hits" 'a run is a task — say the kind (userPrompt/summarizeCall) when it matters'

# ── 3. Docs, for the states whose two names this page settles ──────────────
hits=$(grep -rniE '\bcollaps' docs/tui.md docs/internal/virtual-rendering-performance.md 2>/dev/null)
report "$hits" 'the folded state is folded/unfolded'

hits=$(grep -rnE '\b(expanded|Expanded)\b' docs/tui.md docs/internal/virtual-rendering-performance.md 2>/dev/null)
report "$hits" 'the folded state is folded/unfolded'

# ── 4. A usage line is rendered, not spelled ───────────────────────────────
# commands.Defs owns a command's argument shape and commands.UsageLine renders
# it; a handler that wrote its own "usage: :name <args>" drifted from the table
# (:tool_confirm <id> against a <call_id>, :model_sync <json> against <content>).
# The renderer itself lives in internal/commands and is the one allowed spelling.
hits=$(grep -rn '"usage: :' --include=*.go internal/agent internal/adapters 2>/dev/null)
report "$hits" 'a usage line spelled in a handler — render it with commands.UsageLine'

# ── 4. Wire values that have a name ────────────────────────────────────────
# A status or state string means one thing because it is written once, in
# protocol: the producer returns the constant and the adapter compares it. A
# literal comparison in an adapter is a second home for the same word, and the
# copy the producer changes is the one nobody sees. Exact quoted values only —
# a message that merely contains the word ("connecting %q") is not a comparison.
wires='"(ready|closed|starting|initializing|connecting|connected|failed|auth_required|auth_running|done|queued|delivered|dropped|refused|canceled)"'
hits=$(grep -rnE "$wires" --include=*.go internal/adapters 2>/dev/null |
	grep -vE '_test\.go:' | grep -vE ':[0-9]+:[[:space:]]*//')
report "$hits" 'a wire value spelled as a literal in an adapter — use the protocol constant'

# ── 5. …and where the value is produced ───────────────────────────────────
# The rule is not about adapters; it is about any code that names a wire value.
# Five literals lived in the session and the MCP client — one of them a `case`
# for a value that never reaches the wire — because this scan stopped at
# internal/adapters. Only a *use* matches here (a case label, a comparison, a
# field or map value): a definition is where the literal belongs, so
# `InitCanceled = "canceled"` passes and `case "canceled"` does not.
wire_use="(case[[:space:]]+|==[[:space:]]*|!=[[:space:]]*|:[[:space:]]*)"$wires
hits=$(grep -rnE "$wire_use" --include=*.go internal/agent internal/mcp 2>/dev/null |
	grep -vE '_test\.go:' | grep -vE ':[0-9]+:[[:space:]]*//')
report "$hits" 'a wire value spelled as a literal outside an adapter — use the constant'

# ── 5. Retired file names ──────────────────────────────────────────────────
# A rename in the vocabulary pass (0d1134ce) moved program_input*.go to
# program_keys*.go, attachment_window.go to attachment_overlay.go and
# help_window.go to help_overlay.go, and left 39 mentions of the old names in
# comments and docs — the files they named no longer existed, and nothing
# noticed, because a comment is not a link. check-docs.sh cannot see these
# (`internal/adapters/terminal`'s own comments are not prose), so the names are
# retired here, and the next rename adds its old name to this list.
retired_files='\b(program_input[a-z_]*\.go|attachment_window\.go|help_window\.go|cursor_probe_test\.go)\b'
hits=$(grep -rnE "$retired_files" --include=*.go . 2>/dev/null | grep -v '^\./misc/')
report "$hits" 'a file name that no longer exists — say what it is called now'

# ── 6. Retired CO error codes ──────────────────────────────────────────────
# Protocol v17 replaced twenty codes with eighteen, in five classes named by what
# a caller can do about them. docs/commands.md's table is checked against
# protocol.CmdErrorCodes by TestCommandsDocErrorCodesMatchTheProtocol; this is the
# copy that test cannot see — a literal in Go, and a name in prose anywhere else.
# `ERROR` is left out: it is also a UI label ("SYSTEM ERROR"), so a word grep
# cannot tell the code from the label. Its successor, INTERNAL_UNCLASSIFIED, is
# in the list, which is what a reintroduction of the old fallback would have to
# spell.
old_codes='INVALID_ARGS|UNKNOWN_COMMAND|NOT_FOUND|MODEL_NOT_FOUND|INVALID_STATE|NOT_CONFIGURED|MODEL_VALIDATION|MODEL_ERROR|UNAVAILABLE|BUSY|SHUTTING_DOWN|MCP_NOT_READY|NOT_IN_PROGRESS|NOTHING_TO_CANCEL|LOAD_FAILED|THEME_ERROR|IO_ERROR|PERSIST_FAILED|NOT_INITIALIZED|UNCLASSIFIED|BAD_FRAME'
hits=$(grep -rnE "\"($old_codes)\"" --include=*.go . 2>/dev/null | grep -v '^\./misc/')
report "$hits" 'a retired CO error code spelled as a literal — use protocol.ErrCode*'
hits=$(grep -rnE "\b($old_codes)\b" --include=*.md docs adapter-guide README.md README.zh-CN.md 2>/dev/null)
report "$hits" 'a retired CO error code in prose — docs/commands.md has the table'

# ── 6. One spelling for English, everywhere ───────────────────────────────
hits=$(grep -rniE '\b(behaviour|colour|summarise|organise|recognise|normalise|initialis|serialis)[a-z]*' \
	--include=*.go --include=*.md . 2>/dev/null)
report "$hits" 'American spelling: behavior, color, summarize'

if [ "$fails" -gt 0 ]; then
	printf '\n%d term violation(s). docs/vocabulary.md says what each name is.\n' "$fails" >&2
	exit 1
fi
printf 'one name per concept: retired names absent, owned words confined, one spelling.\n'
