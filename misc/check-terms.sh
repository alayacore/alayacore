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

# ── 4. Wire values that have a name ────────────────────────────────────────
# A status or state string means one thing because it is written once, in
# protocol: the producer returns the constant and the adapter compares it. A
# literal comparison in an adapter is a second home for the same word, and the
# copy the producer changes is the one nobody sees. Exact quoted values only —
# a message that merely contains the word ("connecting %q") is not a comparison.
wires='"(ready|closed|starting|initializing|connecting|connected|failed|auth_required|auth_running|done|queued|delivered|dropped|refused)"'
hits=$(grep -rnE "$wires" --include=*.go internal/adapters 2>/dev/null |
	grep -vE '_test\.go:' | grep -vE ':[0-9]+:[[:space:]]*//')
report "$hits" 'a wire value spelled as a literal in an adapter — use the protocol constant'

# ── 4. One spelling for English, everywhere ───────────────────────────────
hits=$(grep -rniE '\b(behaviour|colour|summarise|organise|recognise|normalise|initialis|serialis)[a-z]*' \
	--include=*.go --include=*.md . 2>/dev/null)
report "$hits" 'American spelling: behavior, color, summarize'

if [ "$fails" -gt 0 ]; then
	printf '\n%d term violation(s). docs/vocabulary.md says what each name is.\n' "$fails" >&2
	exit 1
fi
printf 'one name per concept: retired names absent, owned words confined, one spelling.\n'
