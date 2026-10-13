#!/usr/bin/env bash
#
# session_upgrade_20261014.sh — migrate v13 AlayaCore session files to v14.
#
# Usage:
#   ./misc/session_upgrade_20261014.sh [file.alaya ...]
#
# With no arguments it upgrades every *.alaya in the current directory.
#
# v14 changed the wire format: content frames are JSON content items, the tag
# vocabulary collapsed to UX/AX/AF/UF, and the tool-call id is "call_id". The
# core does not read v13 files (loading demands an exact message_version match),
# so an old session must be converted before it can be reopened.
#
# The original is never destroyed. For each file the script:
#   1. skips it if its frontmatter already says message_version: 14, or if it is
#      one of this script's own backups;
#   2. COPIES it to <name>_backup20261014.alaya (a pristine copy — the original
#      path still holds the original bytes at this point);
#   3. converts the copy into <name>.upgrade-tmp;
#   4. verifies the temp is a v14 session file, then renames it over the
#      original path (an atomic rename within the directory).
#
# So at every instant the original content exists in at least two places: the
# original path and the backup, until step 4 replaces the original with the
# verified conversion. A converter bug, a crash, or a kill loses nothing — the
# backup is a full copy, and the temp is only ever renamed into place after it
# has been checked.
#
# The conversion runs in misc/session_upgrade_20261014.go (a one-off tool that
# still knows the old format). It needs a Go toolchain; the file itself is not
# part of the build.
#
# Old files carry no per-message time, so converted parts carry none either: the
# adapter stamps those with its receipt clock, exactly as v13 did.

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
converter="$repo_root/misc/session_upgrade_20261014.go"

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
	# The auto-scan skips this script's own backups: they are v13 by definition,
	# so upgrading them would only create a two-generation backup.
	mapfile -t files < <(find . -maxdepth 1 -name '*.alaya' ! -name '*_backup20261014.alaya' | sort)
fi
if [ "${#files[@]}" -eq 0 ]; then
	echo "no .alaya files to upgrade" >&2
	exit 0
fi

# verify_upgraded checks that the converter produced a session file at v14. It is
# deliberately shallow — the converter already rejects a body it cannot parse —
# but it catches the failure that matters here: a run that wrote nothing, wrote
# outside the format, or stopped early, before that file can replace the
# original.
verify_upgraded() {
	[ -s "$1" ] || return 1
	head -n 1 -- "$1" | grep -qx -- '---' || return 1
	grep -qE '^message_version:[[:space:]]*14[[:space:]]*$' "$1" || return 1
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

	if head -n 20 -- "$f" | grep -qE '^message_version:[[:space:]]*14[[:space:]]*$'; then
		echo "already v14: $f"
		continue
	fi

	backup="${f%.alaya}_backup20261014.alaya"
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
