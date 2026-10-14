#!/usr/bin/env bash
#
# session_upgrade_v15.sh — migrate a v14 AlayaCore session file to v15.
#
# Usage:
#   ./misc/session_upgrade_v15.sh [file.alaya ...]
#
# With no arguments it upgrades every *.alaya in the current directory.
#
# v15 changed the wire framing: the 4-byte length field is gone and a frame is one
# line. A v14 value is already the value v15 carries, so this step re-frames and
# nothing else — the tags do not move. It is not the same step as the v13 → v14
# conversion (misc/session_upgrade_v14.sh), and a v13 file needs that one
# first: this script refuses it and says so.
#
# The core does not read v14 files (loading demands an exact message_version
# match), so an old session must be converted before it can be reopened.
#
# The original is never destroyed. For each file the script:
#   1. skips it if its frontmatter already says message_version: 15, or if it is
#      one of this script's own backups;
#   2. COPIES it to <name>_backup_before_v15.alaya (a pristine copy — the original
#      path still holds the original bytes at this point);
#   3. converts the copy into <name>.upgrade-tmp;
#   4. verifies the temp is a v15 session file, then renames it over the original
#      path (an atomic rename within the directory).
#
# So at every instant the original content exists in at least two places: the
# original path and the backup, until step 4 replaces the original with the
# verified conversion. A converter bug, a crash, or a kill loses nothing — the
# backup is a full copy, and the temp is only ever renamed into place after it has
# been checked.
#
# The conversion runs in misc/session_upgrade_v15.go (a one-off tool that carries
# its own copy of the v14 framing). It needs a Go toolchain; the file itself is not
# part of the build.

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
converter="$repo_root/misc/session_upgrade_v15.go"
v14_script="misc/session_upgrade_v14.sh"

# The version this step writes, and the one it reads. A file at any other version
# is reported rather than guessed at.
target_version=15
previous_version=14

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
	# The auto-scan skips this script's own backups: they are v14 by definition,
	# so upgrading one would only create a v15 copy of a file that was already
	# backed up once.
	mapfile -t files < <(find . -maxdepth 1 -name '*.alaya' ! -name '*_backup*' | sort)
fi
if [ "${#files[@]}" -eq 0 ]; then
	echo "no .alaya files to upgrade" >&2
	exit 0
fi

# file_version prints the message_version a session file records, or nothing when it
# says none.
file_version() {
	head -n 20 -- "$1" |
		sed -nE 's/^message_version:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' |
		head -n 1
}

# verify_upgraded checks that the converter produced a session file at v15. It is
# deliberately shallow — the converter already rejects a body it cannot parse — but
# it catches the failure that matters here: a run that wrote nothing, wrote outside
# the format, or stopped early, before that file can replace the original.
#
# The body check is new with this version and worth having: a v15 body is text, so
# its first frame begins with a two-letter tag. A v14 body began with that tag too
# followed by a length field, which is four bytes nothing in this format allows —
# decoded as a line, it is not a frame, so a conversion that silently did nothing
# fails here.
verify_upgraded() {
	[ -s "$1" ] || return 1
	head -n 1 -- "$1" | grep -qx -- '---' || return 1
	grep -qE "^message_version:[[:space:]]*${target_version}[[:space:]]*\$" "$1" || return 1
	grep -qE '^[A-Za-z][A-Za-z]\{' "$1" || return 1
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
	# directory (go run needs the module) while the caller's paths are relative to
	# where the script was invoked.
	f="$(cd "$(dirname "$f")" && pwd)/$(basename "$f")"

	# Dispatch on what the file says it is: this step's input is converted, its own
	# output is left alone, and a file behind its input names the step that comes
	# first — the failure a user hits by running the steps out of order, which is
	# worth an instruction rather than a parse error from the converter.
	version="$(file_version "$f")"
	case "$version" in
	15) echo "already v15: $f"; continue ;;
	14) ;;
	13)
		echo "a v13 file goes to v14 first, then here: run ${v14_script}: $f" >&2
		status=1
		continue
		;;
	*)
		echo "this step converts v14; $f says message_version ${version:-nothing}" >&2
		status=1
		continue
		;;
	esac

	backup="${f%.alaya}_backup_before_v15.alaya"
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
	if ! verify_upgraded "$tmp"; then
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
