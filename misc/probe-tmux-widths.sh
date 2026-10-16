#!/usr/bin/env bash
#
# probe-tmux-widths.sh — re-take the host's cell counts for the clusters this
# package's width table and tmux are measured to disagree about.
#
# Usage:
#   ./misc/probe-tmux-widths.sh
#
# Why this exists: docs/tui.md (Line Wrapping) and the host model in
# internal/adapters/terminal/host_width_test.go assert that a host bills certain
# clusters a different number of cells than width.go does, and the frame's
# containment contract is sized against that. Those numbers are a claim about one
# build of one host, so the build is printed with them, and the way they were
# taken is a script rather than a sentence saying they were taken.
#
# The procedure, per row: a 40x6 pane on a private socket, printf the bytes at
# column 0, then #{cursor_x} — the pane starts at column 0 and the rows here are
# short, so the cursor column IS the cells the row took. "app" is what
# internal/adapters/terminal's table bills the same bytes (cellWidth; the table
# itself is the source of truth — this column is here to make a disagreement
# visible, and the same pairs are listed in host_width_test.go).
#
# Private socket only (-L): a tmux the reader is working in is left alone.
# Deliberately not in `make check`: it is one tmux run per row, and what it
# records is a conclusion (a build that answers differently is a reason to
# re-take the numbers, not to loosen them), which the tests then assert.

set -u

TM="tmux -L alaya-width-$$-$RANDOM"
cleanup() { $TM kill-server 2>/dev/null; }
trap cleanup EXIT

if ! command -v tmux >/dev/null; then
	echo "tmux is not installed; this probe measures a terminal and needs one" >&2
	exit 1
fi

# probe label app-cells printf-hex: draw the bytes in a fresh pane and report the
# column the pane's cursor ended up at.
probe() {
	local label="$1" app="$2" hex="$3"
	local fmt="" i=0
	while [ $i -lt ${#hex} ]; do
		fmt="$fmt\\x${hex:$i:2}"
		i=$((i + 2))
	done
	$TM new-session -d -x 40 -y 6 "printf '$fmt'; sleep 30" 2>/dev/null
	sleep 0.35
	local cur
	cur=$($TM display-message -p '#{cursor_x}' 2>/dev/null)
	$TM kill-server 2>/dev/null
	sleep 0.1
	local verdict=ok
	[ "$cur" = "$app" ] || verdict="DISAGREES"
	printf '%-34s app=%-3s host=%-3s %s\n' "$label" "$app" "$cur" "$verdict"
}

echo "tmux $(tmux -V)  — the build these numbers are from (the recorded table is 3.7c)"
echo
echo "the classes docs/tui.md (Line Wrapping) records, and the emoji-presentation"
echo "classes the glyph policy waives (they are the same kind of fact)"
echo
# A spacing mark the host does not combine onto its base: this table clusters it
# (one cell with the base), the host charges it a cell of its own.
probe "devanagari  ka + i-matra"   1 e0a495e0a4bf
probe "bengali     ka + i-matra"   1 e0a695e0a6bf
probe "tamil       ka + aa-matra"  1 e0aea4e0aebe
probe "malayalam   ka + i-matra"   1 e0b495e0b4bf
probe "sinhala     ka + aeda"      1 e0b6a9e0b79a
probe "thai        ko + sara am"   1 e0b881e0b8b3
probe "khmer       ka + aa"        1 e19e80e19eb6
# Format characters: nothing to this table's cluster, a cell to the host.
probe "soft hyphen after a"        1 61c2ad
probe "arabic number sign after a" 1 61d880
# Narrower to the host than to this table.
probe "lone regional indicator"    2 f09f87a6
probe "hangul filler"              2 e385a4
probe "hangul jungseong filler"    1 e185a0
# Emoji presentation: the host makes the cell the variation selector joined TWO
# cells wide, whatever this table made of it — so 1 here (an ASCII base, which is
# not emoji) is 2 there, and a keycap this table already bills two agrees.
probe "ascii a + vs16"             1 61efb88f
probe "keycap 1"                   2 31efb88fe283a3
probe "sun + vs16"                 2 e29880efb88f
# The agreeing classes, for contrast: same table, same answer.
probe "cjk"                        4 e4b8ade69687
probe "box rule"                   1 e29480
probe "braille"                    1 e2a0bf
