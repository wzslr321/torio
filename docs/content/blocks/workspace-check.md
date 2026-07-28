## Run a check without leaving the control plane {#workspace-check}

To run something inside a checkout without opening a session, `torio vm ssh`
executes a fixed command as `hermes` and returns its output. It forwards no
stdin and no TTY, so it suits non-interactive checks and nothing else.

Pick a check from the repository's own contributor documentation — one that
reads and reports rather than writing, installing, deploying, or pushing — and
run it against the derived workspace path:

```bash
torio vm ssh -- sudo -u hermes -- \
    python3 /home/hermes/projects/my-service/scripts/some-check.py --check
torio vm ssh -- sudo -u hermes -- \
    git -C /home/hermes/projects/my-service status --porcelain
```

The second command must print nothing: a check that leaves the tree dirty was
not the read-only check you thought you were running.

Two things worth knowing before you rely on this:

- **`torio vm ssh` forwards no stdin.** Piping into it produces an empty result while still exiting `0` — `echo … | torio vm ssh -- … tee file` looks like it worked and wrote nothing. Create files in a real shell instead.
- **The guest is deliberately minimal.** Python is there; most other toolchains are not. Anything else you want to run inside the VM you install in the VM yourself, and that install must never add a Git remote, configure a credential helper, or grant push access.
