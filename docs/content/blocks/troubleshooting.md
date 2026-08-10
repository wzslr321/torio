## Work out why something isn't running {#troubleshooting}

Most first-run failures are one of the cases below. On failure `torio` exits
non-zero with a specific code and prints a diagnostic to stderr; match the
symptom, then apply the fix.

| What you see | What it means, and what to do |
| --- | --- |
| `zsh: command not found: torio` | The binary is not on your `PATH`. Build it and install it once — `go build -o torio ./cmd/torio` then `sudo install -m 755 torio /usr/local/bin/torio` — or run it in place as `./torio` from the repository root. |
| `torio: no command given; run 'torio --help'` (exit 2) | You ran `torio` with no subcommand somewhere that is not a terminal, such as a pipeline or a CI job, or you passed `--json`. Add a subcommand, for example `torio vm status`, or run `torio --help` to list the command surface. On a terminal the same invocation opens the hub instead. |
| `torio: the hub needs a terminal …` (exit 3) | `torio ui` was run where standard input and standard output are not both a terminal. Run it in a terminal, or use the individual commands, which is what a script wants anyway. |
| A `vm` command fails mentioning `limactl` (exit 8) | Lima is not installed, or `limactl` is not on your `PATH`. Install Lima and confirm `limactl` runs; Torio drives the VM through it. |
| `torio: stopped`, or a precondition error (exit 3) | The VM is not running. Start it with `torio vm start`, then re-run your command. |
| `torio: not_found` from `torio vm status` (exit 0) | No VM exists yet. This is the answer on a host that has never run Torio; create one with `torio vm init`. |
| `torio: timeout … exceeds policy maximum 10m0s` (exit 2) | `--timeout` is capped at ten minutes for any single operation, and the check runs before any work. Ask for `10m` or less. |
| A Desktop session does not offer `torio-brain` although `torio brain status` says the skill is installed | Hermes caches the assembled skills prompt in the backend process. Restart the backend — `torio serve restart --timeout 2m` — not just the Desktop connection. |
| `torio brain status` reports `retrieval_skill_drift` on a guest that was working | Expected after upgrading Torio: the skill moved into its own category, and a guest still holding the older copy has two files claiming one skill name, which Hermes refuses to load rather than choosing between. Run `torio brain init` to retire the old copy, then `torio serve install` and `torio serve restart --timeout 2m` so the backend picks up the regenerated unit. |
| `torio serve status` exits **3** | The backend service is not installed or not active. Run `torio serve install`, then `torio serve start`. |
| `torio serve status` exits **6** | The service is active but the loopback endpoint did not answer. Check `torio serve logs`, then `torio serve restart`. |
| `torio mcp status` reports that policy services require login | The broker and policy are installed, but the unit is intentionally dormant. Run `torio mcp login <service>` for every reported policy service; the last successful login starts the unit. |
| `torio mcp login <service>` cannot open its callback listener | Local port `43119` is already in use or the SSH callback forward could not bind. Stop the process using that loopback port and retry; do not widen the bind address. |
| `curl` to `127.0.0.1:19119/api/status` hangs or fails | The SSH tunnel is not up, or you forwarded a different local port. Re-open the forward and curl the same port you forwarded. |
| `overall:degraded` in `/api/status` | Hermes reports one of its own optional components as down — its messaging gateway, which Torio neither installs nor manages. The backend and dashboard components are still `ok`; no action needed. |
| `401` on `/api/*` while `/api/status` returns `200` | The `X-Hermes-Session-Token` gate. Headless `serve` surfaces no token, so pin one in the systemd drop-in and give Desktop the same value — see [Pin a session token](#session-token). |
| `torio project add` fails saying the guest cannot read the remote (exit 7) | Read access does not exist on the guest yet. Torio stores no credentials and will not prompt, so re-running changes nothing: grant the guest read access yourself, outside Torio, then attach again. |
| `torio project shell` refuses before opening a session | A preflight did not hold — most often an empty SSH agent. Check `ssh-add -l` lists an identity; the other causes (project not registered, VM not bootstrap-verified, checkout missing) are named in the error. |
| Desktop's file tree shows the Hermes source checkout, not your project | Settings → Workspace → *Working Directory* is still `.`, which resolves against the serve unit's `WorkingDirectory`. Set the project's derived path — see [Point Desktop at a project](#desktop-workspace). |
| `agent init failed: No … credentials stored` | No provider is configured on the guest. Run the interactive picker as `hermes` in a real shell — see [Configure a model provider](#provider-auth). `torio vm ssh` cannot do this; it forwards no TTY. |
| `Failed to connect to bus: No medium found` | `systemctl --user` under plain `sudo -u hermes` has no `XDG_RUNTIME_DIR`. Pass it explicitly: `env XDG_RUNTIME_DIR=/run/user/1000`. |
| A file you piped through `torio vm ssh` is empty, but the command exited `0` | `torio vm ssh` forwards no stdin, so `… \| torio vm ssh -- … tee file` writes nothing and still reports success. Create the file in an interactive shell (`limactl shell torio`, then `sudo -iu hermes`) instead. |

Add `--json` to any command for a single machine-readable envelope on stdout;
human diagnostics always go to stderr.
