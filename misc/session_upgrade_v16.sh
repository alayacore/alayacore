#!/usr/bin/env bash
#
# session_upgrade_v16.sh — migrate a v15 AlayaCore session file to v16.
#
# Usage:
#   ./misc/session_upgrade_v16.sh [file.alaya ...]
#
# With no arguments it upgrades every *.alaya in the current directory.
#
# v16 renamed vocabulary and no framing: :mcp_cancel is :mcp_skip, :reason is
# :reasoning_level, and the SM that reports a reasoning-level change is
# "reasoning_level". None of those is in a session file — a file holds
# conversation content — so this step rewrites the frontmatter's
# message_version and changes nothing else. The body must come through byte for
# byte, which is what the verification below checks.
#
# It is a real step rather than a no-op because the loader demands the number
# exactly: a v15 file does not open in this build, and editing the number by
# hand is the same edit done without a backup. A v14 file needs
# misc/session_upgrade_v15.sh first; this script refuses it and says so.
#
# The original is never destroyed. For each file the script:
#   1. skips it if its frontmatter already says message_version: 16, or if it is
#      one of this script's own backups;
#   2. COPIES it to <name>_backup_before_v16.alaya (a pristine copy — the
#      original path still holds the original bytes at this point);
#   3. converts the copy into <name>.upgrade-tmp;
#   4. verifies the temp is a v16 file whose body is byte-identical to the
#      input's, then renames it over the original path (an atomic rename within
#      the directory).
#
# So at every instant the original content exists in at least two places: the
# original path and the backup, until step 4 replaces the original with the
# verified conversion. A converter bug, a crash, or a kill loses nothing — the
# backup is a full copy, and the temp is only ever renamed into place after it
# has been checked.
#
# The conversion runs in misc/session_upgrade_v16.go, which calls
# internal/sessionmigrate — the same code the migration test exercises.

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
converter="$repo_root/misc/session_upgrade_v16.go"
v15_script="misc/session_upgrade_v15.sh"

# The version this step writes, and the one it reads. A file at any other version
# is reported rather than guessed at.
target_version=16
previous_version=15

# tmp_files collects the temps to remove on any exit, including a signal.
tmp_files=()
cleanup() {
	local t
	for t in "${tmp_files[@]:-}"; do
		rm -f -- "$t"
	done
}
trap cleanup EXIT INT TERM

files=("$@")
if [ "${#files[@]}" -eq 0 ]; then
	# The auto-scan skips this script's own backups: they are v15 by definition,
	# so upgrading one would only create a v16 copy of a file already backed up.
	mapfile -t files < <(find . -maxdepth 1 -name '*.alaya' ! -name '*_backup*' | sort)
fi
if [ "${#files[@]}" -eq 0 ]; then
	echo "no .alaya files to upgrade" >&2
	exit 0
fi

# file_version prints the message_version a session file records, or nothing when
# it says none.
file_version() {
	head -n 20 -- "$1" |
		sed -nE 's/^message_version:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' |
		head -n 1
}

# body_of prints everything after the frontmatter's closing delimiter. This
# step's contract is that the body does not move, so the check is exact: the
# two bodies are compared with cmp.
body_of() {
	awk 'BEGIN { n = 0 } /^---[[:space:]]*$/ { n++; if (n == 2) { body = 1; next } } body' "$1"
}

# verify_upgraded checks the converter's output before it can replace anything:
# a v16 header, a frame-shaped body, and a body byte-identical to the input's.
verify_upgraded() {
	local src=$1 dst=$2
	[ -s "$dst" ] || return 1
	head -n 1 -- "$dst" | grep -qx -- '---' || return 1
	grep -qE "^message_version:[[:space:]]*${target_version}[[:space:]]*\$" "$dst" || return 1
	grep -qE '^[A-Za-z][A-Za-z]\{' "$dst" || return 1
	cmp -s <(body_of "$src") <(body_of "$dst") || return 1
	return 0
}

status=0
for f in "${files[@]}"; do
	if [ ! -f "$f" ]; then
		echo "skip (not a file): $f" >&2
		status=1
		continue
	fi
	# Absolute, because the converter runs with the repository as its working
	# directory (go run needs the module) while the caller's paths are relative
	# to where the script was invoked.
	f="$(cd "$(dirname "$f")" && pwd)/$(basename "$f")"

	# Dispatch on what the file says it is: this step's input is converted, its
	# own output is left alone, and a file behind its input names the step that
	# comes first — the failure a user hits by running the steps out of order,
	# which is worth an instruction rather than a parse error from the converter.
	version="$(file_version "$f")"
	case "$version" in
	"$target_version") echo "already v${target_version}: $f"; continue ;;
	"$previous_version") ;;
	14 | 13)
		echo "a v${version} file goes to 15 first, then here: run ${v15_script}: $f" >&2
		status=1
		continue
		;;
	*)
		echo "this step converts v${previous_version}; $f says message_version ${version:-nothing}" >&2
		status=1
		continue
		;;
	esac

	backup="${f%.alaya}_backup_before_v16.alaya"
	if [ "$backup" = "$f" ]; then
		echo "skip (already a backup): $f" >&2
		status=1
		continue
	fi
	if [ -e "$backup" ]; then
		echo "backup already exists, refusing to overwrite: $backup" >&2
		status=1
		continue
	fi

	tmp="$f.upgrade-tmp"
	backup_part="${backup}.part"
	tmp_files+=("$tmp" "$backup_part")

	# 1. Pristine copy, made to a ".part" and only renamed into place once it
	#    matches the original byte for byte. The original path is untouched
	#    throughout, so the content is in two places before anything modifies
	#    either — and a half-written backup can never sit at the backup path
	#    looking like a good one.
	if ! cp -p -- "$f" "$backup_part"; then
		echo "FAILED to back up, original untouched: $f" >&2
		status=1
		continue
	fi
	if ! cmp -s -- "$f" "$backup_part"; then
		echo "backup copy differs, original untouched: $f" >&2
		status=1
		continue
	fi
	if ! mv -f -- "$backup_part" "$backup"; then
		echo "FAILED to finalize backup, original untouched: $f" >&2
		status=1
		continue
	fi

	# 2. Convert the copy into the temp.
	if ! (cd "$repo_root" && go run "$converter" "$backup" "$tmp"); then
		echo "FAILED to convert, original untouched (backup: $backup): $f" >&2
		status=1
		continue
	fi

	# 3. Verify before replacing anything.
	if ! verify_upgraded "$backup" "$tmp"; then
		echo "conversion failed verification, original untouched (backup: $backup): $f" >&2
		status=1
		continue
	fi

	# 4. Atomic rename within the directory.
	if ! mv -f -- "$tmp" "$f"; then
		echo "FAILED to replace, original untouched (backup: $backup): $f" >&2
		status=1
		continue
	fi
	echo "upgraded: $f  (backup: $backup)"
done

exit "$status"
