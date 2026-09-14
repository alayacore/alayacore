#!/usr/bin/env bash
#
# run-golangci-lint.sh — run the pinned golangci-lint (installed copy first)
#
# Usage:
#   ./misc/run-golangci-lint.sh
#   make lint
#
# Why this exists: two things must both be true of the linter that judges this
# tree, and neither is true of "whatever golangci-lint happens to be on PATH".
#
#   1. The VERSION is the one CI pins. It lives in one file —
#      .golangci-lint-version, read here and by both CI jobs — so it cannot
#      drift between the Makefile and the workflow. @latest would let a check
#      added in someone else's release decide whether this build is green, for
#      code nobody touched.
#
#   2. The TOOLCHAIN that built it is not older than go.mod declares. v1.64.8 is
#      the last v1 release, so its published binary is frozen at the Go that
#      built it (1.24) while this module declares a newer one — and such a
#      binary cannot load the module at all ("the Go language version used to
#      build golangci-lint is lower than the targeted Go version"). CI builds the
#      tag with the job's own toolchain (install-mode: goinstall) for exactly
#      that reason, and so does `make tools`.
#
# An installed binary that satisfies both is used as-is: `go install` builds it
# with this module's toolchain, and it starts with no compile step and no
# network. Anything else — no binary, a different version, an unreadable build,
# an older Go — falls back to `go run pkg@version`, which compiles the pinned tag
# with this module's toolchain, the same way CI does; that fallback needs the
# module source, so the first run on a machine that lacks it needs the network.
# `make tools` is how to preinstall it.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="$(cat .golangci-lint-version)"

# The Go version this module targets; the linter must have been built with at
# least this, or it refuses to load the module.
modGo="$(awk '/^go /{print $2; exit}' go.mod)"

# goAtLeast A B — true when Go version A is >= B (major.minor decides; the
# patch component never has, for this question).
goAtLeast() {
	printf '%s\n%s\n' "$1" "$2" | awk -F. '
		NR == 1 { have = $1 * 1000 + $2 }
		NR == 2 { exit !(have >= $1 * 1000 + $2) }'
}

fallback() {
	echo "go run github.com/golangci/golangci-lint/cmd/golangci-lint@$version run ./..." >&2
	exec go run "github.com/golangci/golangci-lint/cmd/golangci-lint@$version" run ./...
}

bin="$(command -v golangci-lint 2>/dev/null || true)"
if [ -n "$bin" ]; then
	# `go version -m` answers both questions in one query: the module version the
	# binary was built from, and the Go version that built it.
	info="$(go version -m "$bin" 2>/dev/null || true)"
	binVersion="$(printf '%s\n' "$info" | awk \
		'$1 == "mod" && $2 == "github.com/golangci/golangci-lint" { print $3 }')"
	binGo="$(printf '%s\n' "$info" | sed -n '1s/.*: go//p')"
	if [ "$binVersion" = "$version" ] && [ -n "$binGo" ] && goAtLeast "$binGo" "$modGo"; then
		echo "golangci-lint $binVersion (installed, built with go$binGo): run ./..." >&2
		exec "$bin" run ./...
	fi
	if [ -z "$binVersion" ]; then
		echo "ignoring $bin — its build info names no golangci-lint module (run: make tools)" >&2
	else
		echo "ignoring $bin — built from $binVersion with go$binGo; this tree pins $version built with go >= $modGo (run: make tools)" >&2
	fi
fi

fallback
