## Command surface — `torio version` {#version}

| Command | What it does |
| --- | --- |
| `torio version` | Print the version, commit, build date, and Go toolchain of the binary you are running. Accepts `--json`. |

This is the only place Torio names its own version. Nothing else in the CLI or
in these docs is labelled by release, because the label would not tell you
anything you could act on — and it would be wrong the moment the next one shipped.

A binary built straight from a checkout reports `dev` with an unknown commit;
that is expected, not a fault.
