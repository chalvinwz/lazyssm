# Renders docs/demo.gif. Needs VHS (https://github.com/charmbracelet/vhs) and
# lazyssm on PATH:
#   go install github.com/charmbracelet/vhs@latest
#   make install            # installs into GOBIN, which must be on PATH
# Record with demo data, so no AWS account is needed:
#   vhs demo/lazyssm.tape
# CI re-records the GIF on every release PR (release-please.yml); run this
# locally to preview a UI change.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 90ms

# Demo mode serves a fixed sample inventory and never calls AWS.
Env LAZYSSM_DEMO "1"

Hide
Type "lazyssm" Enter
Sleep 1.5s
Show

Sleep 2s

# Browse the list.
Down@500ms 3
Sleep 1.2s

# Switch to another AWS profile.
Type "P"
Sleep 1.5s
Type "prod"
Sleep 1.2s
Enter
Sleep 2s

# Switch region, found by city name.
Type "R"
Sleep 1.2s
Type "sydney"
Sleep 1.5s
Enter
Sleep 2s

# Pin the highlighted instance; pinned rows sort to the top.
Type "p"
Sleep 1.8s

# Fuzzy search, client-side.
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

# Toggle the source between SSM-only and all EC2.
Type "t"
Sleep 2s
Type "t"
Sleep 1.5s

# Open and close the keybinding help.
Type "?"
Sleep 2.2s
Type "?"
Sleep 1.2s

# Quit.
Type "q"
Sleep 1.2s
