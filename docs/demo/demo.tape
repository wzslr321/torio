# Records docs/demo/torio-demo.gif: the agent can commit, only you can push.
#
# Prerequisites, all on the host:
#   - vhs installed (`brew install vhs`) and the JetBrains Mono font
#     (`brew install --cask font-jetbrains-mono`)
#   - the claude-code box running and logged in (`torio status`)
#   - the box has its own working tree with the demo branch checked out:
#       torio project add torio --backend claude-code
#       torio project enter torio --backend claude-code
#       git switch -c demo/vm-boundary
#       exit
#   - Claude Code has been launched once in that workspace and the
#     "do you trust this folder" dialog answered with Yes (it is remembered);
#     otherwise the recording types into the dialog and the take dies
#   - run from this directory: `cd docs/demo && vhs demo.tape`
#
# The recording runs a real agent against a real remote. Nothing is faked;
# the push fails because the guest holds no credential, not because anything
# was staged. The agent's exact words differ per take, so expect to record
# two or three times before the take reads well.
#
# If `torio project agent` errors (box down, id wrong), abort: everything
# after it would run on the HOST and the take is garbage.
#
# Cleanup after the final take, in `torio project shell torio --backend claude-code`:
#   git push origin --delete demo/vm-boundary
#   git switch main && git branch -D demo/vm-boundary

Output torio-demo.gif
Output torio-demo.mp4

Set FontSize 20
Set Width 1600
Set Height 900
Set Padding 24
Set Margin 24
Set MarginFill "#1a1a2e"
Set WindowBar Colorful
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono"
Set Framerate 20
Set TypingSpeed 40ms
Set WaitTimeout 8m

# A minimal prompt, so the recording is about the commands and not the host.
Hide
Type "export PS1='❯ '; clear"
Enter
Show

# The agent gets a session inside the box. Ready when the welcome box or the
# status bar renders; the exact footer text varies between Claude Code versions.
Type "torio project agent torio --backend claude-code"
Enter
Wait+Screen /for shortcuts|bypass permissions|Welcome back/
Sleep 3s

# Off camera: a small fast model, so the take stays short. The boundary being
# demonstrated does not depend on which model hits it.
#
# `/model` writes the box's own default, so after a take the guest keeps
# running Haiku until someone changes it back. Restore it afterwards:
#   limactl shell torio-claude-code -- sudo -u claude \
#     python3 -c "import json,pathlib; p=pathlib.Path('/home/claude/.claude/settings.json'); \
#       d=json.loads(p.read_text()); d.pop('model',None); p.write_text(json.dumps(d,indent=2))"
Hide
Type "/model haiku"
Enter
Sleep 3s
Type "/clear"
Enter
Sleep 2s
Show

# One instruction that shows both sides of the boundary: the commit lands,
# the push is denied.
Type "Make an empty commit on this branch with message 'hello from inside the VM', then try to push it to origin. Report what happened in one short sentence, then on its own line write the word done in uppercase."
Enter
# A fixed sleep rather than Wait+Screen: a Wait that times out discards the
# whole recording, while a sleep that is too short only costs a take. Haiku
# answers this in well under ten seconds; the rest is margin, and every second
# of margin the agent does not use is a frozen screen in the result.
Sleep 14s

# Let the agent finish explaining, then leave the session.
Type "/exit"
Enter
Sleep 4s

# The operator session: your SSH agent, forwarded for exactly this session.
# You push the commit the agent made.
Type "torio project shell torio --backend claude-code"
Enter
Sleep 4s
Type "git push origin HEAD"
Enter
# The push waits for the operator_key approval dialog on the host, so this
# sleep is the human's reaction time plus the push itself. Answer the dialog
# promptly: whatever is left over is a frozen screen at the end of the take.
Sleep 12s
Type "exit"
Enter
Sleep 3s
