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
# For each file the script:
#   1. skips it if its frontmatter already says message_version: 14;
#   2. renames it to <name>_backup20261014.alaya (the original is kept);
#   3. writes the converted file back to the original path.
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
	if head -n 20 "$f" | grep -qE '^message_version:[[:space:]]*14[[:space:]]*$'; then
		echo "already v14: $f"
		continue
	fi

	backup="${f%.alaya}_backup20261014.alaya"
	if [ -e "$backup" ]; then
		echo "backup already exists, refusing to overwrite: $backup" >&2
		status=1
		continue
	fi

	mv -- "$f" "$backup"
	if (cd "$repo_root" && go run "$converter" "$backup" "$f.upgrade-tmp"); then
		mv -- "$f.upgrade-tmp" "$f"
		echo "upgraded: $f  (backup: $backup)"
	else
		rm -f -- "$f.upgrade-tmp"
		mv -- "$backup" "$f"
		echo "FAILED, restored: $f" >&2
		status=1
	fi
done

exit "$status"
