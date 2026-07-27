## Work out why something isn't running {#troubleshooting}

Most first-run failures are one of the cases below. On failure `torio` exits
non-zero with a specific code and prints a diagnostic to stderr; match the
symptom, then apply the fix.

| What you see | What it means, and what to do |
| --- | --- |
| `zsh: command not found: torio` | The binary is not on your `PATH`. Build it and install it once — `go build -o torio ./cmd/torio` then `sudo install -m 755 torio /usr/local/bin/torio` — or run it in place as `./torio` from the repository root. |
| `torio: no command given; run 'torio --help'` (exit 2) | You ran `torio` with no subcommand. Add one, for example `torio vm status`, or run `torio --help` to list the command surface. |
| A `vm` command fails mentioning `limactl` (exit 8) | Lima is not installed, or `limactl` is not on your `PATH`. Install Lima and confirm `limactl` runs; Torio drives the VM through it. |
| `torio: Stopped`, or a precondition error (exit 3) | The VM is not running. Start it with `torio vm start`, then re-run your command. |
| `torio serve status` exits **3** | The backend service is not installed or not active. Run `torio serve install`, then `torio serve start`. |
| `torio serve status` exits **6** | The service is active but the loopback endpoint did not answer. Check `torio serve logs`, then `torio serve restart`. |
| `curl` to `127.0.0.1:19119/api/status` hangs or fails | The SSH tunnel is not up, or you forwarded a different local port. Re-open the forward and curl the same port you forwarded. |
| `overall:degraded` in `/api/status` | Expected when the messaging gateway is stopped. The serve backend/dashboard component is still `ok`; no action needed. |
| `401` on `/api/*` while `/api/status` returns `200` | The `X-Hermes-Session-Token` gate. Headless `serve` surfaces no token, so pin one in the systemd drop-in and give Desktop the same value — see [Pin a session token](#session-token). |
| Desktop's file tree shows the Hermes source checkout, not the Code V0 workspace | Settings → Workspace → *Working Directory* is still `.`, which resolves against the serve unit's `WorkingDirectory`. Set the fixed workspace path — see [Point Desktop at the Code V0 workspace](#desktop-workspace). |
| `agent init failed: No … credentials stored` | No provider is configured on the guest. Run the interactive picker as `hermes` in a real shell — see [Configure a model provider](#provider-auth). `torio vm ssh` cannot do this; it forwards no TTY. |
| `Failed to connect to bus: No medium found` | `systemctl --user` under plain `sudo -u hermes` has no `XDG_RUNTIME_DIR`. Pass it explicitly: `env XDG_RUNTIME_DIR=/run/user/1000`. |
| A file you piped through `torio vm ssh` is empty, but the command exited `0` | `torio vm ssh` forwards no stdin, so `… \| torio vm ssh -- … tee file` writes nothing and still reports success. Create the file in an interactive shell (`limactl shell torio`, then `sudo -iu hermes`) instead. |

Add `--json` to any command for a single machine-readable envelope on stdout;
human diagnostics always go to stderr.
