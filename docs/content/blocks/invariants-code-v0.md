## Invariants (hard stops) {#invariants}

- **Credential- and config-neutral control plane.** The control plane never copies or materializes host Git state in the VM — no host `.git/`, `.git/config`, hooks, SSH keys, personal access tokens, `.env`, or host Git configuration — and never causes a credential prompt. Read access is provisioned by a **human** directly on the guest; the control plane only runs credential-neutral `git`.
- **No host-copy seed.** Do not seed the workspace by copying a host checkout into the guest: a recursive copy drags the host `.git/` (config, hooks, host-only keys) across the boundary. If read access cannot be provisioned, **stop at the human prerequisite** — do not weaken the boundary.
- **Non-destructive workspace gate.** Clone only when the fixed guest path is **absent**. An existing Git repo with exact `origin`, on `main`, clean is **retained as-is** (verify only). A non-repo, origin mismatch, dirty tree, or wrong branch is a **hard stop** with no destructive action. Never overwrite, reset, clean, delete, or reclone over an existing checkout.
- **No substitution.** If the exact remote is unreadable, do not swap in another repository or attempt an auth workaround.
- **Human-only writes.** No `git commit`, `git push`, merge, deploy, or release by the control plane. Push requires credentials scoped for write, which are not provisioned.
