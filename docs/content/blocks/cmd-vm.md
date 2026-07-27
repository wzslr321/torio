## Command surface — `torio vm` {#vm}

Controls the existing `torio` VM. The parent takes no action itself; an
absent or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio vm status` | Report the Torio VM state. |
| `torio vm start` | Start the VM. Idempotent; confirms a `Running` post-state before reporting success. |
| `torio vm stop` | Stop the VM. Graceful and idempotent; never uses `--force`, never removes the VM or its data, and requires a `Stopped` post-state. |
| `torio vm bootstrap` | Reconcile and verify the existing target for Remote Second Brain V1. Idempotent; mutates nothing on a reconciled target. Accepts `--timeout` and `--json`. |
| `torio vm ssh -- COMMAND…` | Run a command inside the VM. Does not open an interactive shell, forward stdin or a TTY, migrate data, start a chat, or copy credentials. |
