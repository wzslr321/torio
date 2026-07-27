## Bring up the loopback backend {#serve-bring-up}

Install and start the persistent Hermes backend as a user systemd service inside
the VM, using the existing `/home/hermes/.hermes` profile. It binds guest
loopback only (`127.0.0.1:9119`) and never a public address:

```bash
torio serve install --timeout 2m
torio serve start   --timeout 2m
torio serve status
```

- `install` ensures user linger, renders the unit (loopback bind, `HERMES_HOME`, `Restart=always`), validates it with `systemd-analyze` **before** activation, then reloads and enables it for boot. Idempotent; accepts no secrets; does not start the backend.
- `start` starts it and fails closed unless the systemd state is active **and** `GET /api/status` answers 200 through loopback.
- `status` proves the same and exits non-zero when not ready. `stop` and `restart` mirror the lifecycle. `logs [--lines N]` shows bounded, redacted, unit-scoped journal entries only.
