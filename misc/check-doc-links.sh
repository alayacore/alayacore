#!/usr/bin/env bash
#
# check-doc-links.sh — assert every relative link in the Markdown still resolves
#
# Usage:
#   ./misc/check-doc-links.sh
#   make check-doc-links
#
# Why this exists: the docs are a graph — architecture.md points at
# development-principles.md, tui.md points at internal/tool-spinner-refresh.md,
# and so on — and nothing else in the build reads those edges. A page that is
# renamed, or a link typed before its target existed, leaves a 404 that only a
# human clicking it would ever find. This is the one class of doc rot a tool can
# check exactly: a relative link either resolves from the file that wrote it or
# it does not.
#
# Scope, deliberately narrow. Only relative Markdown links are checked. Absolute
# URLs are somebody else's uptime; a bare filename in prose ("see width.go") is
# not a link and is left alone, because it is as likely to name a file in an
# upstream project (Bubble Tea's tea.go) as one here — that class of reference is
# for review, not for a grep to guess at.
#
# A target is matched as ](target); one starting with a scheme or '#' is skipped,
# and any #fragment is dropped before the path is resolved against the directory
# of the file that wrote it.

set -euo pipefail

if git rev-parse --show-toplevel >/dev/null 2>&1; then
	cd "$(git rev-parse --show-toplevel)"
	files=$(git ls-files '*.md')
else
	files=$(find . -name '*.md' -not -path './.git/*')
fi

broken=$(mktemp)
trap 'rm -f "$broken"' EXIT
checked=0
nfiles=0

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
done <<<"$files"

if [ -s "$broken" ]; then
	count=$(wc -l <"$broken" | tr -d ' ')
	echo "$count relative Markdown links do not resolve from the file that wrote them:" >&2
	sed 's/^/  /' "$broken" >&2
	exit 1
fi

echo "$checked relative Markdown links across $nfiles files resolve."
