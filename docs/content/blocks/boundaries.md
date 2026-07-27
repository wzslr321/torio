## Fixed boundaries {#boundaries}

| Thing | Value in V0 |
| --- | --- |
| VM | `torio` — existing, never created or destroyed by Torio |
| Guest identity | `hermes` |
| Knowledge base / profile | `/home/hermes/.hermes` (native Linux filesystem) |
| Workspace root | `/home/hermes/projects` |
| Code V0 workspace | `/home/hermes/projects/REDACTED-PROJECT` — one hardcoded path |
| Code V0 remote | `https://github.com/REDACTED/REDACTED` |
| Backend bind | `127.0.0.1:9119` inside the VM (never a public address) |
| Backend API auth | non-public `/api/*` requires an `X-Hermes-Session-Token` header; `/api/status` is public. Headless `serve` surfaces no token, so the operator pins one. |
| Serve unit working directory | `/home/hermes/hermes-agent` — which is why Desktop's working directory must be set explicitly |
