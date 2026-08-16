## Fixed boundaries {#boundaries}

| Thing | Value |
| --- | --- |
| VM | `torio-<backend>` — a Lima instance Torio creates from a pinned template and never re-images or deletes |
| Guest identity | the backend's own: `claude` on Claude Code, `codex` on Codex |
| Profile / application state | `/home/<identity>/.<backend>` (native Linux filesystem; not the Second Brain) |
| Second Brain vault | `/home/<identity>/brain` (native Linux filesystem; separate from profile) |
| Workspace root | `/home/<identity>/projects` (setgid `torio-projects` shared workspaces) |
| A project's checkout | `/home/<identity>/projects/<id>` — always derived from the project id, never supplied |

Rootful Docker for the agent's guest identity is forbidden. Bootstrap verifies
it is **not** in the `docker` group.
