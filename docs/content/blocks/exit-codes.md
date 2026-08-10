## Readiness and exit semantics {#readiness}

- `torio vm bootstrap` fails closed on any drift or unverifiable state with **exit 6** and remediation. A rerun is success only when every postcondition is proven.
- `torio serve start` and `restart` fail closed unless the systemd state is active *and* `GET /api/status` answers `200` through loopback.
- `torio serve status` exits non-zero when not ready: **3** = not installed or inactive; **6** = active but the endpoint is dead.
- Bare `torio` opens the hub when stdin and stdout are terminals; otherwise it is a usage error (**exit 2**). A missing `limactl` is **exit 8**; an unmet precondition such as a stopped VM is **exit 3**.

### Global flags {#global-flags}

Four flags are accepted by every command, before or after the subcommand. This
is the whole list; an unknown flag is a usage error (**exit 2**), never silently
ignored, and there is no global `--force`.

| Flag | What it does |
| --- | --- |
| `--json` | Emit a single machine-readable JSON document on stdout, where the command has one to emit; human logs stay on stderr. `--help` is the one exception — it prints usage and exits `0` without an envelope. |
| `--verbose` | Raise stderr diagnostics from warnings to debug. Stdout is untouched, so machine output is identical with and without it, and the extra lines are redacted like every other diagnostic. |
| `--timeout DURATION` | Bound the operation. Default `30s`, or `default_timeout` from the config file when set; an explicit flag always wins. Anything above the policy maximum `10m` is rejected before any work starts. |
| `--config PATH` | Read the non-secret config document from `PATH` instead of `$XDG_CONFIG_HOME/torio/config.json`. It bypasses XDG entirely, is resolved and validated rather than merely parsed, and applies to the project registry as well; a missing file or an invalid document is a usage error (**exit 2**). |
