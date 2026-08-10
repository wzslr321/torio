# Records the README walkthrough against an already configured box.
#
# The registry is staged from screens-registry.json into a throwaway
# XDG_CONFIG_HOME, so the project list is three example repositories rather than
# whatever the author happens to have attached. Everything else is read from the
# running box.
#
# Prerequisites, all on the host:
#   - vhs and JetBrains Mono installed
#   - the default hermes box running, bootstrapped, with a Brain and a service
#   - the binary built from this tree: `go build -o torio ./cmd/torio`
#   - run from the repository root

Output docs/demo/hub.gif

Set FontSize 20
Set Width 1600
Set Height 620
Set Padding 24
Set Margin 24
Set MarginFill "#1a1a2e"
Set WindowBar Colorful
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono"
Set Framerate 20
Set TypingSpeed 60ms
Set WaitTimeout 2m

Hide
Type "export PATH=$PWD:$PATH"
Enter
Type "export XDG_CONFIG_HOME=$(mktemp -d)"
Enter
Type "mkdir -m 700 -p $XDG_CONFIG_HOME/torio"
Enter
Type "install -m 600 docs/demo/screens-registry.json $XDG_CONFIG_HOME/torio/projects.json"
Enter
Type "export PS1='> '; clear"
Enter
Show

# One word, no arguments. It sits on screen for a beat before the return,
# because the whole claim is that this is the only thing to remember.
Sleep 500ms
Set TypingSpeed 100ms
Type "torio"
Set TypingSpeed 60ms
Sleep 1200ms
Enter

# The header lands immediately: this box, this backend, running. The step it is
# on has to wait for the guest.
Sleep 24s

# Every step verified, and the one thing Torio cannot verify said out loud.
Sleep 4s

# The same poll `torio status` prints.
Type "2"
Sleep 7s

# The registry, and the ways into a checkout.
Type "3"
Sleep 4s

# Adding one is a form, and this add is real: the box already has this checkout,
# so `add` adopts what is there rather than cloning anything, and the row that
# appears afterwards is the registry having actually changed.
Type "a"
Sleep 800ms
Set TypingSpeed 90ms
Type "hermes-agent"
Sleep 600ms
Tab
Set TypingSpeed 45ms
Type "https://github.com/NousResearch/hermes-agent.git"
Set TypingSpeed 60ms
Sleep 1500ms
Enter
Sleep 12s

# The vault, read from the guest.
Type "4"
Sleep 6s

# The service, and whether its endpoint is answering.
Type "5"
Sleep 6s

Type "q"
Sleep 800ms
