#!/bin/sh
#
# inspect-image — answer a question about images without putting the pixels in
# the caller's conversation.
#
# The caller writes the image path(s) into the prompt; the script never sees a
# path as such, which is what leaves the number of images to the caller. The
# prompt goes to a separate AlayaCore in --terseio mode, which reads the images
# itself and prints only its final answer — so the caller's history records a
# command and a line of text instead of an image.
#
# Usage: inspect-image.sh "<prompt naming the image path(s)>"
#
# INSPECT_IMAGE_CONFIG — a config directory to run the nested instance with.
# Passed as --config-path only when set. A directory without a model.conf is
# not left alone: AlayaCore writes its default (an Ollama model) there. So an
# unset variable has to mean "use the caller's own configuration", never
# "point the nested run at an empty directory".

set -eu

if [ "$#" -ne 1 ]; then
	printf 'usage: %s "<prompt naming the image path(s)>"\n' "$0" >&2
	exit 2
fi

prompt=$1
if [ -z "$prompt" ]; then
	printf '%s: empty prompt\n' "$0" >&2
	exit 2
fi

# terseio reads an input that begins with ":" as a command, not a prompt
# (docs/terseio.md), so a prompt that happens to start with one would be run as
# a command and fail as an unknown one — an error with nothing to do with the
# image. Refused here rather than passed on, because the nested run's own
# complaint would not point at the prompt.
case $prompt in
:*)
	printf '%s: the prompt begins with ":" — terseio would run it as a command\n' "$0" >&2
	exit 2
	;;
esac

if ! command -v alayacore >/dev/null 2>&1; then
	printf '%s: alayacore is not on PATH; the nested run cannot start\n' "$0" >&2
	exit 1
fi

# --builtin-tools read_file keeps the *builtin* set to what looking at an image
# needs: no execute_command, no write_file, no edit_file. It does not keep the
# nested instance to read_file alone — MCP tools are appended to the builtin set
# whatever it is (agent/session_loop.go), and the servers come from mcp.conf in
# the config directory. That is why the documented setup points
# INSPECT_IMAGE_CONFIG at a directory without one.
#
# No --skill — the nested run must not load this skill and call itself. No
# --session — the image must not reach a session file. No --tool-confirm —
# terseio rejects it, since a confirmation could never be answered on stdin.
#
# --max-steps bounds the run. The default is no limit, and a nested instance
# that is looping is billing with nobody watching it.
set -- --terseio --builtin-tools read_file --max-steps 8 \
	--system 'You are looking at images on behalf of another agent that cannot see them. Read every image path named in the request with read_file before you answer. Answer only from what the images show; if a path cannot be read, say so plainly. Reply with the answer itself and nothing else — no preamble, no restating of the request, no offer to help further.'

if [ -n "${INSPECT_IMAGE_CONFIG:-}" ]; then
	set -- "$@" --config-path "$INSPECT_IMAGE_CONFIG"
fi

if ! answer=$(printf '%s' "$prompt" | alayacore "$@"); then
	printf '%s: the nested alayacore run failed (its diagnostics are above)\n' "$0" >&2
	exit 1
fi

# terseio prints nothing when the final message carries no text — a
# reasoning-only or tool-only ending. That is a failure to answer, not an
# empty answer, and a caller that cannot tell the two apart will read silence
# as "nothing wrong".
if [ -z "$answer" ]; then
	printf '%s: the nested run produced no answer text\n' "$0" >&2
	exit 1
fi

printf '%s\n' "$answer"
