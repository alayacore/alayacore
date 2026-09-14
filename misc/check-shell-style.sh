#!/usr/bin/env bash
#
# check-shell-style.sh — assert every shell script here indents with tabs
#
# Usage:
#   ./misc/check-shell-style.sh
#   make check-shell-style
#
# Why this exists: docs/development-principles.md → "Code Style" says a shell
# script indents with tabs, like Go. Nothing else notices when one does not —
# two-space indentation is the shell world's convention and most editors default
# *.sh to it, so the rule is one editor default away from drifting into the next
# script. Review catches that late and inconsistently; this catches it in a
# second. (.editorconfig hands the same rule to editors, so the drift is usually
# prevented rather than reported.)
#
# The rule, exactly: the leading whitespace of a line — the indent — is tabs and
# nothing else. Shell here has no column-aligned construct (no struct tags, no
# comment tables), so a continuation indents like any other line, and a space
# inside an indent is always a mis-indent. Alignment INSIDE a line — the column a
# trailing comment starts at — is spaces, and is not this check's business.
#
# What this deliberately does not check, so nobody adds it back:
#
#   - trailing whitespace, or a whitespace-only line. That is a different
#     complaint about a different thing (the indent of a blank line is not read
#     by anyone), and folding it in here would make one finding mean two.
#   - tabs inside a line, or the width a tab is displayed at. A tab is a tab;
#     how wide one is drawn is the reader's own setting, and .editorconfig
#     deliberately says nothing about it.
#   - shell quoted inside markdown fences. A fence's `#!/bin/sh` is prose, and
#     prose indents with spaces; the shebang this script consults is the FIRST
#     line of a file, which a fence cannot be.
#
# Scope: tracked files named *.sh, plus any tracked file whose first line is a
# shell shebang — a script that loses the extension is still a script. Untracked
# files are not checked, the same rule check-gitattributes.sh follows and for the
# same reason: this is about what the repo carries.

set -euo pipefail

# A tree with no git — a release tarball, a module-copy in the module cache — has
# no tracked files to check.
if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	echo "not a git checkout; no tracked scripts to check" >&2
	exit 0
fi
cd "$(git rev-parse --show-toplevel)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The candidates: by extension, plus every tracked file that mentions a shell
# shebang anywhere (one pass over the index, so this stays cheap).
{
	git ls-files '*.sh'
	git grep -lE '^#!.*[[:space:]/](sh|bash|dash|ksh)([[:space:]]|$)' -- . || true
} | sort -u >"$tmp/candidates"

: >"$tmp/scripts"
while IFS= read -r f; do
	[ -f "$f" ] || continue
	case "$f" in
	*.sh) ;; # by extension
	*)
		# By shebang, but only when it is the file's first line (see above).
		head -1 "$f" | grep -qE '^#![[:space:]]*.*[[:space:]/](sh|bash|dash|ksh)([[:space:]]|$)' || continue
		;;
	esac
	echo "$f" >>"$tmp/scripts"
done <"$tmp/candidates"

# One finding per line: the file, the line number, and the indent as it stands,
# with tabs spelled out (portable awk has no %q, and a tab spelled as \t reads
# the same in every terminal).
while IFS= read -r f; do
	awk -v f="$f" '
		/^[ \t]*$/ { next }              # whitespace-only: not an indent
		/^[ \t]* / {
			indent = $0
			sub(/[^ \t].*$/, "", indent)
			gsub(/\t/, "\\t", indent)
			printf "%s:%d\tindent \"%s\"\n", f, FNR, indent
		}' "$f" >>"$tmp/findings"
done <"$tmp/scripts"

count=$(wc -l <"$tmp/findings" | tr -d ' ')
scripts=$(wc -l <"$tmp/scripts" | tr -d ' ')
if [ "$count" != "0" ]; then
	echo "$count shell lines whose indent is not tabs-only. docs/development-principles.md → Code Style: a shell indent is tabs and nothing else, so a continuation indents like any other line. A leading space is usually the editor's *.sh default, which .editorconfig overrides:" >&2
	head -10 "$tmp/findings" | sed 's/^/  /' >&2
	if [ "$count" -gt 10 ]; then
		echo "  ... and $((count - 10)) more; the count is the finding, not the list" >&2
	fi
	exit 1
fi
echo "$scripts shell scripts: indents are tabs."
