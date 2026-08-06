## What holds, always {#invariants}

These are guarantees `torio project` enforces, not rules you have to remember.
Each one fails closed: when Torio cannot prove the condition, it stops and says
so rather than proceeding.

- **The control plane is credential- and config-neutral.** It never copies or materializes host Git state in the VM — no host `.git/`, `.git/config`, hooks, SSH keys, tokens, `.env`, or host Git configuration — and never causes a credential prompt. It runs credential-neutral `git`; read access is yours to provision on the guest.
- **A workspace is never seeded from the host.** A recursive copy would drag the host `.git/` — config, hooks, host-only keys — across the VM boundary. If read access cannot be provisioned, the correct outcome is to stop at that prerequisite, not to weaken the boundary.
- **Attaching is non-destructive.** `add` clones only into a path that is absent. A checkout already there with the registered origin is verified and adopted as-is. Anything else — not a repository, a different origin, unreadable — is a hard stop. Nothing is overwritten, reset, cleaned, deleted, or recloned over.
- **No substitution.** If the exact remote is unreadable, Torio does not swap in another repository and does not attempt an auth workaround.
- **Writes to history are yours.** The control plane runs no `git commit`, `push`, merge, deploy, or release. The persistent backend holds read access only; write capability exists solely inside a `torio project shell` session you opened, and leaves when you exit it.
