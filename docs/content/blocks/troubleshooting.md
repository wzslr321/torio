## Work out why something isn't running {#troubleshooting}

Most first-run failures are one of the cases below. On failure `torio` exits
non-zero with a specific code and prints a diagnostic to stderr; match the
symptom, then apply the fix.

| What you see | What it means, and what to do |
| --- | --- |
| `zsh: command not found: torio` | The binary is not on your `PATH`. Build it and install it once — `go build -o torio ./cmd/torio` then `sudo install -m 755 torio /usr/local/bin/torio` — or run it in place as `./torio` from the repository root. |
| `torio: no command given; run 'torio --help'` (exit 2) | You ran `torio` with no subcommand somewhere that is not a terminal, such as a pipeline or a CI job, or you passed `--json`. Add a subcommand, for example `torio vm status`, or run `torio --help` to list the command surface. On a terminal the same invocation opens the hub instead. |
| `torio: the hub requires a terminal …` (exit 3) | `torio ui` was run where standard input and standard output are not both a terminal. Run it in a terminal, or use an individual command. |
| A `vm` command fails mentioning `limactl` (exit 8) | Lima is not installed, or `limactl` is not on your `PATH`. Install Lima and confirm `limactl` runs; Torio drives the VM through it. |
| `torio: stopped`, or a precondition error (exit 3) | The VM is not running. Start it with `torio vm start`, then re-run your command. |
| `torio: not_found` from `torio vm status` (exit 0) | No VM exists yet. This is the answer on a host that has never run Torio; create one with `torio vm init`. |
| `torio: timeout … exceeds policy maximum 10m0s` (exit 2) | `--timeout` is capped at ten minutes for any single operation, and the check runs before any work. Ask for `10m` or less. |
| `torio mcp status` reports that policy services require login | The broker and policy are installed, but the unit is intentionally dormant. Run `torio mcp login <service>` for every reported policy service; the last successful login starts the unit. |
| `torio mcp login <service>` cannot open its callback listener | Local port `43119` is already in use or the SSH callback forward could not bind. Stop the process using that loopback port and retry; do not widen the bind address. |
| `torio project add` prints a deploy key and exits 7 | Add the public key to that repository as a deploy key with write access off, then run the same command again. Do not add it to your account; Torio cannot verify the forge setting. |
| `torio project shell` refuses before opening a session | A preflight did not hold — most often an empty SSH agent. Check `ssh-add -l` lists an identity; the other causes (project not registered, VM not bootstrap-verified, checkout missing) are named in the error. |
| `agent init failed: No … credentials stored` | No provider is configured on the guest. Run the interactive picker as the agent identity in a real shell — see [Configure a model provider](#provider-auth). `torio vm ssh` cannot do this; it forwards no TTY. |
| A file you piped through `torio vm ssh` is empty, but the command exited `0` | `torio vm ssh` forwards no stdin, so `… \| torio vm ssh -- … tee file` writes nothing and still reports success. Create the file in an interactive shell (`limactl shell torio-claude-code`, then `sudo -iu claude`) instead. |

Add `--json` to a non-interactive command for a single machine-readable envelope
on stdout; human diagnostics stay on stderr.
