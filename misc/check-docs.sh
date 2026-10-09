#!/usr/bin/env bash
#
# check-docs.sh — assert the Markdown carries nothing the build can decide
#
# Usage:
#   ./misc/check-docs.sh
#   make check-docs
#
# Two classes. Both are exact — a claim in either holds or it does not, with
# nothing in between for a reviewer to argue about.
#
# 1. Relative links. The docs are a graph — architecture.md points at
#    development-principles.md, tui.md points at internal/tool-spinner-refresh.md
#    — and nothing else in the build reads those edges. A page that is renamed,
#    or a link typed before its target existed, leaves a 404 that only a human
#    clicking it would ever find. A relative link either resolves from the file
#    that wrote it or it does not.
#
# 2. Perishable shapes in prose: a line number into a file (`width.go:412`), an
#    approximate line count (`~180 lines`), a table column headed Lines/LOC.
#    Each is wrong the moment gofmt, a comment or a test split moves it, and none
#    is information the reader needs — it is what a command prints. See
#    development-principles.md, "Don't write what a command can tell you": a doc
#    that carried a line count had to be edited twice in one series, once to
#    record a fallback and once to correct the number the first edit invalidated.
#
# Scope, deliberately narrow. What this does NOT check, and why:
#
#   - A bare filename in prose ("see width.go"). As likely to name a file in an
#     upstream project (Bubble Tea's tea.go) as one here. That class is for
#     review, not for a grep to guess at.
#   - A symbol name in prose (`handleMessageDelta`). A sweep of the names the
#     docs mention reported 105 hits, of which ~103 are legitimate — upstream
#     library symbols, MCP spec names, Windows API constants, labels a benchmark
#     doc defines for itself. An allowlist of 100 names costs more than the drift
#     it would catch, and a check whose allowlist is the answer to every finding
#     is a check nobody keeps updated.
#   - Counts of call sites, symbols or allocations. Not decidable at all.
#   - Quoted material. Fenced blocks are blanked before the shape checks run:
#     sample output ("Output (5000 lines, 194.2KB)") and wire dumps
#     ("main.go:3:// TODO") are self-evidently samples, not claims about this
#     tree. Blanking rather than deleting keeps the reported line numbers honest.
#
# A link target is matched as ](target); one starting with a scheme or '#' is
# skipped, and any #fragment is dropped before the path is resolved against the
# directory of the file that wrote it.

set -euo pipefail

if git rev-parse --show-toplevel >/dev/null 2>&1; then
	cd "$(git rev-parse --show-toplevel)"
	files=$(git ls-files '*.md')
else
	files=$(find . -name '*.md' -not -path './.git/*')
fi

# perishable is class 2 as one extended regex: a path into a known file
# extension followed by :<digits>; a tilde count of lines or LOC; a table cell
# that is nothing but Lines, Line or LOC.
perishable='[A-Za-z0-9_./-]+\.(go|sh|md|json|conf|yaml|yml):[0-9]+|~[[:space:]]*[0-9]+[[:space:]]*(lines|LOC)|\|[[:space:]]*(Lines?|LOC)[[:space:]]*\|'

broken=$(mktemp)
shapes=$(mktemp)
prose=$(mktemp)
trap 'rm -f "$broken" "$shapes" "$prose"' EXIT
checked=0
nfiles=0

# strip_fences blanks a fenced code block, one blank line per input line, so a
# line number in the output is still the line number in the file.
strip_fences() {
	awk '
		/^[[:space:]]*```/ { fence = !fence; print ""; next }
		{ if (fence) print ""; else print }
	' "$1"
}

while IFS= read -r f; do
	[ -n "$f" ] || continue
	nfiles=$((nfiles + 1))
	dir=$(dirname "$f")

	links=$(grep -oE '\]\([^)]+\)' "$f" | sed -e 's/^](//' -e 's/)$//') || true
	while IFS= read -r target; do
		[ -n "$target" ] || continue
		case "$target" in
		http://* | https://* | mailto:* | \#*) continue ;;
		esac
		path=${target%%#*}
		[ -n "$path" ] || continue
		checked=$((checked + 1))
		if [ ! -e "$dir/$path" ]; then
			printf '%s -> %s\n' "$f" "$target" >>"$broken"
		fi
	done <<<"$links"

	strip_fences "$f" >"$prose"
	grep -nE "$perishable" "$prose" | sed "s|^|$f:|" >>"$shapes" || true
done <<<"$files"

status=0
if [ -s "$broken" ]; then
	count=$(wc -l <"$broken" | tr -d ' ')
	echo "$count relative Markdown links do not resolve from the file that wrote them:" >&2
	sed 's/^/  /' "$broken" >&2
	status=1
fi
if [ -s "$shapes" ]; then
	count=$(wc -l <"$shapes" | tr -d ' ')
	echo "$count perishable shapes in prose — a line number or a line count, which is what a command prints:" >&2
	sed 's/^/  /' "$shapes" >&2
	status=1
fi
[ "$status" -eq 0 ] || exit 1

echo "$checked relative Markdown links across $nfiles files resolve; no perishable shapes in prose."
