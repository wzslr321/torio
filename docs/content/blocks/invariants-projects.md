## What holds, always {#invariants}

These are guarantees `torio project` enforces, not rules you have to remember.
Each one fails closed: when Torio cannot prove the condition, it stops and says
so rather than proceeding.

- **Host Git state never crosses the boundary.** Torio copies no host `.git/`, hooks, SSH keys, tokens, `.env`, or Git configuration into the VM. Private SSH read access uses a key the guest generates and keeps; the host holds no copy, and you authorize it on the forge.
- **A workspace is never seeded from the host.** A recursive copy would drag the host `.git/` — config, hooks, host-only keys — across the VM boundary. If read access cannot be provisioned, the correct outcome is to stop at that prerequisite, not to weaken the boundary.
- **Attaching is non-destructive.** `add` clones only into a path that is absent. A checkout already there with the registered origin is verified and adopted as-is. Anything else — not a repository, a different origin, unreadable — is a hard stop. Nothing is overwritten, reset, cleaned, deleted, or recloned over.
- **No substitution.** If the exact remote is unreadable, Torio does not swap in another repository and does not attempt an auth workaround.
- **No unattended operator signature.** Torio never pushes, merges, deploys, or releases. Its write route exists inside `project shell`, or one approved signature at a time in an agent session opened with `--push-grant`; no unanswered prompt signs. The forge-side permission of a guest deploy key is outside this guarantee.
