## Command surface — `torio vm` {#vm}

Controls the `torio` Lima VM. `torio vm init` creates it; every other subcommand
operates on the existing instance. The parent takes no action itself; an absent
or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio vm init` | Create the Torio VM from the embedded pinned template, or succeed idempotently when a compatible instance already exists. Incompatible instances fail closed — there is no `--force`. The global `--backend NAME` selects which box this is and records the agent it runs, fixed at creation. Sizing: `--cpus N`, `--memory SIZE`, `--disk SIZE` (defaults 4, `8GiB`, `60GiB`). They apply at creation only, since `init` never recreates. |
| `torio vm status` | Report the Torio VM state. |
| `torio vm start` | Start the VM. Idempotent; confirms a `Running` post-state before reporting success. |
| `torio vm stop` | Stop the VM. Graceful and idempotent; never uses `--force`, never removes the VM or its data, and requires a `Stopped` post-state. |
| `torio vm bootstrap` | Reconcile and verify the existing target for the declared backend. Installs the backend at its pin when it is missing; verifies operator membership in `torio-projects`. Idempotent on a reconciled target. Accepts `--timeout` and `--json`. |
| `torio vm ssh -- COMMAND…` | Run a command inside the VM. Does not open an interactive shell, forward stdin or a TTY, migrate data, start a chat, or copy credentials. |
| `torio vm shell` | Open the Lima login identity's own shell inside the box, also on `s` on the hub's dashboard. No SSH agent is forwarded and no multiplexed connection is reused, so the session cannot ride or become a push-capable one. Interactive; `--json` is a usage error. |
