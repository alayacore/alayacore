#!/usr/bin/env bash
#
# check-docs.sh — assert the Markdown carries nothing the build can decide
#
# Usage:
#   ./misc/check-docs.sh
#   make check-docs
#
# Class 3 needs a Go toolchain (misc/check-doc-anchors.go). Without one the
# script says so and fails, rather than reporting a pass over a check it did not
# run.
#
# Three classes. Each is exact — a claim in any of them holds or it does not,
# with nothing in between for a reviewer to argue about.
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
# 3. Heading anchors. A relative link has two ends, and only one of them was
#    read: `](docs/tui.md#windows-consoles)` was checked for `docs/tui.md` and
#    not for `#windows-consoles`. Retitling a section therefore broke every link
#    to it in silence — exactly how tool-execution.md's "Same-File Writes Are
#    Serialized" became "Writes Exclude All Access, and Order Nothing", leaving
#    the link in the same file pointing at nothing. The transform from a heading
#    to an anchor is GitHub's, and the half that computes it lives in
#    misc/check-doc-anchors.go, because it is a Unicode transform (README.zh-CN.md
#    links to (#模式)) and this script is byte-oriented shell.
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
#   - Setext headings (a title underlined with === or ---), and headings anchored
#     by an explicit HTML id. Neither is used in this repo; a doc that adopts one
#     would need misc/check-doc-anchors.go extended, and until then its links
#     would be reported as broken rather than passed over.
#
# A link target is matched as ](target); one starting with a scheme or '#' is
# skipped as a path, and any #fragment is dropped before the path is resolved
# against the directory of the file that wrote it. The fragment, when there is
# one, is then checked against the target's headings.

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
fragments=$(mktemp)
fragcount=$(mktemp)
work=$(mktemp -d)
trap 'rm -f "$broken" "$shapes" "$prose" "$fragments" "$fragcount"; rm -rf "$work"' EXIT
checked=0
anchors_checked=0
anchors_built=1
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

# Class 3. One program for the whole tree rather than one pass per file: a page
# linked from twenty others is slugged once, not twenty times.
if command -v go >/dev/null 2>&1; then
	if go build -o "$work/check-doc-anchors" ./misc/check-doc-anchors.go; then
		printf '%s\n' "$files" | "$work/check-doc-anchors" -report "$fragcount" >"$fragments" || true
		anchors_checked=$(tr -d '[:space:]' <"$fragcount" 2>/dev/null || true)
		anchors_checked=${anchors_checked:-0}
	else
		echo "misc/check-doc-anchors.go did not build — heading anchors were not checked" >&2
		anchors_built=0
	fi
else
	echo "no Go toolchain on PATH — heading anchors were not checked" >&2
	anchors_built=0
fi

status=0
if [ -s "$broken" ]; then
	count=$(wc -l <"$broken" | tr -d ' ')
	echo "$count relative Markdown links do not resolve from the file that wrote them:" >&2
	sed 's/^/  /' "$broken" >&2
	status=1
fi
if [ -s "$fragments" ]; then
	count=$(wc -l <"$fragments" | tr -d ' ')
	echo "$count fragment links name a heading that does not exist in the file they point at:" >&2
	sed 's/^/  /' "$fragments" >&2
	status=1
fi
if [ -s "$shapes" ]; then
	count=$(wc -l <"$shapes" | tr -d ' ')
	echo "$count perishable shapes in prose — a line number or a line count, which is what a command prints:" >&2
	sed 's/^/  /' "$shapes" >&2
	status=1
fi
[ "$anchors_built" -eq 1 ] || status=1
[ "$status" -eq 0 ] || exit 1

echo "$checked relative Markdown links and $anchors_checked heading anchors across $nfiles files resolve; no perishable shapes in prose."
