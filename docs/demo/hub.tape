# Records docs/demo/hub.gif: one pass through the hub on a box that is already
# set up.
#
# What the take is about is the second half: five screens, each of which is a
# read of the same box, and a form that cancels without doing anything. The
# first twenty seconds are the hub proving what the guest holds before it will
# name a step. That wait is in the recording because it is in the product; a
# take that cut it would promise a hub that answers instantly.
#
# The registry is staged from screens-registry.json into a throwaway
# XDG_CONFIG_HOME, so the project list is three example repositories rather than
# whatever the author happens to have attached. Everything else is read from the
# running box.
#
# The service journal is deliberately not opened. It is real output from a real
# backend and nothing redacts it on the way to the screen, which is fine on your
# own terminal and wrong in a file that gets published.
#
# Prerequisites, all on the host:
#   - vhs installed (`brew install vhs`) and the JetBrains Mono font
#     (`brew install --cask font-jetbrains-mono`)
#   - the default hermes box running, bootstrapped, with a Brain and a service
#   - the binary built from this tree: `go build -o torio ./cmd/torio`
#   - run from the repository root: `vhs docs/demo/hub.tape`

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

# One word, no arguments. The header lands immediately: this box, this backend,
# running. The step it is on has to wait for the guest.
Sleep 1s
Type "torio"
Enter
Sleep 24s

# Every step verified, and the one thing Torio cannot verify said out loud.
Sleep 4s

# The same poll `torio status` prints, redrawn on a timer.
Type "2"
Sleep 7s

# The registry, and the ways into a checkout.
Type "3"
Sleep 4s

# Adding one is a form. Escape leaves the registry as it was.
Type "a"
Sleep 1s
Type "notes-service"
Sleep 1s
Tab
Type "https://github.com/you/notes-service.git"
Sleep 2s
Escape
Sleep 2s

# The vault, read from the guest.
Type "4"
Sleep 6s

# The service, and whether its endpoint is answering.
Type "5"
Sleep 6s

Type "q"
Sleep 2s
