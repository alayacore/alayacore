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
# Each class below is exact — a claim in any of them holds or it does not, with
# nothing in between for a reviewer to argue about. The list is not counted: a
# number here is one more thing to update for a class that was already described.
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
# 4. File names. A doc that names a file is asserting it exists: "the input loop
#    and the parking protocol (`program_input.go`)" was true until the file
#    became program_keys.go, and then the doc pointed at nothing — the same
#    rename left the name in `internal/adapters/terminal`'s own comments, where a
#    link check cannot see it (`check-terms.sh` retires those, because a Go
#    comment is not prose a checker can resolve). Any
#    `*.go`/`*.sh` in a code span on a non-fenced line must be a basename in the
#    tree, or a path that resolves from the doc's directory or the repo root.
#
# 5. Flags. A doc that names --terseio is asserting the parser answers to it.
#    --model-config, --runtime-config and --themes were retired when the four
#    config files moved under one --config-path, and configuration.md went on
#    listing all three in a migration note: a reader who copies one gets a usage
#    error and no config directory. The set is derived, not maintained — every
#    flag config.Parse registers, read out of the source — so a flag added there
#    is docable the same day and a flag removed there is a finding the same day.
#
# Scope, deliberately narrow. What this does NOT check, and why:
#
#   - A bare filename in prose ("see width.go" without backticks). As likely to
#     name a file in an upstream project (Bubble Tea's tea.go) as one here. Class
#     4 covers the names a doc puts in a code span, which is how this tree writes
#     one it means; the unquoted mention is for review.
#   - A symbol name in prose (`handleMessageDelta`). A sweep of the names the
#     docs mention reported 105 hits, of which ~103 are legitimate — upstream
#     library symbols, MCP spec names, Windows API constants, labels a benchmark
#     doc defines for itself. An allowlist of 100 names costs more than the drift
#     it would catch, and a check whose allowlist is the answer to every finding
#     is a check nobody keeps updated.
#   - A flag belonging to another program, in a fenced block that does not name
#     `alayacore`. Class 5 reads un-fenced lines (a `--name` in prose is this
#     tree's by the same convention class 4 uses for file names) and the fenced
#     blocks that are examples of running `alayacore`; a block showing `rg --json`
#     is that program's, and a flag retired inside such a block would be missed.
#   - Counts of call sites, symbols or allocations. Not decidable at all.
#   - Quoted material. Fenced blocks are blanked before the shape checks run:
#     sample output ("Output (5000 lines, 194.2KB)") and wire dumps
#     ("main.go:3:// TODO") are self-evidently samples, not claims about this
#     tree. Blanking rather than deleting keeps the reported line numbers honest.
#   - Setext headings (a title underlined with === or ---), and headings anchored
#     by an explicit HTML id. Neither is used in this repo; a doc that adopts one
#     would need misc/check-doc-anchors.go extended, and until then its links
#     would be reported as broken rather than passed over.
#   - A file name that only a *test fixture* names, or one belonging to another
#     project: `a.go` and `script.sh` are created by the tests that mention them,
#     and `tea.go`/`tty.go` are Bubble Tea's. Class 4 checks the names a doc
#     writes, where the convention is that the file is this repo's; `hello.go`
#     in the adapter guide is the one documented exception (a user's program).
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
stale=$(mktemp)
badflags=$(mktemp)
flaglines=$(mktemp)
namelist=$(mktemp)
treepaths=$(mktemp)
slashpaths=$(mktemp)
work=$(mktemp -d)
trap 'rm -f "$broken" "$shapes" "$prose" "$fragments" "$fragcount" "$stale" "$namelist" "$treepaths" "$slashpaths" "$badflags" "$flaglines"; rm -rf "$work"' EXIT
checked=0
anchors_checked=0
anchors_built=1
names_checked=0
flags_checked=0
flags_derived=1
nfiles=0

# report_name records the name in $name as missing from $f.
report_name() { printf '%s -> %s\n' "$f" "$name" >>"$stale"; }

# Two indexes for class 4: every basename in the tree (a bare name is a claim
# about this repo), and every full path with a leading slash (a path a doc writes
# may start anywhere — `llm/types.go` means internal/llm/types.go — so it passes
# when it ends a real path).
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	git ls-files >"$treepaths"
else
	find . -type f -not -path './.git/*' | sed 's|^\./||' >"$treepaths"
fi
sed 's|.*/||' "$treepaths" | sort -u >"$namelist"
sed 's|^|/|' "$treepaths" | sort -u >"$slashpaths"

# What opens a fence, in one place: strip_fences and flag_lines both have to agree
# on it, and two copies of a rule is how a fix to one leaves the other reading the
# docs differently.
fence_open='^[[:space:]]*```'

# strip_fences blanks a fenced code block, one blank line per input line, so a
# line number in the output is still the line number in the file.
strip_fences() {
	awk -v open="$fence_open" '
		$0 ~ open { fence = !fence; print ""; next }
		{ if (fence) print ""; else print }
	' "$1"
}

# Class 5 is class 4's other half. The set comes from the program: misc/
# check-doc-flags.go asks internal/config, which registers the flags, so a page is
# checked against the list the command line answers to. Nothing here reads Go
# source — a regex at that job missed fs.Duration and fs.BoolVar, and derived "a"
# from fs.Var(makeList("a"), "tags").
flags_derived=0
if ! command -v go >/dev/null 2>&1; then
	echo "no Go toolchain on PATH — flags were not checked" >&2
elif ! go build -o "$work/check-doc-flags" ./misc/check-doc-flags.go; then
	echo "misc/check-doc-flags.go did not build — flags were not checked" >&2
elif flags=$("$work/check-doc-flags") && [ -n "$flags" ]; then
	flags_derived=1
else
	echo "misc/check-doc-flags.go named no flags — flags were not checked" >&2
fi
# --help is the flag package's own, answered whether or not this tree registers it.
allowed=" $(printf '%s\n' "$flags" | tr '\n' ' ')help "

# Keep a link target out of the scan: GitHub slugs "Steering — a prompt" to
# #steering--a-prompt-…, whose two hyphens are an em dash, not a flag.
strip_targets() { sed -E 's/\]\([^)]*\)//g'; }

# flag_lines prints the lines a flag mention is read from, numbered: every
# un-fenced line, and every line of a fenced block that names alayacore (an
# example of running it, continuations included).
flag_lines() {
	awk -v open="$fence_open" '
		$0 ~ open {
			if (fenced) { if (names) for (i = 1; i <= n; i++) print buf[i]; fenced = 0 }
			else { fenced = 1 }
			n = 0; names = 0; next
		}
		fenced { n++; buf[n] = NR ":" $0; if ($0 ~ /alayacore/) names = 1; next }
		{ print NR ":" $0 }
		END { if (fenced && names) for (i = 1; i <= n; i++) print buf[i] }
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

	spans=$(grep -oE '`[^`]*\.(go|sh)`' "$prose" | tr -d '`') || true
	while IFS= read -r name; do
		[ -n "$name" ] || continue
		case "$name" in
		*'*'* | ./*) continue ;; # a glob, or a path inside an example project
		hello.go) continue ;;    # the adapter guide's example program
		esac
		names_checked=$((names_checked + 1))
		case "$name" in
		*/*) grep -Fq "/$name" "$slashpaths" || report_name ;;
		*) grep -Fxq "$name" "$namelist" || report_name ;;
		esac
	done <<<"$spans"

	# grep says 1 for "no mention", which is a clean file, and 2 for a failure,
	# which is not: only the second one may pass for a result.
	flag_scan_status=0
	{ flag_lines "$f" | strip_targets | grep -E -e '--[a-z][a-z0-9-]*'; } >"$flaglines" ||
		flag_scan_status=$?
	if [ "$flag_scan_status" -gt 1 ]; then
		echo "the flag scan failed on $f (status $flag_scan_status) — flags were not checked" >&2
		flags_derived=0
	fi
	while IFS= read -r entry; do
		[ -n "$entry" ] || continue
		text=${entry#*:}
		for flag in $(printf '%s\n' "$text" | grep -oE '\-\-[a-z][a-z0-9-]*' || true); do
			flags_checked=$((flags_checked + 1))
			# allowed holds names without their hyphens, as config registers them.
			case "$allowed" in
			*" ${flag#--} "*) continue ;;
			esac
			printf '%s:%s\n' "$f" "$entry" >>"$badflags"
			break
		done
	done <"$flaglines"
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
if [ -s "$stale" ]; then
	count=$(wc -l <"$stale" | tr -d ' ')
	echo "$count file names in a code span are not files in this tree:" >&2
	sed 's/^/  /' "$stale" >&2
	status=1
fi
# A failed derivation already said so; listing every mention as a finding on top
# of it would bury the one line that matters.
if [ "$flags_derived" -eq 1 ] && [ -s "$badflags" ]; then
	count=$(wc -l <"$badflags" | tr -d ' ')
	echo "$count flags in the docs are not flags this program answers to (config.FlagNames registers them):" >&2
	sed 's/^/  /' "$badflags" >&2
	status=1
fi
if [ -s "$shapes" ]; then
	count=$(wc -l <"$shapes" | tr -d ' ')
	echo "$count perishable shapes in prose — a line number or a line count, which is what a command prints:" >&2
	sed 's/^/  /' "$shapes" >&2
	status=1
fi
[ "$anchors_built" -eq 1 ] || status=1
[ "$flags_derived" -eq 1 ] || status=1
[ "$status" -eq 0 ] || exit 1

echo "$checked relative Markdown links, $anchors_checked heading anchors, $names_checked file names and $flags_checked flag mentions across $nfiles files resolve; no perishable shapes in prose."
