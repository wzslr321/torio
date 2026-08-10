# Records docs/demo/screens/*.png: one frame of each hub screen.
#
# The images are documentation, so every one of them is a real hub reading a
# real box. Two things are arranged rather than found, and both are arranged in
# the open:
#
#   - The wizard frames run against `torio-demo`, an instance that does not
#     exist. That is the only honest way to photograph the first step: on a box
#     that is already set up the wizard correctly says there is nothing to do.
#   - The registry is staged from screens-registry.json into a throwaway
#     XDG_CONFIG_HOME, so the project list in the picture is three example
#     repositories rather than whatever the author happens to have attached.
#
# Everything else — box state, bootstrap, the Brain, the service — is read from
# the running box and photographed as it answers.
#
# Prerequisites, all on the host:
#   - vhs installed (`brew install vhs`) and the JetBrains Mono font
#     (`brew install --cask font-jetbrains-mono`)
#   - the default hermes box running, bootstrapped, with a Brain and a service
#     (`torio status`, `torio serve status`, `torio brain status`)
#   - the binary built from this tree: `go build -o torio ./cmd/torio`
#   - run from the repository root: `vhs docs/demo/screens.tape`
#
# The sleeps are what a probe costs, not padding. Opening the hub on a running
# box verifies the guest before it can name a step, which takes upwards of
# twenty seconds; a sleep that is too short photographs a spinner.

Output docs/demo/screens/screens.gif

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
Set TypingSpeed 40ms
Set WaitTimeout 3m

# The local build answers to `torio`, and the staged registry keeps the author's
# own attachments out of the picture. A minimal prompt, so a frame that catches
# the shell is about the command and not the host.
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

# A first run: no box, so the wizard holds the whole route and points at the
# step that starts it.
Type "TORIO_INSTANCE=torio-demo torio"
Enter
Sleep 8s
Screenshot docs/demo/screens/setup-first-run.png
Sleep 1s

# The step itself. Enter opens the form; escape closes it without creating
# anything, which is what keeps this tape safe to re-run.
Enter
Sleep 2s
Screenshot docs/demo/screens/setup-create-box.png
Sleep 1s
Escape
Sleep 1s
Type "q"
Sleep 2s

# The same screen on a box that is finished: every step verified, and the one
# thing Torio cannot verify said out loud.
Type "torio"
Enter
Sleep 35s
Screenshot docs/demo/screens/setup-complete.png
Sleep 1s

Type "2"
Sleep 10s
Screenshot docs/demo/screens/dashboard.png
Sleep 1s

Type "3"
Sleep 4s
Screenshot docs/demo/screens/projects.png
Sleep 1s

Type "4"
Sleep 8s
Screenshot docs/demo/screens/brain.png
Sleep 1s

Type "5"
Sleep 8s
Screenshot docs/demo/screens/serve.png
Sleep 1s

Type "q"
Sleep 2s
