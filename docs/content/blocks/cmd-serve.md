## Command surface — `torio serve` {#serve}

Manages the loopback-only Hermes Desktop backend as a user systemd service. It
accepts no secrets.

| Command | What it does |
| --- | --- |
| `torio serve install` | Generate, validate (with `systemd-analyze`), and enable the backend user service. Idempotent; does not start the backend. |
| `torio serve start` | Start the backend and prove loopback readiness. |
| `torio serve stop` | Stop the backend service. |
| `torio serve restart` | Restart the backend and prove loopback readiness. |
| `torio serve status` | Report systemd state and loopback endpoint readiness. |
| `torio serve logs` | Show recent, bounded, redacted, unit-scoped service logs. Accepts `--lines N`. |
