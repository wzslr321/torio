## Command surface — `torio vm` {#vm}

Controls the `torio` Lima VM. `torio vm init` creates it; every other subcommand
operates on the existing instance. The parent takes no action itself; an absent
or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio vm init` | Create the Torio VM from the embedded Gate-0 template, or succeed idempotently when a compatible instance already exists. Incompatible instances fail closed — there is no `--force`. Sizing: `--cpus N`, `--memory SIZE`, `--disk SIZE` (defaults 4, `8GiB`, `60GiB`). They apply at creation only, since `init` never recreates. |
| `torio vm status` | Report the Torio VM state. |
| `torio vm start` | Start the VM. Idempotent; confirms a `Running` post-state before reporting success. |
| `torio vm stop` | Stop the VM. Graceful and idempotent; never uses `--force`, never removes the VM or its data, and requires a `Stopped` post-state. |
| `torio vm bootstrap` | Reconcile and verify the existing target for Hermes. Installs the pinned Hermes Agent when the launcher is missing; verifies operator membership in `torio-projects`. Idempotent on a reconciled target. Accepts `--timeout` and `--json`. |
| `torio vm ssh -- COMMAND…` | Run a command inside the VM. Does not open an interactive shell, forward stdin or a TTY, migrate data, start a chat, or copy credentials. |
