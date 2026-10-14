# demo-rawio.tape — VHS script for a raw IO mode demo GIF
#
# Usage:
#   vhs demo-rawio.tape
#
# Prerequisites:
#   - VHS installed: go install github.com/charmbracelet/vhs@latest
#   - alayacore on PATH
#   - A working LLM backend configured in ~/.alayacore/model.conf

Output alayacore-demo-rawio.gif

Require alayacore

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 550
Set Margin 8
Set Padding 4
Set BorderRadius 12
Set Theme "Builtin Dark"

# ── Launch in raw IO mode ─────────────────────────────────────────

Hide
Type "cd /home/wallace/playground/alayacore"
Enter
Sleep 500ms
Show

Type 'printf "UX{\"type\":\"text\",\"text\":\"current time\"}\nUE\n" | ./alayacore --rawio --config-path ./misc/samples/config/ | go run ./misc/wirecat.go'

Enter

Sleep 4s

