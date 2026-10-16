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
# short, so the cursor column IS the cells the row took.
#
# Only the HOST's number is printed. This package's own answer for the same bytes
# is not a number to type in here: it comes out of cellWidth, and the pair (this
# table, the host) is asserted in internal/adapters/terminal/host_width_test.go —
# TestHostDeltaMatchesTheMeasuredHost, whose rows are these. A hand-written "app"
# column is exactly how one of them was recorded wrong once.
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

# probe label printf-hex: draw the bytes in a fresh pane and report the column
# the pane's cursor ended up at.
probe() {
	local label="$1" hex="$2"
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
	printf '%-34s host=%s\n' "$label" "$cur"
}

echo "tmux $(tmux -V)  — the build these numbers are from (the recorded table is 3.7c)"
echo
echo "the classes docs/tui.md (Line Wrapping) records, and the emoji-presentation"
echo "classes the glyph policy waives (they are the same kind of fact)"
echo
# A spacing mark the host does not combine onto its base: this table clusters it
# (one cell with the base), the host charges it a cell of its own.
probe "devanagari  ka + i-matra"   e0a495e0a4bf
probe "bengali     ka + i-matra"   e0a695e0a6bf
probe "gurmukhi    ka + sihari"    e0a895e0a8bf
probe "tamil       ka + aa-matra"  e0aea4e0aebe
probe "malayalam   ka + i-matra"   e0b495e0b4bf
probe "sinhala     ka + aeda"      e0b79ae0b79a
probe "thai        ko + sara am"   e0b881e0b8b3
probe "khmer       ka + aa"        e19e80e19eb6
# Format characters: nothing to this table's cluster, a cell to the host.
probe "soft hyphen after a"        61c2ad
probe "arabic number sign after a" 61d880
# Narrower to the host than to this table.
probe "lone regional indicator"    f09f87a6
probe "hangul filler U+3164"       e385a4
probe "hangul jungseong filler"    e185a0
# Agreeing rows, for contrast: the emoji-presentation classes this table applies
# itself (a cluster ending in a variation selector is two cells to BOTH), the
# classes a host that does not cluster would bill differently (tmux clusters
# them), the other fillers, and the glyphs the app draws.
probe "ascii a + vs16"             61efb88f
probe "keycap one"                 31efb88fe283a3
probe "sun + vs16"                 e29880efb88f
probe "zwj family"                 f09f91a8e2808df09f91a9e2808df09f91a7e2808df09f91a6
probe "regional pair"              f09f87a8f09f87b3
probe "hangul choseong filler"     e1859f
probe "halfwidth hangul filler"    efbea0
probe "cjk"                        e4b8ade69687
probe "box rule"                   e29480
probe "braille"                    e2a0bf
