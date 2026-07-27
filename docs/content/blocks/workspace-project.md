## Register the Hermes project {#workspace-project}

Hermes exposes native **projects** — human-named workspaces that can span
multiple folders or repositories, and that anchor desktop session grouping
(state is per-profile). This is the supported way to bind Hermes to this one
workspace — **not** the systemd unit's working directory. Register the single
path and make it active:

```bash
torio vm ssh -- sudo -u hermes -- \
    hermes project create REDACTED-PROJECT /home/hermes/projects/REDACTED-PROJECT --use
torio vm ssh -- sudo -u hermes -- hermes project list   # the active project is marked with *
torio vm ssh -- sudo -u hermes -- hermes project show REDACTED-PROJECT
```

Do **not** pass `--board`: kanban binding is the excluded worker path.
