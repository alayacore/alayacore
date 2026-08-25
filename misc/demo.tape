# demo.tape — VHS script to record an AlayaCore TUI demo GIF
#
# Usage:
#   vhs demo.tape
#
# Prerequisites:
#   - VHS installed: go install github.com/charmbracelet/vhs@latest
#   - alayacore on PATH
#   - A working LLM backend configured in ~/.alayacore/model.conf

Output alayacore-demo.gif

Require alayacore

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 550
Set Margin 8
Set Padding 4
Set BorderRadius 12
Set Theme "Builtin Dark"

# ── Launch alayacore ────────────────────────────────────────────────

Hide
Type "cd /home/wallace/playground/alayacore"
Enter
Sleep 500ms
Show

Type "./alayacore"
Enter
Sleep 1s

# ── Choose Model ────────────────────────────────────────────────────

Ctrl+L
Sleep 1s
Type "deflash"
Sleep 1s
Enter

# ── Task ────────────────────────────────────────────────────────────

Type "what Go version does this project use?"
Enter
Sleep 5s

# ── Unfold, Fold ────────────────────────────────────────────────────

Tab
Type "k"
Sleep 200ms
Type "k"
Sleep 200ms
Type "k"
Sleep 500ms
Type "j"
Sleep 500ms
Space
Sleep 1s
Space
Sleep 500ms


# ── Exit ────────────────────────────────────────────────────────────

Type ":quit"
Enter
Sleep 1s
Type "y"
Sleep 1s
