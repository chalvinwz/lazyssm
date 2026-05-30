# lazyssm demo tape — renders docs/demo.gif
#
# Prereqs: install VHS (https://github.com/charmbracelet/vhs) and lazyssm:
#   go install github.com/charmbracelet/vhs@latest
#   make install            # puts lazyssm on PATH
# Record (no AWS account needed — uses built-in demo data):
#   vhs demo/lazyssm.tape
# Re-record only when the UI changes; commit docs/demo.gif alongside.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 90ms

# Demo mode: fixed sample inventory, never calls AWS.
Env LAZYSSM_DEMO "1"

Hide
Type "lazyssm" Enter
Sleep 1.5s
Show

Sleep 2s

# Browse the list.
Down@500ms 3
Sleep 1.2s

# Pin the highlighted instance — it floats to the top.
Type "p"
Sleep 1.8s

# Fuzzy search (client-side, instant).
Type "/"
Sleep 700ms
Type "api"
Sleep 2s
Escape
Sleep 1.2s

# Server-side filter by tag.
Type "f"
Sleep 700ms
Type "tag:Env=prod"
Sleep 1.2s
Enter
Sleep 2s

# Toggle source: SSM-only <-> all EC2.
Type "t"
Sleep 2s
Type "t"
Sleep 1.5s

# Keybinding help overlay.
Type "?"
Sleep 2.2s
Type "?"
Sleep 1.2s

# Quit.
Type "q"
Sleep 1.2s
