## Credential preflight (this step gates the rest) {#workspace-preflight}

Read access to the private remote is **outside the scope of Code V0 and of the
control plane**: Torio neither sets up nor configures credentials. A human must
establish repo-scoped guest read access outside Torio; the secret and the method
never enter this repository, its evidence, pull request comments, or the
control-plane workflow.

From the guest, as `hermes`, check read access to the exact remote without any
prompt or askpass:

```bash
torio vm ssh --timeout 90s -- sudo -u hermes -- \
    env GIT_TERMINAL_PROMPT=0 \
    git ls-remote --exit-code https://github.com/REDACTED/REDACTED HEAD
```

- Exit `0` (prints the `HEAD` oid) → read access exists; continue.
- Non-zero (`could not read Username … terminal prompts disabled`) → no read access. Stop and report the human-only prerequisite, then re-run once it is satisfied. Do **not** seed from a host copy.
