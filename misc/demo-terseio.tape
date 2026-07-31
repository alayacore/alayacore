# demo-terseio.tape — VHS script for a terse IO mode demo GIF
#
# Usage:
#   vhs demo-terseio.tape
#
# Prerequisites:
#   - VHS installed: go install github.com/charmbracelet/vhs@latest
#   - alayacore on PATH
#   - A working LLM backend configured in ~/.alayacore/model.conf

Output alayacore-demo-terseio.gif

Require alayacore

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 550
Set Margin 8
Set Padding 4
Set BorderRadius 12
Set Theme "Builtin Dark"

# ── Launch in terse IO mode ─────────────────────────────────────────

Hide
Type "cd /home/wallace/playground/alayacore"
Enter
Sleep 500ms
Show

Sleep 500ms
Type "echo 'What is my CPU?' | ./alayacore --terseio"
Enter
Sleep 5s
