## Prepare the workspace (non-destructive) {#workspace-clone}

Decide by the workspace's current state. Every branch is non-destructive:

| State of `/home/hermes/projects/REDACTED-PROJECT` | Action |
| --- | --- |
| Absent | Clone the exact remote into it. |
| Git repo, `origin` exact, on `main`, clean | **Retain as-is** and only run the verification checks. No delete, reclone, reset, or clean. |
| Non-repo, origin mismatch, dirty, or wrong branch | **Hard stop**, no destructive action. Report it. |

Classify before acting — every command here is read-only:

```bash
torio vm ssh -- sudo -u hermes -- test -e /home/hermes/projects/REDACTED-PROJECT
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT remote get-url origin
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT rev-parse --abbrev-ref HEAD
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT status --porcelain
```

Only when the path is **absent**, clone the exact remote directly into it. The
control plane runs credential-neutral `git`; the human-established read access
supplies authentication and the control plane never reads it:

```bash
torio vm ssh -- sudo -u hermes -- \
    git clone https://github.com/REDACTED/REDACTED \
              /home/hermes/projects/REDACTED-PROJECT
```

Verify — all of these must hold, else hard stop:

```bash
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT remote get-url origin
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT rev-parse --abbrev-ref HEAD
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT rev-parse --is-shallow-repository
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT status --porcelain
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT config --local --get credential.helper
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT fsck --no-dangling
```

Require: exact `origin`; on `main` (HEAD is the remote tip, fetch-verified by the
clone); full, non-shallow history; clean tree; **repo-local** `credential.helper`
unset; `fsck` clean; owned `hermes:hermes`.
