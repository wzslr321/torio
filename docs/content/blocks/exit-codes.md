## Readiness and exit semantics {#readiness}

- `torio vm bootstrap` fails closed on any drift or unverifiable state with **exit 6** and remediation. A rerun is success only when every postcondition is proven.
- `torio serve start` and `restart` fail closed unless the systemd state is active *and* `GET /api/status` answers `200` through loopback.
- `torio serve status` exits non-zero when not ready: **3** = not installed or inactive; **6** = active but the endpoint is dead.
- `torio` with no subcommand is a usage error (**exit 2**); a missing `limactl` is **exit 8**; an unmet precondition such as a stopped VM is **exit 3**.

Use `--json` where offered for a single machine-readable envelope on stdout;
human logs go to stderr.
