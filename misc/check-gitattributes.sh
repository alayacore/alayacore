#!/usr/bin/env bash
#
# check-gitattributes.sh — assert that .gitattributes still does what it says
#
# Usage:
#   ./misc/check-gitattributes.sh
#   make check-gitattributes
#
# Why this exists: Git for Windows installs with core.autocrlf=true, which
# smudges an LF-committed file to CRLF on checkout, and gofmt — which parses CRLF
# happily and prints LF — then calls every file in the tree unformatted. That is
# how the Windows CI job once reported 144 findings across the terminal adapter,
# each at line 1, none of them about the code. .gitattributes is the fix, and one
# line of it is load-bearing.
#
# Two findings, chosen because no existing tool reports them:
#
#   1. every tracked text path still resolves to eol=lf
#      The only check that notices a weakened or deleted .gitattributes on Linux,
#      where the checkout is LF either way and so nothing else is any wiser. The
#      next Windows build is where it surfaces, as 144 findings again.
#   2. every binary is binary by declaration, not by git's guess at content
#      `text=auto` peeks at bytes. For prose that is a fair bet; for the TLV
#      recordings under adapter-guide it is one heuristic away from a rewrite,
#      and gofmt will never say anything because it does not read .go-less files.
#
# What this deliberately does not check, with the reason, so nobody adds it back:
#
#   - CR in a checkout, or in a stored blob. gofmt reports both, loudly, and on
#     every platform: eol=lf means "same endings as the index", so a blob
#     committed with CRLF checks out with CRLF and fails the lint on Linux too.
#     A loud problem stays loud on purpose.
#   - whether a declared binary's bytes match its blob. When the declaration is
#     right, `git status` compares those bytes without any filter in the way and
#     sees a difference immediately; when it is wrong, finding 2 fires first. So
#     the third check this script used to carry could only ever repeat one of the
#     other two — mutation-tested, and it never had a case of its own.
#
# Paths are read newline-separated, so a filename containing a newline is not
# supported. Fixtures with such names would be a worse idea than the bug here.

set -euo pipefail

# A tree with no git — a release tarball, a module-copy in the module cache — has
# no checkout for these rules to have acted on, so the answer is "nothing to
# check", not a failure in a build that is otherwise fine.
if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "not a git checkout; .gitattributes has had no effect on these files" >&2
  exit 0
fi
cd "$(git rev-parse --show-toplevel)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# One read of git, as <path>\t<i-column>\t<matched attributes>. Records look like
# "i/lf    w/lf    attr/text eol=lf \tpath"; the attribute column can hold more
# than one word and is padded, so its tail is rejoined with "attr/" stripped from
# the first token only.
git ls-files --eol |
  awk -F'\t' '
    {
      n = split($1, c, " ")
      attrs = ""
      for (k = 3; k <= n; k++) attrs = attrs (k > 3 ? " " : "") (k == 3 ? substr(c[k], 6) : c[k])
      print $2 "\t" c[1] "\t" attrs
    }' >"$tmp/eol.tsv"

fail=0
report() { # heading, file of findings
  local heading=$1 file=$2 count
  count=$(wc -l <"$file" | tr -d ' ')
  if [ "$count" = "0" ]; then return 0; fi
  fail=1
  echo "$count $heading" >&2
  head -10 "$file" | sed 's/^/  /' >&2
  if [ "$count" -gt 10 ]; then
    echo "  ... and $((count - 10)) more; the count is the finding, not the list" >&2
  fi
  echo >&2
}

# 1. Without eol=lf the working copy follows core.autocrlf, whose default on
#    Windows is true — which is the whole incident. Naming `text` is not enough:
#    `* text=auto` alone normalizes the index and still hands over a CRLF tree.
awk -F'\t' '$2 != "i/-text" && $3 !~ /eol=lf/ {
          print $1 "\tattr/" ($3 == "" ? "(nothing matched: is .gitattributes gone?)" : $3)
        }' "$tmp/eol.tsv" >"$tmp/eol"
report "tracked text files that no rule resolves to eol=lf, so their checkout obeys core.autocrlf (true on Windows, where gofmt then calls every file unformatted). In .gitattributes the last match wins per attribute — look for a later rule that replaces the one setting eol=lf:" "$tmp/eol"

# 2. Anything git considers binary (i/-text) has to say so by name.
awk -F'\t' '$2 == "i/-text" && $3 !~ /(^| )-text/ {
          print $1 "\tattr/" ($3 == "" ? "(nothing matched)" : $3)
        }' "$tmp/eol.tsv" >"$tmp/undeclared"
report "binary files that are binary only because git guessed — name the extension instead, as \`*.<ext> binary -eol\`:" "$tmp/undeclared"

if [ "$fail" = 0 ]; then
  count=$(wc -l <"$tmp/eol.tsv" | tr -d ' ')
  binaries=$(awk -F'\t' '$2 == "i/-text"' "$tmp/eol.tsv" | wc -l | tr -d ' ')
  echo "$count tracked files: eol=lf wherever text is declared, and $binaries binaries declared by name."
fi
exit "$fail"
