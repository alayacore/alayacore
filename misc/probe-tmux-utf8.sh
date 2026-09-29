#!/usr/bin/env bash
#
# probe-tmux-utf8.sh — re-take the terminal measurements behind
# internal/adapters/terminal/sanitize_test.go's terminalCases.
#
# Usage:
#   ./misc/probe-tmux-utf8.sh
#
# Why this exists: terminalCases is not derived from a specification and not from
# the code under test. It is what a terminal drew, read back out of one. Two
# numbers per row decide the repair in sanitize.go, and both come from here:
# cells, the columns the bytes occupied, and boxes, the U+FFFD the terminal
# substituted for the parts of them it could not decode. A table like that is a
# claim about one build of one terminal, so the build is printed with the
# numbers, and the way the numbers were taken is a script rather than a sentence
# saying they were taken.
#
# The procedure, per row: a 40x6 pane on a private socket, printf the bytes,
# then #{cursor_x} — the pane starts at column 0 and the widest row here is 6
# cells, so the cursor column IS the cells the row took, substitute characters
# included — and capture-pane for the bytes the terminal stored, so U+FFFD can
# be counted and the row read rather than only counted.
#
# Private socket only (-L): a tmux the reader is working in is left alone.
# Deliberately not in `make check`: it is one tmux run per row, and what the
# table records is the conclusion, which the unit tests then assert. Run it when
# the table is re-taken — a tmux whose answers for ill-formed bytes have moved
# is a tmux the repair was not written against.

set -u

TM="tmux -L alaya-utf8-$$-$RANDOM"
cleanup() { $TM kill-server 2>/dev/null; }
trap cleanup EXIT

if ! command -v tmux >/dev/null; then
	echo "tmux is not installed; this probe measures a terminal and needs one" >&2
	exit 1
fi

# probe label fmt: draw 'a' + <bytes> + 'b' in a fresh pane and report what the
# terminal did with it. fmt is a printf format for the pane's own shell, so the
# bytes are written as octal escapes; cells is the cursor column after it drew
# them, boxes is the U+FFFD count, bytes is what it stored.
probe() {
	local label="$1" fmt="$2"
	$TM new-session -d -x 40 -y 6 "printf '$fmt'; sleep 30" 2>/dev/null
	sleep 0.55
	local cur bytes boxes
	cur=$($TM display-message -p '#{cursor_x}' 2>/dev/null)
	bytes=$($TM capture-pane -p 2>/dev/null | head -1 | od -An -tx1 | tr -d '\n' | sed 's/  */ /g')
	boxes=$(printf '%s' "$bytes" | grep -o 'ef bf bd' | wc -l | tr -d ' ')
	printf '%-44s cells=%-3s boxes=%-2s bytes:%s\n' "$label" "$cur" "$boxes" "$bytes"
	$TM kill-server 2>/dev/null
	sleep 0.15
}

echo "tmux $(tmux -V)  — the build these numbers are from (the checked-in table is 3.7c)"
echo
echo "the rows terminalCases cites, in its order"
echo
probe 'well-formed ASCII'                      'ab'
probe 'a lone continuation byte'                'a\200b'
probe 'three lone continuation bytes'           'a\200\201\202b'
probe 'a 2-byte encoding cut short'             'a\303b'
probe 'a 3-byte encoding cut short'             'a\344\270b'
probe 'a 4-byte encoding cut short'             'a\360\237\230b'
probe 'an overlong encoding'                    'a\300\200b'
probe 'a surrogate'                             'a\355\240\200b'
probe 'a surrogate, then a continuation'        'a\355\240\200\200b'
probe 'a surrogate, then two continuations'     'a\355\240\200\200\200b'
probe 'a whole 3-byte character, then a cont.'  'a\344\270\200\200b'
probe 'a whole 2-byte character, then a cont.'  'a\303\200\200b'
probe 'a byte past U+10FFFF'                    'a\365b'
probe 'two bytes that are never UTF-8'          'a\376\377b'
probe 'a lone C1 control'                       'a\233b'
probe 'a lone C1 control before text'           'a\23331mb'
probe 'a C1 control in its UTF-8 form'          'a\302\233b'
echo
echo "contrast rows, cited by no case: they are why the two lone-C1 rows above"
echo "are the ones a reader should not try to explain away"
echo
probe 'row 16 as 7-bit: ESC [ 3 1 m'            'a\033[31mb'
probe 'the other C1 introducer, 0x9D (OSC)'     'a\235b'
probe 'a whole 4-byte char, then a cont.'       'a\360\220\200\200\200b'
probe 'a whole 4-byte char that is two cells'   'a\360\237\230\200b'
