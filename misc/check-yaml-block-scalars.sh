#!/usr/bin/env bash
#
# check-yaml-block-scalars.sh — re-verify our block scalars against yaml.v3
#
# Usage:
#   ./misc/check-yaml-block-scalars.sh
#   make check-yaml-block-scalars
#
# Why this exists: docs/skills.md claims a block scalar follows YAML — chomping
# (`|`, `|-`, `|+` and the folded spelling of each), the indentation indicator
# (`|2`), and a line indented deeper than its block. The claim rests on a
# measurement, and a measurement written only in a comment is one nobody can
# re-run: the first pass over these cases was a scratch module typed by hand and
# thrown away. This is that module, kept, so the next change to the reader's
# block-scalar path can be checked against YAML in one command.
#
# What it is not: part of `make check`. It needs yaml.v3, which is deliberately
# not a dependency of this module — see docs/dependencies.md, and the reader's own
# doc comment for why the general parser was dropped. The oracle is therefore
# built in a scratch module outside the tree, which also keeps the removed
# dependency from creeping back in through the test path. That is what it costs:
# a cold module cache with no network cannot run this.
#
# The cases below are the ones this reader claims to read *exactly* as YAML does.
# Where it departs on purpose — a plain value holding a colon, a ` #` kept, a TAB
# indent, an explicit indentation the content does not reach — is not here: those
# would differ by design, and docs/skills.md's table and manifest_test.go pin them
# instead.
#
# The reading is by value, not by error: a case that errors on either side is a
# diff, and a case that makes our reader say anything on stderr is a failure, not
# a difference — these are the cases it claims to read cleanly.

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# label<TAB>frontmatter, with \n and \t spelled out so a case stays one line.
# Each vector is read as a description; the wrapper adds a name and a body.
cat >"$tmp/cases.tsv" <<'CASES'
folded, two lines	description: >\n  one\n  two
folded, strip	description: >-\n  one\n  two
folded, keep a trailing blank	description: >+\n  one\n  two\n
folded, a blank between lines	description: >\n  one\n\n  two
folded, a deeper line	description: >\n  one\n   deeper\n  two
folded, two deeper lines	description: >\n  one\n   d1\n   d2\n  two
folded, two blanks between lines	description: >\n  one\n\n\n  two
literal, two lines	description: |\n  one\n  two
literal, strip	description: |-\n  one\n  two
literal, keep a trailing blank	description: |+\n  one\n  two\n
literal, a deeper line	description: |\n  one\n   deeper\n  two
literal, explicit indent 2	description: |2\n  one\n  two
folded, a comment after the header	description: > # note\n  one
literal, keep two trailing blanks	description: |+\n  one\n  two\n\n
folded, a blank then a deeper line	description: >\n  one\n\n   deeper\n  two
literal, a leading blank	description: |\n\n  one
folded, a leading blank	description: >\n\n  one
folded, a deeper line then a blank	description: >\n  one\n   deeper\n\n  two
CASES

# 1. yaml.v3's reading of each case, in a module of its own. The version is
#    pinned here, so a difference is this reader's or a deliberate move to
#    another YAML release — never an accident of whatever is on the machine.
mkdir -p "$tmp/oracle"
cat >"$tmp/oracle/go.mod" <<'EOF'
module yamlblockdiff

go 1.21

require gopkg.in/yaml.v3 v3.0.1
EOF
cat >"$tmp/oracle/main.go" <<'EOF'
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label, inner, ok := strings.Cut(line, "\t")
		if !ok {
			fmt.Fprintf(os.Stderr, "case without a label: %q\n", line)
			os.Exit(2)
		}
		var m meta
		if err := yaml.Unmarshal([]byte("name: x\n"+unescape(inner)+"\n"), &m); err != nil {
			fmt.Printf("%s\tERR: %s\n", label, strings.ReplaceAll(err.Error(), "\n", " / "))
			continue
		}
		fmt.Printf("%s\t%q\n", label, m.Description)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cases: %v\n", err)
		os.Exit(2)
	}
}

func unescape(s string) string {
	s = strings.ReplaceAll(s, `\t`, "\t")
	return strings.ReplaceAll(s, `\n`, "\n")
}
EOF

if ! (cd "$tmp/oracle" && GOFLAGS=-mod=mod go run . <"$tmp/cases.tsv" >"$tmp/yaml.txt" 2>"$tmp/oracle.err"); then
	echo "could not run the yaml.v3 oracle — it needs the module on disk or the network:" >&2
	sed 's/^/  /' "$tmp/oracle.err" >&2
	exit 2
fi

# 2. Our reading, from the package under test.
if ! (cd "$root" && go run ./misc/yaml-block-scalars-ours.go <"$tmp/cases.tsv" >"$tmp/ours.txt" 2>"$tmp/ours.err"); then
	echo "our half did not run:" >&2
	sed 's/^/  /' "$tmp/ours.err" >&2
	exit 2
fi

# 3. A case that makes the reader report something is a failure of the claim, not
#    a difference between two readings: the whole point of the list is that these
#    read cleanly.
if [ -s "$tmp/ours.err" ]; then
	echo "the reader had something to report on cases it claims to read as YAML does:" >&2
	sed 's/^/  /' "$tmp/ours.err" >&2
	exit 1
fi

cases=$(grep -cv '^#\|^$' "$tmp/cases.tsv" || true)
if ! diff -u --label yaml.v3 --label ours "$tmp/yaml.txt" "$tmp/ours.txt" >"$tmp/diff.txt"; then
	echo "these block scalars no longer read the way yaml.v3 reads them:" >&2
	sed 's/^/  /' "$tmp/diff.txt" >&2
	exit 1
fi

echo "$cases block scalar cases read exactly as yaml.v3 v3.0.1 reads them."
