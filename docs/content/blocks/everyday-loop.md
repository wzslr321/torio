## The everyday loop {#everyday-loop}

Once the VM and backend are up, the day-to-day loop is the same whichever editor
or interface you use:

1. Work in a checkout — from a Desktop session, your own editor, or `torio project enter <id>`.
2. Edit, or let your AI tool edit, files there.
3. Run a check that reads rather than writes.
4. Review what changed: `git diff` and `git status`.
5. Decide whether any of it should leave the VM.
6. If it should: `torio project shell <id>`, commit, push, exit.

Steps 5 and 6 are the split: Torio forwards operator write capability only
inside a session you opened, and stops forwarding it when you exit.
