## Fixed boundaries {#boundaries}

| Thing | Value |
| --- | --- |
| VM | `torio` — a Lima instance Torio creates from a pinned template and never re-images or deletes |
| Guest identity | `hermes` |
| Profile / application state | `/home/hermes/.hermes` (native Linux filesystem; Hermes `$HERMES_HOME`, not the Second Brain) |
| Second Brain vault | `/home/hermes/brain` (native Linux filesystem; separate from profile) |
| Workspace root | `/home/hermes/projects` (setgid `torio-projects` shared workspaces) |
| A project's checkout | `/home/hermes/projects/<id>` — always derived from the project id, never supplied |
| Backend bind | `127.0.0.1:9119` inside the VM (never a public address) |
| Backend API auth | non-public `/api/*` requires an `X-Hermes-Session-Token` header; `/api/status` is public. Headless `serve` surfaces no token, so the operator pins one. |
| Serve unit working directory | `/home/hermes/hermes-agent` — which is why Desktop's working directory must be set explicitly |

Rootful Docker for the `hermes` guest identity is forbidden. Bootstrap verifies
`hermes` is **not** in the `docker` group.
